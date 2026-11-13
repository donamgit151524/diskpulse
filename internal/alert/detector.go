package alert

import (
	"fmt"
	"math"
	"strings"
	"time"

	"diskpulse/internal/config"
	"diskpulse/internal/diff"
	"diskpulse/internal/model"
	"diskpulse/internal/tagging"
)

// Reader provides historical snapshot and alert lookup for rule evaluation.
type Reader interface {
	ListSnapshots(driveRoot string, limit int) ([]model.Snapshot, error)
	GetSnapshotNodes(snapshotID int64) ([]model.DirNode, error)
	GetSnapshotLargeFiles(snapshotID int64) ([]model.LargeFile, error)
	HasRecentAlert(driveRoot, ruleType, targetPath string, since time.Time) (bool, error)
}

// Detector evaluates disk health and growth anomalies against recent snapshots.
type Detector struct {
	reader Reader
}

// NewDetector creates a Detector backed by Reader.
func NewDetector(reader Reader) *Detector {
	return &Detector{reader: reader}
}

// EvaluateDrive runs all alert rules after a completed snapshot for driveRoot.
func (d *Detector) EvaluateDrive(driveRoot string, cfg config.AlertConfig, now time.Time) ([]model.Alert, error) {
	snaps, err := d.reader.ListSnapshots(driveRoot, 30)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return nil, nil
	}

	latest := snaps[0]
	cooldownSince := now.Add(-time.Duration(cfg.CooldownHours) * time.Hour)
	var alerts []model.Alert

	if a, ok := d.checkLowSpace(latest, cfg, cooldownSince, now); ok {
		alerts = append(alerts, a)
	}

	if len(snaps) >= 2 {
		spikeAlerts, err := d.checkSuddenSpike(snaps, cfg, cooldownSince, now)
		if err == nil {
			alerts = append(alerts, spikeAlerts...)
		}
	}

	if len(snaps) >= cfg.CreepingMinRuns {
		creepAlerts, err := d.checkCreepingGrowth(snaps[:cfg.CreepingMinRuns], cfg, cooldownSince, now)
		if err == nil {
			alerts = append(alerts, creepAlerts...)
		}
	}

	forecast := ComputeForecast(driveRoot, snaps, 30)
	if forecast.HasEnoughData && forecast.DailyGrowthBytes > 0 && forecast.DaysUntilFull <= float64(cfg.ForecastDaysWarn) {
		recent, err := d.reader.HasRecentAlert(driveRoot, "predicted_full", driveRoot, cooldownSince)
		if err == nil && !recent {
			severity := "warning"
			if forecast.DaysUntilFull <= 5 {
				severity = "critical"
			}
			alerts = append(alerts, model.Alert{
				CreatedAt:  now,
				DriveRoot:  driveRoot,
				RuleType:   "predicted_full",
				Severity:   severity,
				TargetPath: driveRoot,
				Tag:        tagging.TagOther,
				DeltaBytes: forecast.DailyGrowthBytes,
				Message: fmt.Sprintf(
					"Ổ %s đang tăng trung bình %s/ngày — dự kiến đầy sau khoảng %.0f ngày",
					strings.TrimSuffix(driveRoot, `\`),
					FormatBytes(forecast.DailyGrowthBytes),
					math.Max(1, math.Ceil(forecast.DaysUntilFull)),
				),
			})
		}
	}

	return alerts, nil
}

func (d *Detector) checkLowSpace(latest model.Snapshot, cfg config.AlertConfig, cooldownSince, now time.Time) (model.Alert, bool) {
	if latest.DriveTotalBytes <= 0 {
		return model.Alert{}, false
	}
	freePct := float64(latest.DriveFreeBytes) / float64(latest.DriveTotalBytes) * 100.0
	freeGB := float64(latest.DriveFreeBytes) / float64(1024*1024*1024)

	var severity string
	if freePct < cfg.CriticalSpacePercent || freeGB < cfg.CriticalSpaceGB {
		severity = "critical"
	} else if freePct < cfg.LowSpacePercent || freeGB < cfg.LowSpaceGB {
		severity = "warning"
	} else {
		return model.Alert{}, false
	}

	recent, err := d.reader.HasRecentAlert(latest.DriveRoot, "low_space", latest.DriveRoot, cooldownSince)
	if err != nil || recent {
		return model.Alert{}, false
	}

	return model.Alert{
		CreatedAt:  now,
		DriveRoot:  latest.DriveRoot,
		RuleType:   "low_space",
		Severity:   severity,
		TargetPath: latest.DriveRoot,
		Tag:        tagging.TagOther,
		DeltaBytes: latest.DriveFreeBytes,
		Message: fmt.Sprintf(
			"Ổ %s sắp đầy: chỉ còn %s trống (%.1f%%)",
			strings.TrimSuffix(latest.DriveRoot, `\`),
			FormatBytes(latest.DriveFreeBytes),
			freePct,
		),
	}, true
}

func (d *Detector) checkSuddenSpike(snaps []model.Snapshot, cfg config.AlertConfig, cooldownSince, now time.Time) ([]model.Alert, error) {
	latest := snaps[0]
	baseline := pickBaseline24h(snaps)

	nodesA, err := d.reader.GetSnapshotNodes(baseline.ID)
	if err != nil {
		return nil, err
	}
	nodesB, err := d.reader.GetSnapshotNodes(latest.ID)
	if err != nil {
		return nil, err
	}
	filesA, _ := d.reader.GetSnapshotLargeFiles(baseline.ID)
	filesB, _ := d.reader.GetSnapshotLargeFiles(latest.ID)

	res := diff.CompareSnapshots(baseline, latest, nodesA, nodesB, filesA, filesB)
	var alerts []model.Alert
	for _, rc := range res.RootCauses {
		if rc.AttributedDeltaBytes < cfg.SpikeBytes {
			break
		}
		recent, err := d.reader.HasRecentAlert(latest.DriveRoot, "sudden_spike", rc.Path, cooldownSince)
		if err != nil || recent {
			continue
		}
		severity := "warning"
		if rc.AttributedDeltaBytes >= cfg.SpikeBytes*3 {
			severity = "critical"
		}
		label := tagging.HumanLabel(rc.Tag)
		alerts = append(alerts, model.Alert{
			CreatedAt:  now,
			DriveRoot:  latest.DriveRoot,
			RuleType:   "sudden_spike",
			Severity:   severity,
			TargetPath: rc.Path,
			Tag:        rc.Tag,
			DeltaBytes: rc.AttributedDeltaBytes,
			Message: fmt.Sprintf(
				"Tăng đột biến +%s tại %s (%s)",
				FormatBytes(rc.AttributedDeltaBytes),
				rc.Path,
				label,
			),
		})
		if len(alerts) >= 5 {
			break
		}
	}
	return alerts, nil
}

// pickBaseline24h selects the oldest snapshot within the last 24h (falling back to immediate predecessor).
func pickBaseline24h(snaps []model.Snapshot) model.Snapshot {
	latest := snaps[0]
	cutoff := latest.CreatedAt.Add(-24 * time.Hour)
	candidate := snaps[1]
	for i := 1; i < len(snaps); i++ {
		if !snaps[i].CreatedAt.Before(cutoff) {
			candidate = snaps[i]
		} else {
			break
		}
	}
	return candidate
}

func (d *Detector) checkCreepingGrowth(recentNewestFirst []model.Snapshot, cfg config.AlertConfig, cooldownSince, now time.Time) ([]model.Alert, error) {
	n := len(recentNewestFirst)
	// Reverse to chronological order [oldest ... newest].
	chrono := make([]model.Snapshot, n)
	for i := 0; i < n; i++ {
		chrono[i] = recentNewestFirst[n-1-i]
	}

	nodeMaps := make([]map[string]model.DirNode, n)
	for i, s := range chrono {
		nodes, err := d.reader.GetSnapshotNodes(s.ID)
		if err != nil {
			return nil, err
		}
		m := make(map[string]model.DirNode, len(nodes))
		for _, node := range nodes {
			m[strings.ToLower(node.Path)] = node
		}
		nodeMaps[i] = m
	}

	// Candidate paths present in all N snapshots that strictly increased every single step.
	type candidate struct {
		node       model.DirNode
		totalDelta int64
	}
	candidates := make(map[string]candidate)
	newestMap := nodeMaps[n-1]
	oldestMap := nodeMaps[0]

	for key, newestNode := range newestMap {
		if newestNode.Depth == 0 {
			continue
		}
		oldestNode, ok := oldestMap[key]
		if !ok {
			continue
		}
		totalDelta := newestNode.SizeBytes - oldestNode.SizeBytes
		if totalDelta < cfg.CreepingMinBytes {
			continue
		}
		strictlyGrowing := true
		prevSize := oldestNode.SizeBytes
		for i := 1; i < n; i++ {
			cur, exists := nodeMaps[i][key]
			if !exists || cur.SizeBytes <= prevSize {
				strictlyGrowing = false
				break
			}
			prevSize = cur.SizeBytes
		}
		if strictlyGrowing {
			candidates[key] = candidate{node: newestNode, totalDelta: totalDelta}
		}
	}

	// Suppress ancestor candidates if a child candidate is also creeping.
	hasCreepingChild := make(map[string]bool)
	for _, c := range candidates {
		if c.node.ParentPath != "" {
			pk := strings.ToLower(c.node.ParentPath)
			if _, ok := candidates[pk]; ok {
				hasCreepingChild[pk] = true
			}
		}
	}

	var alerts []model.Alert
	for key, c := range candidates {
		if hasCreepingChild[key] {
			continue
		}
		recent, err := d.reader.HasRecentAlert(chrono[n-1].DriveRoot, "creeping_growth", c.node.Path, cooldownSince)
		if err != nil || recent {
			continue
		}
		tag := c.node.Tag
		if tag == "" {
			tag = tagging.ClassifyPath(c.node.Path)
		}
		alerts = append(alerts, model.Alert{
			CreatedAt:  now,
			DriveRoot:  chrono[n-1].DriveRoot,
			RuleType:   "creeping_growth",
			Severity:   "warning",
			TargetPath: c.node.Path,
			Tag:        tag,
			DeltaBytes: c.totalDelta,
			Message: fmt.Sprintf(
				"Tăng âm thầm %d lần quét liên tiếp (+%s) tại %s",
				n,
				FormatBytes(c.totalDelta),
				c.node.Path,
			),
		})
		if len(alerts) >= 5 {
			break
		}
	}
	return alerts, nil
}

// ComputeForecast runs linear regression on DriveUsedBytes over windowDays.
func ComputeForecast(driveRoot string, snapsNewestFirst []model.Snapshot, windowDays int) model.ForecastResult {
	res := model.ForecastResult{
		DriveRoot:  driveRoot,
		WindowDays: windowDays,
	}
	if len(snapsNewestFirst) < 2 {
		return res
	}
	latest := snapsNewestFirst[0]
	cutoff := latest.CreatedAt.Add(-time.Duration(windowDays) * 24 * time.Hour)

	var window []model.Snapshot
	for i := len(snapsNewestFirst) - 1; i >= 0; i-- {
		if !snapsNewestFirst[i].CreatedAt.Before(cutoff) {
			window = append(window, snapsNewestFirst[i])
		}
	}
	res.SampleCount = len(window)
	if len(window) < 2 {
		return res
	}

	spanHours := window[len(window)-1].CreatedAt.Sub(window[0].CreatedAt).Hours()
	if spanHours < 6 {
		return res
	}

	t0 := window[0].CreatedAt
	var sumX, sumY, sumXX, sumXY float64
	n := float64(len(window))
	for _, s := range window {
		x := s.CreatedAt.Sub(t0).Hours() / 24.0
		y := float64(s.DriveUsedBytes)
		sumX += x
		sumY += y
		sumXX += x * x
		sumXY += x * y
	}
	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		return res
	}
	slope := (n*sumXY - sumX*sumY) / denom // bytes per day
	res.HasEnoughData = true
	res.DailyGrowthBytes = int64(math.Round(slope))

	if slope > 1024*1024 { // > 1 MB/day positive growth
		res.DaysUntilFull = float64(latest.DriveFreeBytes) / slope
	} else {
		res.DaysUntilFull = -1
	}
	return res
}

// FormatBytes formats a byte count into a human-readable IEC unit string.
func FormatBytes(b int64) string {
	neg := b < 0
	if neg {
		b = -b
	}
	const unit = 1024
	if b < unit {
		if neg {
			return fmt.Sprintf("-%d B", b)
		}
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	val := float64(b) / float64(div)
	if neg {
		return fmt.Sprintf("-%.1f %s", val, units[exp])
	}
	return fmt.Sprintf("%.1f %s", val, units[exp])
}
