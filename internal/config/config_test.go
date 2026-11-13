package config

import (
	"path/filepath"
	"testing"
)

func TestLoadSaveAndNormalize(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg := DefaultConfig()
	cfg.Drives = []string{"c:", "d:\\", "C:\\"}
	cfg.ScanIntervalMinutes = 1
	cfg.ScannerWorkers = 99

	if err := SaveTo(cfgPath, cfg); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	loaded, err := LoadFrom(cfgPath)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if loaded.ScanIntervalMinutes != 5 {
		t.Errorf("ScanIntervalMinutes = %d, want clamped 5", loaded.ScanIntervalMinutes)
	}
	if loaded.ScannerWorkers != 8 {
		t.Errorf("ScannerWorkers = %d, want clamped 8", loaded.ScannerWorkers)
	}
	if len(loaded.Drives) != 2 || loaded.Drives[0] != `C:\` || loaded.Drives[1] != `D:\` {
		t.Errorf("Drives = %+v, want [C:\\ D:\\]", loaded.Drives)
	}
}
