//go:build windows

package syncthing

import "os"

func configCandidates() []string {
	var dirs []string
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, local+`\Syncthing\config.xml`)
	}
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		dirs = append(dirs, roaming+`\Syncthing\config.xml`)
	}
	return dirs
}
