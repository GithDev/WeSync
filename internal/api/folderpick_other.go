//go:build !windows && !darwin && !linux

package api

import "fmt"

func pickFolder() (string, error) {
	return "", fmt.Errorf("folder picker: not supported on this platform")
}
