package cli

import "github.com/spf13/cobra"

// NewRootCmd creates and returns the root cobra command for the droply CLI.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "droply",
		Short: "droply — deploy static sites from the command line",
	}

	// Global --context flag overrides the active context for the current invocation.
	root.PersistentFlags().String("context", "", "Override the active context for this command")
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if ctxName, err := cmd.Flags().GetString("context"); err == nil {
			SetActiveContext(ctxName)
		}
	}

	root.AddCommand(newRegisterCmd())
	root.AddCommand(newLoginCmd())
	root.AddCommand(newLogoutCmd())
	root.AddCommand(newWhoamiCmd())
	root.AddCommand(newContextCmd())
	root.AddCommand(newSubdomainCmd())
	root.AddCommand(newDeployCmd())
	root.AddCommand(newDeploymentCmd())
	root.AddCommand(newProjectTokenCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newDomainCmd())
	root.AddCommand(newCertificateCmd())
	root.AddCommand(newAccessCmd())
	root.AddCommand(newProjectCmd())
	root.AddCommand(newStatsCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newVersionCmd(version))

	return root
}
