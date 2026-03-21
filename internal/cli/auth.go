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

func newRegisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "register",
		Short: "Create a new droply account",
		RunE: func(cmd *cobra.Command, args []string) error {
			email := readInput("Email: ")
			password := readPassword("Password: ")

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			var resp struct {
				APIToken string `json:"api_token"`
			}
			if err := client.doJSON("POST", "/auth/register", map[string]string{
				"email":    email,
				"password": password,
			}, &resp); err != nil {
				return err
			}

			cfg.Token = resp.APIToken
			if err := SaveConfig(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Println("Registered successfully. Token saved.")
			return nil
		},
	}
}

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Log in to your droply account",
		RunE: func(cmd *cobra.Command, args []string) error {
			email := readInput("Email: ")
			password := readPassword("Password: ")

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			var resp struct {
				APIToken string `json:"api_token"`
			}
			if err := client.doJSON("POST", "/auth/login", map[string]string{
				"email":    email,
				"password": password,
			}, &resp); err != nil {
				return err
			}

			cfg.Token = resp.APIToken
			if err := SaveConfig(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Println("Logged in successfully. Token saved.")
			return nil
		},
	}
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear your stored authentication token",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := LoadConfig()
			cfg.Token = ""
			if err := SaveConfig(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Println("Logged out. Token cleared.")
			return nil
		},
	}
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the current API URL and authentication status",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := LoadConfig()
			fmt.Printf("API URL: %s\n", cfg.APIURL)
			if cfg.Token == "" {
				fmt.Println("Token:   (not set)")
			} else {
				masked := cfg.Token
				if len(masked) > 8 {
					masked = masked[:8] + strings.Repeat("*", len(masked)-8)
				}
				fmt.Printf("Token:   %s\n", masked)
			}
		},
	}
}
