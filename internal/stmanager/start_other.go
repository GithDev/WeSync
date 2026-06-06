//go:build !windows

package stmanager

import "os/exec"

func configureCmd(cmd *exec.Cmd) {}
