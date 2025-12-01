//go:build windows

package scenarios

import "github.com/timvw/wt/e2e/harness"

// createShellAdapter creates a platform-specific shell adapter
func createShellAdapter(name string) harness.ShellAdapter {
	switch name {
	case "bash":
		return harness.NewBashAdapter()
	case "zsh":
		return harness.NewZshAdapter()
	case "pwsh":
		return harness.NewPwshAdapter()
	default:
		return nil
	}
}
