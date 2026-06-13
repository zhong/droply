package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage droply server contexts (connection profiles)",
	}
	cmd.AddCommand(newContextListCmd())
	cmd.AddCommand(newContextShowCmd())
	cmd.AddCommand(newContextUseCmd())
	cmd.AddCommand(newContextAddCmd())
	cmd.AddCommand(newContextRemoveCmd())
	return cmd
}

func newContextListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all contexts",
		Run: func(cmd *cobra.Command, args []string) {
			full := LoadFullConfig()
			active := resolveActiveContextName(full)

			if len(full.Contexts) == 0 {
				fmt.Println("No contexts configured.")
				return
			}

			names := make([]string, 0, len(full.Contexts))
			for name := range full.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CONTEXT\tAPI URL\tAUTHED")
			for _, name := range names {
				ctx := full.Contexts[name]
				marker := " "
				if name == active {
					marker = "*"
				}
				authed := "no"
				if ctx.Token != "" {
					authed = "yes"
				}
				fmt.Fprintf(w, "%s %s\t%s\t%s\n", marker, name, ctx.APIURL, authed)
			}
			w.Flush()
		},
	}
}

func newContextShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [NAME]",
		Short: "Show details of a context (default: active context)",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			full := LoadFullConfig()
			name := resolveActiveContextName(full)
			if len(args) > 0 {
				name = args[0]
			}
			ctx, ok := full.Contexts[name]
			if !ok {
				fmt.Printf("Context %q not found.\n", name)
				os.Exit(1)
			}
			fmt.Printf("Context: %s\n", name)
			fmt.Printf("API URL: %s\n", ctx.APIURL)
			if ctx.Token == "" {
				fmt.Println("Token:   (not set)")
			} else {
				masked := ctx.Token
				if len(masked) > 8 {
					masked = masked[:8] + "..."
				}
				fmt.Printf("Token:   %s\n", masked)
			}
			if name == full.CurrentContext {
				fmt.Println("(current context)")
			}
		},
	}
}

func newContextUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Switch to a different context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			full := LoadFullConfig()
			if _, ok := full.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found; use 'droply context add' to create it", name)
			}
			full.CurrentContext = name
			if err := SaveFullConfig(full); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Switched to context %q.\n", name)
			return nil
		},
	}
}

func newContextAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add NAME --api-url URL",
		Short: "Create or update a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			apiURL, _ := cmd.Flags().GetString("api-url")
			if apiURL == "" {
				return fmt.Errorf("--api-url is required")
			}

			full := LoadFullConfig()
			ctx := full.Contexts[name]
			ctx.APIURL = apiURL
			full.Contexts[name] = ctx

			// If this is the only context, make it current.
			if full.CurrentContext == "" || len(full.Contexts) == 1 {
				full.CurrentContext = name
			}

			if err := SaveFullConfig(full); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Context %q added (%s).\n", name, apiURL)
			fmt.Println("Run 'droply auth login --context " + name + "' to authenticate.")
			return nil
		},
	}
	cmd.Flags().String("api-url", "", "API URL of the droply server (required)")
	cmd.MarkFlagRequired("api-url")
	return cmd
}

func newContextRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm", "delete"},
		Short:   "Delete a context",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			full := LoadFullConfig()
			if _, ok := full.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found", name)
			}
			delete(full.Contexts, name)
			if full.CurrentContext == name {
				// Switch to another context if available.
				for otherName := range full.Contexts {
					full.CurrentContext = otherName
					break
				}
				if full.CurrentContext == name {
					full.CurrentContext = "" // was the last one
				}
			}
			if err := SaveFullConfig(full); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Context %q removed.\n", name)
			return nil
		},
	}
}
