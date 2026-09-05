package cli

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newAccessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Manage access control for subdomains and projects",
	}
	cmd.AddCommand(newAccessSetCmd())
	cmd.AddCommand(newAccessGetCmd())
	cmd.AddCommand(newAccessRemoveCmd())
	return cmd
}

func newAccessSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set access control rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, _ := cmd.Flags().GetString("subdomain")
			project, _ := cmd.Flags().GetString("project")
			ips, _ := cmd.Flags().GetStringSlice("ip")
			password, _ := cmd.Flags().GetString("password")
			expire, _ := cmd.Flags().GetString("expire")
			weworkEnabled, _ := cmd.Flags().GetBool("wework")
			weworkUsers, _ := cmd.Flags().GetStringSlice("wework-user")

			if sub == "" {
				return fmt.Errorf("--subdomain is required")
			}

			reqBody := map[string]any{}
			if len(ips) > 0 {
				reqBody["allowed_ips"] = ips
			}
			if password == "auto" {
				reqBody["auto_password"] = true
			} else if password != "" {
				reqBody["password"] = password
			}
			if weworkEnabled {
				reqBody["wework_enabled"] = true
			}
			if len(weworkUsers) > 0 {
				reqBody["allowed_wework_users"] = weworkUsers
				// Implicitly enable WeWork when allow-list is provided.
				reqBody["wework_enabled"] = true
			}

			if expire != "" {
				ttl, err := parseDuration(expire)
				if err != nil {
					return fmt.Errorf("invalid --expire value: %w", err)
				}
				reqBody["session_ttl"] = int(ttl.Seconds())
			}

			cfg, err := LoadConfig()

			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/access", sub)
			if project != "" {
				apiPath = fmt.Sprintf("/subdomains/%s/projects/%s/access", sub, project)
			}

			var result map[string]any
			if err := client.doJSONContext(cmd.Context(), "PUT", apiPath, reqBody, &result); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Access control updated.")

			// Determine password to display.
			var displayPassword string
			if gp, ok := result["generated_password"].(string); ok && gp != "" {
				displayPassword = gp
			} else if password != "" && password != "auto" {
				displayPassword = password
			}

			// Build share line if site URL can be derived.
			siteURL := buildAccessURL(cfg.APIURL, sub, project)
			if siteURL != "" {
				var ips []any
				if ipList, ok := result["allowed_ips"].([]any); ok {
					ips = ipList
				}

				var ttl float64
				if t, ok := result["session_ttl"].(float64); ok {
					ttl = t
				}

				fmt.Fprintln(cmd.OutOrStdout(), buildShareLine(siteURL, displayPassword, ips, ttl))
			} else {
				// Fallback: original format for non-standard API URLs.
				if ips := result["allowed_ips"]; ips != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  IP whitelist: %v\n", ips)
				}
				if displayPassword != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  Password: %s\n", displayPassword)
				} else if result["has_password"] == true {
					fmt.Fprintln(cmd.OutOrStdout(), "  Password: (set)")
				}
				if ttl, ok := result["session_ttl"].(float64); ok {
					fmt.Fprintf(cmd.OutOrStdout(), "  Session TTL: %s\n", formatTTL(ttl))
				}
			}

			if result["wework_enabled"] == true {
				if users, ok := result["allowed_wework_users"].([]any); ok && len(users) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "  WeCom login: enabled (allow-list: %v)\n", users)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "  WeCom login: enabled (any corp member)")
				}
			}

			return nil
		},
	}

	cmd.Flags().String("subdomain", "", "Subdomain name (required)")
	cmd.Flags().String("project", "", "Project name (optional, for project-level rules)")
	cmd.Flags().StringSlice("ip", nil, "Allowed IP or CIDR (repeatable)")
	cmd.Flags().String("password", "", "Password ('auto' to generate, or a custom value)")
	cmd.Flags().String("expire", "24h", "Session expiry duration (e.g. 1h, 24h, 7d, never)")
	cmd.Flags().Bool("wework", false, "Enable WeCom (WeWork) QR code login")
	cmd.Flags().StringSlice("wework-user", nil, "Allowed WeCom user_id (repeatable; empty = any corp member; implies --wework)")

	return cmd
}

func newAccessGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show access control rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, _ := cmd.Flags().GetString("subdomain")
			project, _ := cmd.Flags().GetString("project")

			if sub == "" {
				return fmt.Errorf("--subdomain is required")
			}

			cfg, err := LoadConfig()

			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/access", sub)
			if project != "" {
				apiPath = fmt.Sprintf("/subdomains/%s/projects/%s/access", sub, project)
			}

			var result map[string]any
			if err := client.doJSONContext(cmd.Context(), "GET", apiPath, nil, &result); err != nil {
				return err
			}

			target := sub
			if project != "" {
				target = sub + "/" + project
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Access control for %s:\n", target)
			if ips := result["allowed_ips"]; ips != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  IP whitelist: %v\n", ips)
			}
			if result["has_password"] == true {
				fmt.Fprintln(cmd.OutOrStdout(), "  Password: (set)")
				if ttl, ok := result["session_ttl"].(float64); ok {
					fmt.Fprintf(cmd.OutOrStdout(), "  Session TTL: %s\n", formatTTL(ttl))
				}
			}
			if result["wework_enabled"] == true {
				if users, ok := result["allowed_wework_users"].([]any); ok && len(users) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "  WeCom login: enabled (allow-list: %v)\n", users)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "  WeCom login: enabled (any corp member)")
				}
			}
			return nil
		},
	}

	cmd.Flags().String("subdomain", "", "Subdomain name (required)")
	cmd.Flags().String("project", "", "Project name (optional)")

	return cmd
}

func newAccessRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove access control rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			sub, _ := cmd.Flags().GetString("subdomain")
			project, _ := cmd.Flags().GetString("project")

			if sub == "" {
				return fmt.Errorf("--subdomain is required")
			}

			cfg, err := LoadConfig()

			if err != nil {
				return err
			}
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/access", sub)
			if project != "" {
				apiPath = fmt.Sprintf("/subdomains/%s/projects/%s/access", sub, project)
			}

			if err := client.doJSONContext(cmd.Context(), "DELETE", apiPath, nil, nil); err != nil {
				return err
			}

			target := sub
			if project != "" {
				target = sub + "/" + project
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Access control removed for %s.\n", target)
			return nil
		},
	}

	cmd.Flags().String("subdomain", "", "Subdomain name (required)")
	cmd.Flags().String("project", "", "Project name (optional)")

	return cmd
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "never") {
		return 87600 * time.Hour, nil // 10 years
	}
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func formatTTL(seconds float64) string {
	s := int(seconds)
	if s >= 315360000 {
		return "never"
	}
	if s >= 86400 && s%86400 == 0 {
		return fmt.Sprintf("%dd", s/86400)
	}
	return (time.Duration(s) * time.Second).String()
}

func buildShareLine(url, password string, ips []any, ttlSeconds float64) string {
	parts := []string{url}
	if password != "" {
		parts = append(parts, "Password: "+password)
	}
	if len(ips) > 0 {
		ipStrs := make([]string, len(ips))
		for i, ip := range ips {
			ipStrs[i] = fmt.Sprintf("%v", ip)
		}
		parts = append(parts, "IP: "+strings.Join(ipStrs, ", "))
	}
	if password != "" && ttlSeconds > 0 {
		parts = append(parts, "Expires: "+formatTTL(ttlSeconds))
	}
	return strings.Join(parts, " | ")
}

func buildAccessURL(apiURL, subdomain, project string) string {
	u, err := url.Parse(apiURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if !strings.HasPrefix(host, "api.") {
		return ""
	}
	siteHost := strings.TrimPrefix(host, "api.")
	siteHost = subdomain + "." + siteHost
	if port := u.Port(); port != "" {
		siteHost += ":" + port
	}
	result := u.Scheme + "://" + siteHost
	if project != "" {
		result += "/" + project
	}
	return result
}
