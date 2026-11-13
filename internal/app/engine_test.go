package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"diskpulse/internal/config"
	"diskpulse/internal/notify"
	"diskpulse/internal/store"
)

func TestEngineTriggerScanEndToEnd(t *testing.T) {
	scanDir := t.TempDir()
	targetFile := filepath.Join(scanDir, "Downloads", "big.bin")
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, make([]byte, 20*1024), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "engine.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	cfg := config.DefaultConfig()
	cfg.MaxDepth = 3
	cfg.MinPersistBytes = 4 * 1024
	cfg.LargeFileThresholdBytes = 10 * 1024

	eng := New(cfg, st, notify.NewDispatcher(nil))
	if started := eng.TriggerScan(context.Background(), scanDir); !started {
		t.Fatalf("expected TriggerScan to start")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !eng.Progress().Active {
			snaps, err := st.ListSnapshots(scanDir, 5)
			if err == nil && len(snaps) == 1 {
				if snaps[0].ScannedBytes != 20*1024 {
					t.Fatalf("ScannedBytes = %d, want %d", snaps[0].ScannedBytes, 20*1024)
				}
				files, err := st.GetSnapshotLargeFiles(snaps[0].ID)
				if err != nil || len(files) != 1 || files[0].Path != targetFile {
					t.Fatalf("unexpected large files: %+v (err=%v)", files, err)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for async scan snapshot")
}
