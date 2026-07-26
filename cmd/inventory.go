package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spencer-life/ai-tracker/internal/inventory"
	"github.com/spf13/cobra"
)

var inventoryJSON bool

var inventoryCmd = &cobra.Command{Use: "inventory", Short: "Inventory privacy-safe agent customizations", RunE: func(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	items, err := inventory.ScanWithRoots(cmd.Context(), home, cwd, inventory.Roots{
		CodexHome:  os.Getenv("CODEX_HOME"),
		ClaudeHome: os.Getenv("CLAUDE_CONFIG_DIR"),
	})
	if err != nil {
		return err
	}
	if items == nil {
		items = []inventory.Component{}
	}
	if inventoryJSON {
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROVIDER\tKIND\tNAME\tSCOPE\tSTATE\tSOURCE")
	for _, item := range items {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s:%s\n", item.Provider, item.Kind, item.DisplayName, item.Scope, item.State, item.Source.Base, item.Source.Hash[:12])
	}
	return w.Flush()
}}

func init() {
	inventoryCmd.Flags().BoolVar(&inventoryJSON, "json", false, "emit JSON")
	rootCmd.AddCommand(inventoryCmd)
}
