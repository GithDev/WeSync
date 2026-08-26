//go:build !windows

package main

import "os/exec"

// configureBackendCmd is a no-op outside Windows: there is no console window to
// suppress.
func configureBackendCmd(_ *exec.Cmd) {}
