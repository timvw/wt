package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var defaultCmd = &cobra.Command{
	Use:   "default",
	Short: "Navigate to the main worktree",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := getRepoInfo()
		if err != nil {
			return err
		}

		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{
				"path":        info.Main,
				"navigate_to": info.Main,
			})
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Navigating to main worktree: %s\n", info.Main)
		printCDMarker(info.Main)
		return nil
	},
}
