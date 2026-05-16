//go:build !darwin && !windows

package platform

import "strings"

func defaultAppPickerOptions() AppPickerOptions {
	return AppPickerOptions{
		Title: "Choose an application",
	}
}

func normalizePickedAppPath(path string) string {
	return strings.TrimSpace(path)
}
