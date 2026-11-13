package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"diskpulse/internal/alert"
	"diskpulse/internal/config"
	"diskpulse/internal/model"
	"diskpulse/internal/notify"
	"diskpulse/internal/scanner"
	"diskpulse/internal/store"
	"diskpulse/internal/tray"
)

// Engine coordinates scheduled scans, snapshot persistence, anomaly alerts, and tray updates.
type Engine struct {
	mu           sync.RWMutex
	cfg          config.Config
	progress     model.ScanProgress
	scanning     bool
	dashboardURL string

	store    *store.Store
	detector *alert.Detector
	notifier *notify.Dispatcher
	tray     *tray.Tray

	wakeSchedule chan struct{}
}

// New constructs an Engine backed by store and notifier.
func New(cfg config.Config, st *store.Store, notifier *notify.Dispatcher) *Engine {
	return &Engine{
		cfg:          cfg,
		store:        st,
		detector:     alert.NewDetector(st),
		notifier:     notifier,
		wakeSchedule: make(chan struct{}, 1),
	}
}

// AttachTray wires system tray updates to the engine.
func (e *Engine) AttachTray(t *tray.Tray) {
	e.mu.Lock()
	e.tray = t
	e.mu.Unlock()
	e.RefreshTrayState()
}

// SetDashboardURL stores the active local dashboard URL.
func (e *Engine) SetDashboardURL(url string) {
	e.mu.Lock()
	e.dashboardURL = url
	e.mu.Unlock()
}

// DashboardURL returns the local dashboard URL.
func (e *Engine) DashboardURL() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dashboardURL
}

// OpenDashboard launches the default browser to the local dashboard.
func (e *Engine) OpenDashboard() {
	url := e.DashboardURL()
	if url == "" {
		return
	}
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}

// Config returns a copy of the active configuration.
func (e *Engine) Config() config.Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// Progress returns current live scan progress.
func (e *Engine) Progress() model.ScanProgress {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.progress
}

// UpdateConfig validates, persists, and applies updated configuration.
func (e *Engine) UpdateConfig(next config.Config) error {
	next.Normalize()
	if next.AutoStart != e.Config().AutoStart {
		if err := config.SetAutoStart(next.AutoStart); err != nil {
			return err
		}
	}
	if err := config.Save(next); err != nil {
		return err
	}
	e.mu.Lock()
	e.cfg = next
	e.mu.Unlock()

	select {
	case e.wakeSchedule <- struct{}{}:
	default:
	}
	e.RefreshTrayState()
	return nil
}

// TogglePause flips automatic scheduled scanning.
func (e *Engine) TogglePause() error {
	cfg := e.Config()
	cfg.Paused = !cfg.Paused
	return e.UpdateConfig(cfg)
}

// ToggleAutoStart flips Windows startup registration.
func (e *Engine) ToggleAutoStart() error {
	cfg := e.Config()
	cfg.AutoStart = !cfg.AutoStart
	return e.UpdateConfig(cfg)
}

// TargetDrives returns configured drives or auto-detected Windows fixed drives.
func (e *Engine) TargetDrives() ([]model.DriveInfo, error) {
	fixed, err := scanner.ListFixedDrives()
	if err != nil {
		return nil, err
	}
	cfg := e.Config()
	if len(cfg.Drives) == 0 {
		return fixed, nil
	}

	byRoot := make(map[string]model.DriveInfo, len(fixed))
	for _, d := range fixed {
		byRoot[strings.ToUpper(d.Root)] = d
	}

	var selected []model.DriveInfo
	for _, root := range cfg.Drives {
		key := strings.ToUpper(root)
		if info, ok := byRoot[key]; ok {
			selected = append(selected, info)
		} else if info, err := scanner.GetDriveStats(root); err == nil {
			selected = append(selected, info)
		}
	}
	return selected, nil
}

// TriggerScan starts an asynchronous scan for driveRoot (or all target drives if empty).
// Returns false if a scan is already running.
func (e *Engine) TriggerScan(ctx context.Context, driveRoot string) bool {
	e.mu.Lock()
	if e.scanning {
		e.mu.Unlock()
		return false
	}
	e.scanning = true
	e.progress = model.ScanProgress{
		Active:    true,
		DriveRoot: driveRoot,
		StartedAt: time.Now(),
	}
	e.mu.Unlock()

	e.RefreshTrayState()

	go func() {
		defer func() {
			e.mu.Lock()
			e.scanning = false
			e.progress = model.ScanProgress{}
			e.mu.Unlock()
			e.RefreshTrayState()
		}()
		e.runScanSync(ctx, driveRoot)
	}()
	return true
}

