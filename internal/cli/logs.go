package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var sub string
	var limit int
	var pathFilter string

	cmd := &cobra.Command{
		Use:   "logs [project]",
		Short: "Show detailed visit logs for a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName := ""
			if len(args) > 0 {
				projectName = args[0]
			}

			if sub == "" || projectName == "" {
				pc, err := LoadProjectConfig()
				if err != nil {
					return fmt.Errorf("--sub and project name required (or set .droply.toml): %w", err)
				}
				if sub == "" {
					sub = pc.Subdomain
				}
				if projectName == "" {
					projectName = pc.Project
				}
			}
			if sub == "" || projectName == "" {
				return fmt.Errorf("subdomain and project name required: use --sub and argument, or set .droply.toml")
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/logs?limit=%d", sub, projectName, limit)
			if pathFilter != "" {
				apiPath += fmt.Sprintf("&path=%s", pathFilter)
			}

			var resp struct {
				Logs  []struct {
					Path      string `json:"path"`
					IP        string `json:"ip"`
					Referer   string `json:"referer"`
					UserAgent string `json:"user_agent"`
					VisitedAt string `json:"visited_at"`
				} `json:"logs"`
				Total int `json:"total"`
			}
			if err := client.doJSON("GET", apiPath, nil, &resp); err != nil {
				return err
			}

			fmt.Printf("Project: %s/%s  |  Showing %d of %d logs\n\n", sub, projectName, len(resp.Logs), resp.Total)

			if len(resp.Logs) == 0 {
				fmt.Println("No visit logs found.")
				return nil
			}

			for _, l := range resp.Logs {
				fmt.Printf("  %s  %s  %s  %s\n", l.VisitedAt, l.Path, l.IP, truncate(l.Referer, 40))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	cmd.Flags().IntVar(&limit, "limit", 50, "Number of logs to show (max 500)")
	cmd.Flags().StringVar(&pathFilter, "path", "", "Filter by path prefix")
	return cmd
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}