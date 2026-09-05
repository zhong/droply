package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSubdomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subdomain",
		Short: "Manage subdomains",
	}
	cmd.AddCommand(newSubdomainCreateCmd())
	cmd.AddCommand(newSubdomainListCmd())
	cmd.AddCommand(newSubdomainDeleteCmd())
	return cmd
}

func newSubdomainCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new subdomain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			var result map[string]any
			if err := client.doJSONContext(cmd.Context(), "POST", "/subdomains", map[string]string{"name": name}, &result); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Subdomain %q created successfully.\n", name)
			return nil
		},
	}
}

func newSubdomainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your subdomains",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			var subdomains []struct {
				Name         string `json:"name"`
				ProjectCount int    `json:"project_count"`
			}
			if err := client.doJSONContext(cmd.Context(), "GET", "/subdomains", nil, &subdomains); err != nil {
				return err
			}
			if len(subdomains) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No subdomains found.")
				return nil
			}
			for _, s := range subdomains {
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s  %d project(s)\n", s.Name, s.ProjectCount)
			}
			return nil
		},
	}
}

func newSubdomainDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a subdomain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			if err := client.doJSONContext(cmd.Context(), "DELETE", "/subdomains/"+name, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Subdomain %q deleted.\n", name)
			return nil
		},
	}
}
