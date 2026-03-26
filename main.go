package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version           = "dev"
	worktreeRoot      string
	worktreeStrategy  string
	worktreePattern   string
	worktreeSeparator string
)

func init() {
	loadWorktreeConfig()
	rootCmd.Long = buildRootCmdLong()
}

func main() {
	// Re-load config after cobra parses flags so --config is available
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFormat(); err != nil {
			return err
		}
		if configFlag != "" {
			loadWorktreeConfig()
			rootCmd.Long = buildRootCmdLong()
		}
		return nil
	}
	if err := rootCmd.Execute(); err != nil {
		if isJSONOutput() {
			_ = emitJSONError(rootCmd, err)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:           "wt",
	Short:         "Git worktree helper with organized directory structure",
	Long:          "",
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printCommandHelp(cmd)
	},
}

func printCommandHelp(cmd *cobra.Command) error {
	return cmd.Help()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configFlag, "config", "", "Path to config file (default: ~/.config/wt/config.toml)")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", formatText, "Output format: text or json")

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if !isJSONOutput() {
			defaultHelp(cmd, args)
			return
		}

		buf := bytes.NewBuffer(nil)
		origOut := cmd.OutOrStdout()
		origErr := cmd.ErrOrStderr()
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		defaultHelp(cmd, args)
		cmd.SetOut(origOut)
		cmd.SetErr(origErr)

		_ = emitJSONSuccess(cmd, map[string]any{"help": buf.String()})
	})

	rootCmd.AddCommand(checkoutCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(mrCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(pruneCmd)
	rootCmd.AddCommand(cleanupCmd)
	rootCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(shellenvCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(examplesCmd)
	rootCmd.AddCommand(defaultCmd)
	rootCmd.AddCommand(statusCmd)
	removeCmd.Flags().BoolVarP(&removeForce, "force", "f", false, "Force removal even if worktree has modifications")
	cleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Preview what would be removed without making changes")
	cleanupCmd.Flags().BoolVarP(&cleanupForce, "force", "f", false, "Remove all merged worktrees without confirmation")
	cleanupCmd.Flags().BoolVar(&cleanupStale, "stale", false, "Also detect worktrees with deleted remote branches or old commits")
	cleanupCmd.Flags().IntVar(&cleanupStaleDays, "stale-days", 30, "Threshold in days for considering a worktree inactive")
	migrateCmd.Flags().BoolVarP(&migrateForce, "force", "f", false, "Force migration when target path exists and is non-empty")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Preview changes without modifying files")
	initCmd.Flags().BoolVar(&initUninstall, "uninstall", false, "Remove wt configuration from shell")
	initCmd.Flags().BoolVar(&initNoPrompt, "no-prompt", false, "Skip activation instructions (for automated installs)")
	configInitCmd.Flags().BoolVar(&configInitForce, "force", false, "Overwrite existing config file")
}

func init() {
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)
}

func buildRootCmdLong() string {
	pattern, err := resolveWorktreePattern()
	if err != nil {
		pattern = worktreePattern
		if pattern == "" {
			pattern = "unknown"
		}
	}

	return fmt.Sprintf(`Git-like worktree management with organized directory structure.

Strategy: %s
Pattern:  %s
Root:     %s

Run 'wt info' to see available strategies and pattern variables.
Set WORKTREE_ROOT, WORKTREE_STRATEGY, and WORKTREE_PATTERN to customize.`,
		worktreeStrategy,
		pattern,
		worktreeRoot,
	)
}
