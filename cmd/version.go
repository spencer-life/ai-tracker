package cmd

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Version, Commit, and Date are populated by GoReleaser. They remain useful
// defaults for local builds and `go install` builds without linker flags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of ai-tracker",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "ai-tracker %s\n", displayVersion())
		return err
	},
}

func init() {
	rootCmd.Version = displayVersion()
	rootCmd.SetVersionTemplate("ai-tracker {{.Version}}\n")
	rootCmd.AddCommand(versionCmd)
}

func displayVersion() string {
	version := strings.TrimSpace(Version)
	if version == "" || version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	if version == "" || version == "(devel)" {
		version = "dev"
	}
	if version != "dev" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return version
}
