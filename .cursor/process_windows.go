//go:build windows

package copilot

import (
	"os/exec"
	"syscall"
)

// configureProcAttr configures platform-specific process attributes.
// On Windows, this hides the console window to avoid distracting users in GUI apps.
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
}
