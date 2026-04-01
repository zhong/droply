package cli

import "github.com/spf13/cobra"

// NewRootCmd creates and returns the root cobra command for the droply CLI.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "droply",
		Short: "droply — deploy static sites from the command line",
	}

	root.AddCommand(newRegisterCmd())
	root.AddCommand(newLoginCmd())
	root.AddCommand(newLogoutCmd())
	root.AddCommand(newWhoamiCmd())
	root.AddCommand(newSubdomainCmd())
	root.AddCommand(newDeployCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newDomainCmd())
	root.AddCommand(newAccessCmd())
	root.AddCommand(newProjectCmd())
	root.AddCommand(newStatsCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newVersionCmd(version))

	return root
}
