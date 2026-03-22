package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// resolveProject reads sub and project from cmd flags, falling back to .droply.toml.
func resolveProject(cmd *cobra.Command) (sub, proj string, err error) {
	sub, _ = cmd.Flags().GetString("sub")
	proj, _ = cmd.Flags().GetString("project")

	if sub == "" || proj == "" {
		pc, pcErr := LoadProjectConfig()
		if pcErr == nil {
			if sub == "" {
				sub = pc.Subdomain
			}
			if proj == "" {
				proj = pc.Project
			}
		}
	}

	if sub == "" {
		return "", "", fmt.Errorf("subdomain is required: use --sub or set subdomain in .droply.toml")
	}
	if proj == "" {
		return "", "", fmt.Errorf("project is required: use --project or set project in .droply.toml")
	}
	return sub, proj, nil
}

func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "Manage custom domains for a project",
	}

	cmd.PersistentFlags().String("sub", "", "Subdomain name")
	cmd.PersistentFlags().String("project", "", "Project name")

	cmd.AddCommand(newDomainAddCmd())
	cmd.AddCommand(newDomainListCmd())
	cmd.AddCommand(newDomainRemoveCmd())
	cmd.AddCommand(newDomainVerifyCmd())
	return cmd
}

func newDomainAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <domain>",
		Short: "Add a custom domain to a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := args[0]
			sub, proj, err := resolveProject(cmd)
			if err != nil {
				return err
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			var result struct {
				CnameTarget string `json:"cname_target"`
			}
			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/domains", sub, proj)
			if err := client.doJSON("POST", apiPath, map[string]string{"domain": domain}, &result); err != nil {
				return err
			}

			fmt.Printf("Domain %q added.\n", domain)
			fmt.Printf("CNAME target: %s\n", result.CnameTarget)
			return nil
		},
	}
}

func newDomainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List custom domains for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, proj, err := resolveProject(cmd)
			if err != nil {
				return err
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			var domains []struct {
				Domain   string `json:"domain"`
				Verified bool   `json:"verified"`
			}
			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/domains", sub, proj)
			if err := client.doJSON("GET", apiPath, nil, &domains); err != nil {
				return err
			}

			if len(domains) == 0 {
				fmt.Println("No custom domains configured.")
				return nil
			}
			for _, d := range domains {
				status := "unverified"
				if d.Verified {
					status = "verified"
				}
				fmt.Printf("%-48s  %s\n", d.Domain, status)
			}
			return nil
		},
	}
}

func newDomainVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <domain>",
		Short: "Verify DNS configuration for a custom domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := args[0]
			sub, proj, err := resolveProject(cmd)
			if err != nil {
				return err
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			var result struct {
				Verified bool   `json:"verified"`
				Message  string `json:"message"`
			}
			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/domains/%s/verify", sub, proj, domain)
			if err := client.doJSON("POST", apiPath, nil, &result); err != nil {
				return err
			}

			if result.Verified {
				fmt.Printf("Domain %q verified.\n", domain)
			} else {
				fmt.Printf("Domain %q not verified: %s\n", domain, result.Message)
			}
			return nil
		},
	}
}

func newDomainRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <domain>",
		Short: "Remove a custom domain from a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := args[0]
			sub, proj, err := resolveProject(cmd)
			if err != nil {
				return err
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/domains/%s", sub, proj, domain)
			if err := client.doJSON("DELETE", apiPath, nil, nil); err != nil {
				return err
			}

			fmt.Printf("Domain %q removed.\n", domain)
			return nil
		},
	}
}
