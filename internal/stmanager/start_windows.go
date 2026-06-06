//go:build windows

package stmanager

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW process-creation flag — the
// child runs without allocating a console.
const createNoWindow = 0x08000000

// configureCmd sets Windows-specific flags so Syncthing starts as a
// background process with no visible console window.
func configureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
