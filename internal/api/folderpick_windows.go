//go:build windows

package api

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
)

func pickFolder() (string, error) {
	// Encode the selected path as base64 so that console encoding (UTF-8 BOM,
	// OEM codepage, etc.) cannot corrupt non-ASCII characters like ÅÄÖ.
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", `
		Add-Type -AssemblyName System.Windows.Forms | Out-Null
		$cursor = [System.Windows.Forms.Cursor]::Position
		$screen = [System.Windows.Forms.Screen]::FromPoint($cursor)
		$wa = $screen.WorkingArea
		$owner = New-Object System.Windows.Forms.Form
		$owner.TopMost = $true
		$owner.ShowInTaskbar = $false
		$owner.StartPosition = 'Manual'
		$owner.Size = New-Object System.Drawing.Size(1, 1)
		$owner.Location = New-Object System.Drawing.Point(
			[int]($wa.X + $wa.Width / 2),
			[int]($wa.Y + $wa.Height / 2)
		)
		$owner.Show()
		$owner.BringToFront()
		$owner.Activate()
		[System.Windows.Forms.Application]::DoEvents()
		$d = New-Object System.Windows.Forms.FolderBrowserDialog
		$d.Description = "Select folder to share via WeSync"
		$d.ShowNewFolderButton = $true
		$result = $d.ShowDialog($owner)
		$owner.Dispose()
		if ($result -eq 'OK') {
			[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($d.SelectedPath))
		}
	`)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("folder picker: %w", err)
	}
	b64 := strings.TrimSpace(string(out))
	if b64 == "" {
		// User cancelled the dialog (script didn't emit a path). Return empty
		// path with no error so the frontend doesn't fall back to the manual
		// path input modal — cancel should be cancel, not "picker unavailable".
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("folder picker: decode path: %w", err)
	}
	return string(decoded), nil
}
