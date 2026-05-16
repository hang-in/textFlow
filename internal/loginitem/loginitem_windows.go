//go:build windows

package loginitem

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runEntryName = "DKST Text Flow"
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func Enabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	value, _, err := k.GetStringValue(runEntryName)
	if err != nil {
		return false
	}
	return strings.TrimSpace(value) != ""
}

func SetEnabled(enabled bool) error {
	if !enabled {
		k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.WRITE)
		if err != nil {
			if errors.Is(err, registry.ErrNotExist) {
				return nil
			}
			return err
		}
		defer k.Close()
		if err := k.DeleteValue(runEntryName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(runEntryName, `"`+exe+`"`)
}
