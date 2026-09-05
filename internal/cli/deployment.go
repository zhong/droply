package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/zhong/droply/internal/model"
)

func newDeploymentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "deployment", Short: "Inspect, restore, and clean up deployment versions"}
	cmd.PersistentFlags().String("sub", "", "Subdomain name (or .droply.toml)")
	cmd.PersistentFlags().String("project", "", "Project name (or .droply.toml)")
	cmd.PersistentFlags().Bool("json", false, "Print machine-readable JSON")
	cmd.AddCommand(newDeploymentListCmd(), newDeploymentRollbackCmd(), newDeploymentCleanupCmd(), newDeploymentSwitchCmd("promote"), newDeploymentEventsCmd())
	return cmd
}

func deploymentAPIPath(cmd *cobra.Command) (string, error) {
	sub, project, err := resolveProject(cmd)
	if err != nil {
		return "", err
	}
	return "/subdomains/" + url.PathEscape(sub) + "/projects/" + url.PathEscape(project), nil
}

func newDeploymentListCmd() *cobra.Command {
	return &cobra.Command{
		SilenceUsage: true,
		Use:          "list", Short: "List versions, production state, and artifact availability", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := deploymentAPIPath(cmd)
			if err != nil {
				return err
			}
			var deployments []model.Deployment
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodGet, path+"/deployments", nil, &deployments); err != nil {
				return err
			}
			if deployments == nil {
				deployments = []model.Deployment{}
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(deployments)
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(out, "VERSION\tSTATUS\tPRODUCTION\tARTIFACT\tAVAILABLE\tSIZE\tCREATED\tFAILURE")
			for _, d := range deployments {
				fmt.Fprintf(out, "%d\t%s\t%t\t%s\t%t\t%s\t%s\t%s\n", d.Version, d.Status, d.Production, d.ArtifactState, d.Available, formatSize(d.TotalSize), d.CreatedAt.Format(time.RFC3339), d.FailureReason)
			}
			return out.Flush()
		},
	}
}

func newDeploymentRollbackCmd() *cobra.Command { return newDeploymentSwitchCmd("rollback") }

func newDeploymentSwitchCmd(action string) *cobra.Command {
	return &cobra.Command{
		SilenceUsage: true,
		Use:          action + " <version>", Short: "Commit a production switch to an available version without uploading files",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return err
			}
			version, err := strconv.Atoi(args[0])
			if err != nil || version < 1 {
				return fmt.Errorf("version must be a positive integer")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := deploymentAPIPath(cmd)
			if err != nil {
				return err
			}
			version, _ := strconv.Atoi(args[0])
			var result struct {
				Deployment model.Deployment `json:"deployment"`
				Changed    bool             `json:"changed"`
			}
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodPost, path+"/"+action+"/"+strconv.Itoa(version), nil, &result); err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if result.Changed {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Committed production switch to version %d.\n", result.Deployment.Version)
			} else {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Version %d was already in production at commit time.\n", result.Deployment.Version)
			}
			return err
		},
	}
}

func newDeploymentCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		SilenceUsage: true,
		Use:          "cleanup", Short: "Preview artifact cleanup; use --apply to delete (JSON output)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := deploymentAPIPath(cmd)
			if err != nil {
				return err
			}
			query := url.Values{}
			for _, name := range []string{"keep", "days"} {
				if !cmd.Flags().Changed(name) {
					continue
				}
				value, _ := cmd.Flags().GetInt(name)
				if value < 0 {
					return fmt.Errorf("--%s must be non-negative", name)
				}
				query.Set(name, strconv.Itoa(value))
			}
			path += "/cleanup"
			if len(query) > 0 {
				path += "?" + query.Encode()
			}
			apply, _ := cmd.Flags().GetBool("apply")
			method := http.MethodGet
			if apply {
				method = http.MethodPost
			}
			var result json.RawMessage
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), method, path, nil, &result); err != nil {
				return err
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return err
			}
			var status struct {
				Errors []json.RawMessage `json:"errors"`
			}
			if err := json.Unmarshal(result, &status); err != nil {
				return err
			}
			if len(status.Errors) > 0 {
				return fmt.Errorf("cleanup completed with %d errors; inspect the JSON result", len(status.Errors))
			}
			return nil
		},
	}
	cmd.Flags().Int("keep", 0, "Override the server's minimum retained version count")
	cmd.Flags().Int("days", 0, "Override the server's minimum retention age in days")
	cmd.Flags().Bool("apply", false, "Delete eligible artifacts; omitted means preview only")
	return cmd
}

func newDeploymentEventsCmd() *cobra.Command {
	return &cobra.Command{Use: "events", Short: "List promotion events as JSON", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := deploymentAPIPath(cmd)
			if err != nil {
				return err
			}
			var events []model.PublicationEvent
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodGet, path+"/events", nil, &events); err != nil {
				return err
			}
			if events == nil {
				events = []model.PublicationEvent{}
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(events)
		}}
}