func (e *Engine) runScanSync(ctx context.Context, driveRoot string) {
	cfg := e.Config()
	var targets []string
	if strings.TrimSpace(driveRoot) != "" {
		targets = []string{driveRoot}
	} else {
		drives, err := e.TargetDrives()
		if err != nil {
			return
		}
		for _, d := range drives {
			targets = append(targets, d.Root)
		}
	}

	for _, root := range targets {
		if ctx.Err() != nil {
			return
		}
		out, err := scanner.ScanDrive(ctx, root, cfg, func(p model.ScanProgress) {
			e.mu.Lock()
			e.progress = p
			e.mu.Unlock()
		})
		if err != nil {
			continue
		}
		if _, err := e.store.SaveScanOutput(out); err != nil {
			continue
		}
		now := time.Now()
		_, _ = e.store.ApplyRetention(out.Snapshot.DriveRoot, now)

		alerts, err := e.detector.EvaluateDrive(out.Snapshot.DriveRoot, cfg.Alerts, now)
		if err == nil && len(alerts) > 0 {
			if saveErr := e.store.SaveAlerts(alerts); saveErr == nil && e.notifier != nil {
				e.notifier.SendAlerts(alerts, cfg.Alerts)
			}
		}
	}
}

// StartScheduler runs periodic scans until ctx is canceled.
func (e *Engine) StartScheduler(ctx context.Context) {
	// On startup, establish an initial baseline if any target drive lacks a recent snapshot.
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		if e.Config().Paused {
			return
		}
		if e.needsInitialScan() {
			e.TriggerScan(ctx, "")
		}
	}()

	for {
		cfg := e.Config()
		interval := time.Duration(cfg.ScanIntervalMinutes) * time.Minute
		timer := time.NewTimer(interval)

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-e.wakeSchedule:
			timer.Stop()
		case <-timer.C:
			if !e.Config().Paused {
				e.TriggerScan(ctx, "")
			}
		}
	}
}

func (e *Engine) needsInitialScan() bool {
	drives, err := e.TargetDrives()
	if err != nil {
		return false
	}
	interval := time.Duration(e.Config().ScanIntervalMinutes) * time.Minute
	for _, d := range drives {
		snaps, err := e.store.ListSnapshots(d.Root, 1)
		if err != nil || len(snaps) == 0 {
			return true
		}
		if time.Since(snaps[0].CreatedAt) >= interval {
			return true
		}
	}
	return false
}

// RefreshTrayState updates tray tooltip, status icon, and menu flags.
func (e *Engine) RefreshTrayState() {
	e.mu.RLock()
	tr := e.tray
	cfg := e.cfg
	scanning := e.scanning
	e.mu.RUnlock()

	if tr == nil {
		return
	}

	severity := "normal"
	unreadAlerts, err := e.store.ListAlerts(20, true)
	if err == nil {
		for _, a := range unreadAlerts {
			if a.Severity == "critical" {
				severity = "critical"
				break
			}
			if a.Severity == "warning" {
				severity = "warning"
			}
		}
	}

	var parts []string
	if drives, err := e.TargetDrives(); err == nil {
		for _, d := range drives {
			label := strings.TrimSuffix(d.Root, `\`)
			parts = append(parts, fmt.Sprintf("%s %s trống", label, alert.FormatBytes(d.FreeBytes)))
		}
	}

	tooltip := "DiskPulse"
	if scanning {
		tooltip = "DiskPulse — Đang quét ổ đĩa..."
	} else if len(parts) > 0 {
		tooltip = "DiskPulse: " + strings.Join(parts, " | ")
	}
	if len(unreadAlerts) > 0 {
		tooltip += fmt.Sprintf(" (%d cảnh báo)", len(unreadAlerts))
	}
	// Windows NOTIFYICONDATA.szTip allows up to 127 UTF-16 code units.
	if runes := []rune(tooltip); len(runes) > 120 {
		tooltip = string(runes[:117]) + "..."
	}

	tr.UpdateState(tray.State{
		Tooltip:   tooltip,
		Severity:  severity,
		Scanning:  scanning,
		Paused:    cfg.Paused,
		AutoStart: cfg.AutoStart,
	})
}
