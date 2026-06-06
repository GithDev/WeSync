//go:build windows

package sysinfo

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func osVersion() string {
	info := windows.RtlGetVersion()
	if info == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d build %d", info.MajorVersion, info.MinorVersion, info.BuildNumber)
}
