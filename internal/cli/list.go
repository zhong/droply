package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var sub string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects in a subdomain",
		RunE:  func(cmd *cobra.Command, args []string) error { return runProjectList(cmd, sub) },
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	return cmd
}

func runProjectList(cmd *cobra.Command, sub string) error {
	if sub == "" {
		pc, err := loadOptionalProjectConfig()
		if err != nil {
			return err
		}
		if pc != nil {
			sub = pc.Subdomain
		}
	}
	if sub == "" {
		return fmt.Errorf("subdomain is required: use --sub or set subdomain in .droply.toml")
	}

	cfg, err := LoadConfig()

	if err != nil {
		return err
	}
	client := NewAPIClient(cfg)

	var projects []struct {
		Name string `json:"name"`
	}
	if err := client.doJSONContext(cmd.Context(), "GET", "/subdomains/"+sub+"/projects", nil, &projects); err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No projects found in subdomain %q.\n", sub)
		return nil
	}
	for _, p := range projects {
		fmt.Fprintf(cmd.OutOrStdout(), "%s/%s\n", sub, p.Name)
	}
	return nil
}
