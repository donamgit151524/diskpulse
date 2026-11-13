package diff

import (
	"testing"
	"time"

	"diskpulse/internal/model"
)

const giB = int64(1024 * 1024 * 1024)

func TestCompareSnapshotsRootCauseAttribution(t *testing.T) {
	snapA := model.Snapshot{
		ID:             1,
		DriveRoot:      `C:\`,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		DriveUsedBytes: 100 * giB,
		ScannedBytes:   100 * giB,
	}
	snapB := model.Snapshot{
		ID:             2,
		DriveRoot:      `C:\`,
		CreatedAt:      time.Now(),
		DriveUsedBytes: 118 * giB,
		ScannedBytes:   118 * giB,
	}

	nodesA := []model.DirNode{
		{Path: `C:\`, Name: `C:\`, Depth: 0, SizeBytes: 100 * giB},
		{Path: `C:\Users`, ParentPath: `C:\`, Name: `Users`, Depth: 1, SizeBytes: 60 * giB},
		{Path: `C:\Users\dovan`, ParentPath: `C:\Users`, Name: `dovan`, Depth: 2, SizeBytes: 55 * giB},
		{Path: `C:\Users\dovan\Docker`, ParentPath: `C:\Users\dovan`, Name: `Docker`, Depth: 3, SizeBytes: 10 * giB},
		{Path: `C:\Users\dovan\Downloads`, ParentPath: `C:\Users\dovan`, Name: `Downloads`, Depth: 3, SizeBytes: 5 * giB},
		{Path: `C:\Users\dovan\OldCache`, ParentPath: `C:\Users\dovan`, Name: `OldCache`, Depth: 3, SizeBytes: 4 * giB},
	}

	// Docker grew +15 GiB, Downloads grew +5 GiB, OldCache deleted (-4 GiB),
	// plus +2 GiB of direct files added inside C:\Users\dovan itself.
	// Net change on dovan = +15 + 5 - 4 + 2 = +18 GiB.
	nodesB := []model.DirNode{
		{Path: `C:\`, Name: `C:\`, Depth: 0, SizeBytes: 118 * giB},
		{Path: `C:\Users`, ParentPath: `C:\`, Name: `Users`, Depth: 1, SizeBytes: 78 * giB},
		{Path: `C:\Users\dovan`, ParentPath: `C:\Users`, Name: `dovan`, Depth: 2, SizeBytes: 73 * giB},
		{Path: `C:\Users\dovan\Docker`, ParentPath: `C:\Users\dovan`, Name: `Docker`, Depth: 3, SizeBytes: 25 * giB},
		{Path: `C:\Users\dovan\Downloads`, ParentPath: `C:\Users\dovan`, Name: `Downloads`, Depth: 3, SizeBytes: 10 * giB},
	}

	res := CompareSnapshots(snapA, snapB, nodesA, nodesB, nil, nil)

	if res.DriveUsedDelta != 18*giB {
		t.Fatalf("DriveUsedDelta = %d, want %d", res.DriveUsedDelta, 18*giB)
	}

	// Expect RootCauses ranked: Docker (+15 GiB), Downloads (+5 GiB), dovan direct (+2 GiB).
	// Ancestors C:\ and C:\Users should have 0 attributed delta and must NOT appear in RootCauses.
	if len(res.RootCauses) != 3 {
		t.Fatalf("len(RootCauses) = %d, want 3: %+v", len(res.RootCauses), res.RootCauses)
	}
	if res.RootCauses[0].Path != `C:\Users\dovan\Docker` || res.RootCauses[0].AttributedDeltaBytes != 15*giB {
		t.Errorf("RootCauses[0] = %+v, want Docker +15GiB", res.RootCauses[0])
	}
	if res.RootCauses[1].Path != `C:\Users\dovan\Downloads` || res.RootCauses[1].AttributedDeltaBytes != 5*giB {
		t.Errorf("RootCauses[1] = %+v, want Downloads +5GiB", res.RootCauses[1])
	}
	if res.RootCauses[2].Path != `C:\Users\dovan` || res.RootCauses[2].AttributedDeltaBytes != 2*giB {
		t.Errorf("RootCauses[2] = %+v, want dovan +2GiB", res.RootCauses[2])
	}

	// FreedSpaces should contain OldCache (-4 GiB).
	if len(res.FreedSpaces) != 1 || res.FreedSpaces[0].Path != `C:\Users\dovan\OldCache` || res.FreedSpaces[0].AttributedDeltaBytes != -4*giB {
		t.Errorf("FreedSpaces = %+v, want OldCache -4GiB", res.FreedSpaces)
	}
}
