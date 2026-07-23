package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of ai-tracker",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("ai-tracker v1.0.0")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
