package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var sub string
	var period string

	cmd := &cobra.Command{
		Use:   "stats [project]",
		Short: "Show page view statistics for a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName := ""
			if len(args) > 0 {
				projectName = args[0]
			}

			if sub == "" || projectName == "" {
				pc, err := loadOptionalProjectConfig()
				if err != nil {
					return err
				}
				if pc != nil && sub == "" {
					sub = pc.Subdomain
				}
				if pc != nil && projectName == "" {
					projectName = pc.Project
				}
			}
			if sub == "" || projectName == "" {
				return fmt.Errorf("subdomain and project name required: use --sub and argument, or set .droply.toml")
			}

			cfg, err := LoadConfig()

			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			path := fmt.Sprintf("/subdomains/%s/projects/%s/stats?period=%s", sub, projectName, period)

			var resp struct {
				TotalPV int `json:"total_pv"`
				TotalUV int `json:"total_uv"`
				Pages   []struct {
					Path string `json:"path"`
					PV   int    `json:"pv"`
					UV   int    `json:"uv"`
				} `json:"pages"`
			}
			if err := client.doJSON("GET", path, nil, &resp); err != nil {
				return err
			}

			periodLabel := "all time"
			if period != "all" {
				periodLabel = fmt.Sprintf("last %s", strings.TrimSuffix(period, "d"))
				if !strings.Contains(periodLabel, "days") {
					periodLabel += " days"
				}
			}

			fmt.Printf("Project: %s/%s  |  Period: %s\n\n", sub, projectName, periodLabel)
			fmt.Printf("Total PV: %s  |  Total UV: %s\n\n", formatNum(resp.TotalPV), formatNum(resp.TotalUV))

			if len(resp.Pages) == 0 {
				fmt.Println("No page views recorded yet.")
				return nil
			}

			fmt.Println("Top Pages:")
			for _, p := range resp.Pages {
				fmt.Printf("  %-30s %s PV   %s UV\n", p.Path, formatNum(p.PV), formatNum(p.UV))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	cmd.Flags().StringVar(&period, "period", "30d", "Time period: 7d, 30d, all")
	return cmd
}

func formatNum(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	result := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}
