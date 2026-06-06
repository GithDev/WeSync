//go:build !windows && !linux && !darwin

package sysinfo

func osVersion() string { return "" }
