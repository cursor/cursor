//go:build !windows

package copilot

import "os/exec"

// configureProcAttr configures platform-specific process attributes.
// On non-Windows platforms, this is a no-op.
func configureProcAttr(cmd *exec.Cmd) {
	// No special configuration needed on non-Windows platforms
}
