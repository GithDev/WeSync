//go:build !windows

package syncthing

import "os"

func configCandidates() []string {
	var dirs []string
	home, _ := os.UserHomeDir()
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dirs = append(dirs, xdg+"/syncthing/config.xml")
	}
	if home != "" {
		dirs = append(dirs, home+"/.config/syncthing/config.xml")
	}
	return dirs
}
