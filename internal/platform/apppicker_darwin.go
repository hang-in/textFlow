//go:build darwin

package platform

import (
	"path/filepath"
	"strings"
)

func defaultAppPickerOptions() AppPickerOptions {
	return AppPickerOptions{
		Title:                      "Choose an application",
		DefaultDirectory:           "/Applications",
		TreatPackagesAsDirectories: false,
	}
}

func normalizePickedAppPath(path string) string {
	path = strings.TrimSpace(path)
	for path != "" && path != "." && path != string(filepath.Separator) {
		if strings.EqualFold(filepath.Ext(path), ".app") {
			return path
		}
		next := filepath.Dir(path)
		if next == path {
			break
		}
		path = next
	}
	return ""
}
