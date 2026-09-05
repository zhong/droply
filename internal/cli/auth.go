package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/spf13/cobra"
)

// readInput prints prompt and reads a line from stdin.
func readInput(prompt string) string {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

// readPassword prints prompt and reads a password without echo.
func readPassword(prompt string) string {
	fmt.Print(prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return ""
	}
	return string(b)
}

// resolveLoginTarget returns (contextName, context) for register/login commands.
// If --api-url is set, it implies a new or named context; --context names it (default: derived from URL).
// If neither flag is set, uses the currently active context.
func resolveLoginTarget(cmd *cobra.Command) (string, *Context) {
	apiURL, _ := cmd.Flags().GetString("api-url")
	ctxName, _ := cmd.Flags().GetString("context")

	full := LoadFullConfig()

	if apiURL == "" && ctxName == "" {
		// Use whatever is currently active.
		name := resolveActiveContextName(full)
		ctx := full.Contexts[name]
		if ctx.APIURL == "" {
			ctx.APIURL = defaultAPIURL
		}
		return name, &ctx
	}

	if ctxName == "" {
		ctxName = deriveContextName(apiURL)
	}
	existing, ok := full.Contexts[ctxName]
	if !ok {
		existing = Context{}
	}
	if apiURL != "" {
		existing.APIURL = apiURL
	}
	if existing.APIURL == "" {
		existing.APIURL = defaultAPIURL
	}
	return ctxName, &existing
}

// deriveContextName extracts a short, sensible context name from an API URL.
// e.g. "https://api.staging.example.com" → "example"
// Fallback: "custom" if URL is empty or unparseable.
func deriveContextName(apiURL string) string {
	if apiURL == "" {
		return "custom"
	}
	s := strings.TrimPrefix(apiURL, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "api.")
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	// Pick the second-level label if present.
	// e.g. "staging.example.com" → "example"
	parts := strings.Split(s, ".")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	if s == "" {
		return "custom"
	}
	return s
}

func saveContext(name string, ctx *Context) error {
	full := LoadFullConfig()
	full.Contexts[name] = *ctx
	if full.CurrentContext == "" {
		full.CurrentContext = name
	}
	return SaveFullConfig(full)
}

func newRegisterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Create a new droply account",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, ctx := resolveLoginTarget(cmd)
			email := readInput("Email: ")
			password := readPassword("Password: ")

			client := NewAPIClient(ctx)

			var resp struct {
				APIToken string `json:"api_token"`
			}
			invite := os.Getenv("DROPLY_INVITE")
			if err := client.doJSON("POST", "/auth/register", map[string]string{
				"invite":   invite,
				"email":    email,
				"password": password,
			}, &resp); err != nil {
				return err
			}

			ctx.Token = resp.APIToken
			if err := saveContext(name, ctx); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Registered successfully on context %q (%s). Token saved.\n", name, ctx.APIURL)
			return nil
		},
	}
	cmd.Flags().String("api-url", "", "API URL of a droply server (creates or updates the named context)")
	cmd.Flags().String("context", "", "Name for the context (default: derived from --api-url)")
	return cmd
}

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to your droply account",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, ctx := resolveLoginTarget(cmd)
			email := readInput("Email: ")
			password := readPassword("Password: ")

			client := NewAPIClient(ctx)

			var resp struct {
				APIToken string `json:"api_token"`
			}
			if err := client.doJSON("POST", "/auth/login", map[string]string{
				"email":    email,
				"password": password,
			}, &resp); err != nil {
				return err
			}

			ctx.Token = resp.APIToken
			if err := saveContext(name, ctx); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Logged in successfully on context %q (%s). Token saved.\n", name, ctx.APIURL)
			return nil
		},
	}
	cmd.Flags().String("api-url", "", "API URL of a droply server (creates or updates the named context)")
	cmd.Flags().String("context", "", "Name for the context (default: derived from --api-url)")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear the auth token for the active context",
		RunE: func(cmd *cobra.Command, args []string) error {
			full := LoadFullConfig()
			name := resolveActiveContextName(full)
			ctx := full.Contexts[name]
			ctx.Token = ""
			full.Contexts[name] = ctx
			if err := SaveFullConfig(full); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Logged out of context %q. Token cleared.\n", name)
			return nil
		},
	}
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the active context and authentication status",
		Run: func(cmd *cobra.Command, args []string) {
			full := LoadFullConfig()
			name := resolveActiveContextName(full)
			ctx := full.Contexts[name]
			fmt.Printf("Context: %s\n", name)
			fmt.Printf("API URL: %s\n", ctx.APIURL)
			if ctx.Token == "" {
				fmt.Println("Token:   (not set)")
			} else {
				masked := ctx.Token
				if len(masked) > 8 {
					masked = masked[:8] + strings.Repeat("*", len(masked)-8)
				}
				fmt.Printf("Token:   %s\n", masked)
			}
		},
	}
}
