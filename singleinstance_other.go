//go:build !windows

package main

func acquireSingleInstanceLock(_ int) (bool, func()) {
	return true, func() {}
}
