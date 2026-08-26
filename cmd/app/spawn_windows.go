//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// wesync.exe is linked with -H windowsgui so it normally allocates no console,
// but a dev/CI build without that flag would flash one on every launch.
func configureBackendCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindowFlag,
	}
}
