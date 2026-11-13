package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diskpulse/internal/config"
)

func TestListFixedDrives(t *testing.T) {
	drives, err := ListFixedDrives()
	if err != nil {
		t.Fatalf("ListFixedDrives failed: %v", err)
	}
	if len(drives) == 0 {
		t.Fatalf("expected at least one fixed drive on Windows")
	}
	for _, d := range drives {
		if !strings.HasSuffix(d.Root, `:\`) || strings.HasSuffix(d.Root, `:\\`) {
			t.Errorf("unexpected drive root formatting: %q", d.Root)
		}
		if d.TotalBytes <= 0 {
			t.Errorf("drive %s TotalBytes = %d, want > 0", d.Root, d.TotalBytes)
		}
	}
}

func TestScanDrivePrunesTreeAndCollectsLargeFiles(t *testing.T) {
	root := t.TempDir()

	// Create structure:
	// root/
	//   bigdir/ (depth 1) -> sub1/ (depth 2, 12 KB) + sub2/ (depth 2, 1 KB)
	//   deep/a/b/c/bigleaf (depth 5, 10 KB) -> kept even when MaxDepth=2 because >= MinPersistBytes
	//   deep/a/b/c/tinyleaf (depth 5, 100 B) -> pruned into parent's OtherBytes
	//   ignored_dir/ (50 KB) -> excluded
	writeSizedFile(t, filepath.Join(root, "bigdir", "sub1", "file1.bin"), 12*1024)
	writeSizedFile(t, filepath.Join(root, "bigdir", "sub2", "file2.bin"), 1024)
	writeSizedFile(t, filepath.Join(root, "deep", "a", "b", "c", "bigleaf", "huge.iso"), 10*1024)
	writeSizedFile(t, filepath.Join(root, "deep", "a", "b", "c", "tinyleaf", "small.txt"), 100)
	writeSizedFile(t, filepath.Join(root, "ignored_dir", "skip.bin"), 50*1024)

	cfg := config.DefaultConfig()
	cfg.MaxDepth = 2
	cfg.MinPersistBytes = 8 * 1024
	cfg.LargeFileThresholdBytes = 9 * 1024
	cfg.TopLargeFiles = 5
	cfg.ScannerWorkers = 2
	cfg.IgnorePaths = []string{"ignored_dir"}

	out, err := ScanDrive(context.Background(), root, cfg, nil)
	if err != nil {
		t.Fatalf("ScanDrive failed: %v", err)
	}

	wantTotal := int64(12*1024 + 1024 + 10*1024 + 100)
	if out.Snapshot.ScannedBytes != wantTotal {
		t.Fatalf("ScannedBytes = %d, want %d", out.Snapshot.ScannedBytes, wantTotal)
	}

	byPath := map[string]int64{}
	for _, n := range out.Nodes {
		byPath[n.Path] = n.SizeBytes
	}

	bigLeafPath := filepath.Join(root, "deep", "a", "b", "c", "bigleaf")
	if byPath[bigLeafPath] != 10*1024 {
		t.Errorf("expected bigleaf (depth 5, >=8KB) to be persisted, got %d", byPath[bigLeafPath])
	}

	tinyLeafPath := filepath.Join(root, "deep", "a", "b", "c", "tinyleaf")
	if _, exists := byPath[tinyLeafPath]; exists {
		t.Errorf("expected tinyleaf (depth 5, 100B) to be pruned")
	}

	ignoredPath := filepath.Join(root, "ignored_dir")
	if _, exists := byPath[ignoredPath]; exists {
		t.Errorf("expected ignored_dir to be excluded")
	}

	if len(out.LargeFiles) != 2 {
		t.Fatalf("expected 2 large files >= 9KB, got %d: %+v", len(out.LargeFiles), out.LargeFiles)
	}
	if out.LargeFiles[0].SizeBytes != 12*1024 || out.LargeFiles[1].SizeBytes != 10*1024 {
		t.Errorf("unexpected large files order/sizes: %+v", out.LargeFiles)
	}
}

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
