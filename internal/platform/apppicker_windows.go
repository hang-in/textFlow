//go:build windows

package platform

import (
	"path/filepath"
	"strings"
)

func defaultAppPickerOptions() AppPickerOptions {
	return AppPickerOptions{
		Title:                      "Choose an application",
		DefaultDirectory:           `C:\Program Files`,
		TreatPackagesAsDirectories: false,
	}
}

func normalizePickedAppPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return path
	}
	return ""
}
