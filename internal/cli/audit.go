package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "audit", Args: cobra.NoArgs, Short: "Query sanitized project audit events as JSON", SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		admin, _ := cmd.Flags().GetBool("admin")
		limit, _ := cmd.Flags().GetInt("limit")
		before, _ := cmd.Flags().GetInt64("before")
		if limit < 1 || limit > 100 || before < 0 {
			return errors.New("--limit must be 1–100 and --before non-negative")
		}
		path := "/admin/audit"
		if !admin {
			base, err := deploymentAPIPath(cmd)
			if err != nil {
				return err
			}
			path = base + "/audit"
		}
		query := url.Values{"limit": {strconv.Itoa(limit)}, "before": {strconv.FormatInt(before, 10)}}
		var result json.RawMessage
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if err := NewAPIClient(cfg).doJSON(http.MethodGet, path+"?"+query.Encode(), nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	cmd.Flags().String("sub", "", "Subdomain name (or .droply.toml)")
	cmd.Flags().String("project", "", "Project name (or .droply.toml)")
	cmd.Flags().Bool("admin", false, "Query installation-wide audit as an administrator")
	cmd.Flags().Int("limit", 50, "Page size (1–100)")
	cmd.Flags().Int64("before", 0, "Continue before this audit event ID")
	return cmd
}
