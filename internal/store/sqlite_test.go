package store

import (
	"path/filepath"
	"testing"
	"time"

	"diskpulse/internal/model"
	"diskpulse/internal/scanner"
)

func TestStoreSaveQueryAndRetention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	now := time.Now()
	// Insert 3 snapshots on the same day 25 days ago (should prune down to 1) + 1 fresh snapshot today.
	oldBase := now.Add(-25 * 24 * time.Hour).Truncate(24 * time.Hour).Add(8 * time.Hour)
	for i := 0; i < 3; i++ {
		_, err := st.SaveScanOutput(scanner.ScanOutput{
			Snapshot: model.Snapshot{
				DriveRoot:       `C:\`,
				CreatedAt:       oldBase.Add(time.Duration(i) * time.Hour),
				DriveTotalBytes: 1000,
				DriveFreeBytes:  500,
				DriveUsedBytes:  500,
				ScannedBytes:    400,
				Status:          "completed",
			},
			Nodes: []model.DirNode{
				{Path: `C:\`, Name: `C:\`, Depth: 0, SizeBytes: 400},
			},
		})
		if err != nil {
			t.Fatalf("SaveScanOutput old #%d: %v", i, err)
		}
	}

	idFresh, err := st.SaveScanOutput(scanner.ScanOutput{
		Snapshot: model.Snapshot{
			DriveRoot:       `C:\`,
			CreatedAt:       now,
			DriveTotalBytes: 1000,
			DriveFreeBytes:  450,
			DriveUsedBytes:  550,
			ScannedBytes:    450,
			Status:          "completed",
		},
		Nodes: []model.DirNode{
			{Path: `C:\`, Name: `C:\`, Depth: 0, SizeBytes: 450},
			{Path: `C:\Users`, ParentPath: `C:\`, Name: `Users`, Depth: 1, SizeBytes: 200},
		},
		LargeFiles: []model.LargeFile{
			{Path: `C:\Users\big.vhdx`, SizeBytes: 150, ModifiedAt: now, Tag: "docker"},
		},
	})
	if err != nil {
		t.Fatalf("SaveScanOutput fresh: %v", err)
	}

	deleted, err := st.ApplyRetention(`C:\`, now)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	snaps, err := st.ListSnapshots(`C:\`, 20)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 || snaps[0].ID != idFresh {
		t.Errorf("unexpected snapshots after retention: %+v", snaps)
	}

	children, err := st.GetSnapshotChildren(idFresh, `C:\`)
	if err != nil {
		t.Fatalf("GetSnapshotChildren: %v", err)
	}
	if len(children) != 1 || children[0].Path != `C:\Users` {
		t.Errorf("unexpected children: %+v", children)
	}

	files, err := st.GetSnapshotLargeFiles(idFresh)
	if err != nil {
		t.Fatalf("GetSnapshotLargeFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != `C:\Users\big.vhdx` {
		t.Errorf("unexpected large files: %+v", files)
	}
}
