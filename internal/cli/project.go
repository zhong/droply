package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}
	cmd.AddCommand(newProjectListCmd())
	cmd.AddCommand(newProjectDeleteCmd())
	return cmd
}

func newProjectListCmd() *cobra.Command { return newListCmd() }

func newProjectDeleteCmd() *cobra.Command {
	var sub string

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projName := args[0]

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

			if err := client.doJSONContext(cmd.Context(), "DELETE", "/subdomains/"+sub+"/projects/"+projName, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Project %q deleted from subdomain %q.\n", projName, sub)
			return nil
		},
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	return cmd
}
