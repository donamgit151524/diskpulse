package alert

import (
	"testing"
	"time"

	"diskpulse/internal/config"
	"diskpulse/internal/model"
)

type memReader struct {
	snaps  []model.Snapshot
	nodes  map[int64][]model.DirNode
	recent map[string]bool
}

func (m *memReader) ListSnapshots(driveRoot string, limit int) ([]model.Snapshot, error) {
	if len(m.snaps) > limit {
		return m.snaps[:limit], nil
	}
	return m.snaps, nil
}

func (m *memReader) GetSnapshotNodes(snapshotID int64) ([]model.DirNode, error) {
	return m.nodes[snapshotID], nil
}

func (m *memReader) GetSnapshotLargeFiles(snapshotID int64) ([]model.LargeFile, error) {
	return nil, nil
}

func (m *memReader) HasRecentAlert(driveRoot, ruleType, targetPath string, since time.Time) (bool, error) {
	return m.recent[ruleType+"|"+targetPath], nil
}

const giB = int64(1024 * 1024 * 1024)

func TestDetectorLowSpaceSpikeCreepingAndForecast(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig().Alerts
	cfg.SpikeBytes = 2 * giB
	cfg.CreepingMinRuns = 4
	cfg.CreepingMinBytes = 2 * giB
	cfg.ForecastDaysWarn = 20

	// 4 snapshots spaced 1 day apart:
	// - Drive total 186 GiB, free drops from 26 -> 25 -> 24 -> 15 GiB (triggers low_space < 15% and < 20 GiB).
	// - C:\Users\Docker spikes +8 GiB on day 4 (triggers sudden_spike).
	// - C:\Users\Temp creeps +1 GiB every single day across all 4 days (+3 GiB total, triggers creeping_growth).
	snaps := []model.Snapshot{
		{ID: 4, DriveRoot: `C:\`, CreatedAt: now, DriveTotalBytes: 186 * giB, DriveFreeBytes: 15 * giB, DriveUsedBytes: 171 * giB},
		{ID: 3, DriveRoot: `C:\`, CreatedAt: now.Add(-24 * time.Hour), DriveTotalBytes: 186 * giB, DriveFreeBytes: 24 * giB, DriveUsedBytes: 162 * giB},
		{ID: 2, DriveRoot: `C:\`, CreatedAt: now.Add(-48 * time.Hour), DriveTotalBytes: 186 * giB, DriveFreeBytes: 25 * giB, DriveUsedBytes: 161 * giB},
		{ID: 1, DriveRoot: `C:\`, CreatedAt: now.Add(-72 * time.Hour), DriveTotalBytes: 186 * giB, DriveFreeBytes: 26 * giB, DriveUsedBytes: 160 * giB},
	}

	nodes := map[int64][]model.DirNode{
		1: {
			{Path: `C:\`, Depth: 0, SizeBytes: 160 * giB},
			{Path: `C:\Users`, ParentPath: `C:\`, Depth: 1, SizeBytes: 40 * giB},
			{Path: `C:\Users\Docker`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 10 * giB},
			{Path: `C:\Users\Temp`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 1 * giB},
		},
		2: {
			{Path: `C:\`, Depth: 0, SizeBytes: 161 * giB},
			{Path: `C:\Users`, ParentPath: `C:\`, Depth: 1, SizeBytes: 41 * giB},
			{Path: `C:\Users\Docker`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 10 * giB},
			{Path: `C:\Users\Temp`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 2 * giB},
		},
		3: {
			{Path: `C:\`, Depth: 0, SizeBytes: 162 * giB},
			{Path: `C:\Users`, ParentPath: `C:\`, Depth: 1, SizeBytes: 42 * giB},
			{Path: `C:\Users\Docker`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 10 * giB},
			{Path: `C:\Users\Temp`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 3 * giB},
		},
		4: {
			{Path: `C:\`, Depth: 0, SizeBytes: 171 * giB},
			{Path: `C:\Users`, ParentPath: `C:\`, Depth: 1, SizeBytes: 51 * giB},
			{Path: `C:\Users\Docker`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 18 * giB},
			{Path: `C:\Users\Temp`, ParentPath: `C:\Users`, Depth: 2, SizeBytes: 4 * giB},
		},
	}

	det := NewDetector(&memReader{snaps: snaps, nodes: nodes, recent: map[string]bool{}})
	alerts, err := det.EvaluateDrive(`C:\`, cfg, now)
	if err != nil {
		t.Fatalf("EvaluateDrive failed: %v", err)
	}

	gotRules := map[string]string{}
	for _, a := range alerts {
		gotRules[a.RuleType] = a.TargetPath
	}

	if gotRules["low_space"] != `C:\` {
		t.Errorf("expected low_space alert, got %+v", alerts)
	}
	if gotRules["sudden_spike"] != `C:\Users\Docker` {
		t.Errorf("expected sudden_spike on Docker, got %+v", alerts)
	}
	if gotRules["creeping_growth"] != `C:\Users\Temp` {
		t.Errorf("expected creeping_growth on Temp only (not parent Users), got %+v", alerts)
	}
	if gotRules["predicted_full"] != `C:\` {
		t.Errorf("expected predicted_full alert, got %+v", alerts)
	}

	// Verify cooldown suppresses duplicate alerts.
	detCooldown := NewDetector(&memReader{
		snaps: snaps,
		nodes: nodes,
		recent: map[string]bool{
			`low_space|C:\`:                    true,
			`sudden_spike|C:\Users\Docker`:     true,
			`creeping_growth|C:\Users\Temp`:    true,
			`predicted_full|C:\`:               true,
		},
	})
	suppressed, err := detCooldown.EvaluateDrive(`C:\`, cfg, now)
	if err != nil {
		t.Fatalf("EvaluateDrive cooldown failed: %v", err)
	}
	if len(suppressed) != 0 {
		t.Errorf("expected 0 alerts due to cooldown, got %+v", suppressed)
	}
}
