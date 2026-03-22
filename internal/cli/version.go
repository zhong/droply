package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of droply",
		Run: func(cmd *cobra.Command, args []string) {
			if version == "" {
				version = "dev"
			}
			fmt.Println(version)
		},
	}
}
