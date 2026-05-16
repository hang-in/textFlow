package platform

type AppPickerOptions struct {
	Title                      string
	DefaultDirectory           string
	TreatPackagesAsDirectories bool
}

func DefaultAppPickerOptions() AppPickerOptions {
	return defaultAppPickerOptions()
}

func NormalizePickedAppPath(path string) string {
	return normalizePickedAppPath(path)
}
