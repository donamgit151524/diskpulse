//go:build windows

package config

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const runRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetAutoStart enables or disables starting DiskPulse when the current Windows user signs in.
func SetAutoStart(enabled bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if !enabled {
		err := key.DeleteValue(AppName)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(`"%s" --background`, exePath)
	return key.SetStringValue(AppName, cmd)
}

// IsAutoStartEnabled reports whether the Windows Run registry entry is currently present.
func IsAutoStartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	val, _, err := key.GetStringValue(AppName)
	return err == nil && val != ""
}
