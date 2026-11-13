package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	AppName     = "DiskPulse"
	DefaultPort = 47821
)

// AlertConfig controls threshold, spike, creeping growth, forecast and notification rules.
type AlertConfig struct {
	LowSpacePercent      float64 `json:"low_space_percent"`
	LowSpaceGB           float64 `json:"low_space_gb"`
	CriticalSpacePercent float64 `json:"critical_space_percent"`
	CriticalSpaceGB      float64 `json:"critical_space_gb"`
	SpikeBytes           int64   `json:"spike_bytes"`
	CreepingMinRuns      int     `json:"creeping_min_runs"`
	CreepingMinBytes     int64   `json:"creeping_min_bytes"`
	ForecastDaysWarn     int     `json:"forecast_days_warn"`
	CooldownHours        int     `json:"cooldown_hours"`
	EnableToast          bool    `json:"enable_toast"`
	WebhookURL           string  `json:"webhook_url"`
	TelegramBotToken     string  `json:"telegram_bot_token"`
	TelegramChatID       string  `json:"telegram_chat_id"`
}

// Config holds persisted application configuration.
type Config struct {
	Drives                  []string    `json:"drives"`
	ScanIntervalMinutes     int         `json:"scan_interval_minutes"`
	MaxDepth                int         `json:"max_depth"`
	MinPersistBytes         int64       `json:"min_persist_bytes"`
	LargeFileThresholdBytes int64       `json:"large_file_threshold_bytes"`
	TopLargeFiles           int         `json:"top_large_files"`
	ScannerWorkers          int         `json:"scanner_workers"`
	IgnorePaths             []string    `json:"ignore_paths"`
	Port                    int         `json:"port"`
	AutoStart               bool        `json:"auto_start"`
	Paused                  bool        `json:"paused"`
	Alerts                  AlertConfig `json:"alerts"`
}

// DefaultConfig returns safe defaults tailored for personal Windows machines.
func DefaultConfig() Config {
	return Config{
		Drives:                  []string{}, // Empty means auto-detect fixed local drives.
		ScanIntervalMinutes:     100,        // Every 100 minutes.
		MaxDepth:                5,
		MinPersistBytes:         50 * 1024 * 1024,  // 50 MB
		LargeFileThresholdBytes: 100 * 1024 * 1024, // 100 MB
		TopLargeFiles:           50,
		ScannerWorkers:          3,
		IgnorePaths: []string{
			`System Volume Information`,
		},
		Port:      DefaultPort,
		AutoStart: false,
		Paused:    false,
		Alerts: AlertConfig{
			LowSpacePercent:      15.0,
			LowSpaceGB:           20.0,
			CriticalSpacePercent: 8.0,
			CriticalSpaceGB:      10.0,
			SpikeBytes:           2 * 1024 * 1024 * 1024, // 2 GB
			CreepingMinRuns:      4,
			CreepingMinBytes:     2 * 1024 * 1024 * 1024, // 2 GB
			ForecastDaysWarn:     14,
			CooldownHours:        24,
			EnableToast:          true,
		},
	}
}

// DataDir returns the per-user application data directory (%LocalAppData%\DiskPulse).
func DataDir() (string, error) {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dir := filepath.Join(local, AppName)
		return dir, os.MkdirAll(dir, 0o755)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, AppName)
	return dir, os.MkdirAll(dir, 0o755)
}

// DBPath returns the SQLite database path inside DataDir.
func DBPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "diskpulse.db"), nil
}

// ConfigPath returns the config.json path inside DataDir.
func ConfigPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads config from disk or writes defaults if missing.
func Load() (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return DefaultConfig(), err
	}
	return LoadFrom(path)
}

// LoadFrom reads config from an explicit path, filling missing fields with defaults.
func LoadFrom(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = SaveTo(path, cfg)
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.Normalize()
	return cfg, nil
}

// Save writes config to the default config path.
func Save(cfg Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return SaveTo(path, cfg)
}

// SaveTo writes normalized config JSON atomically to path.
func SaveTo(path string, cfg Config) error {
	cfg.Normalize()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Normalize enforces safe bounds on configurable parameters.
func (c *Config) Normalize() {
	if c.ScanIntervalMinutes < 5 {
		c.ScanIntervalMinutes = 5
	}
	if c.MaxDepth < 2 {
		c.MaxDepth = 2
	}
	if c.MaxDepth > 12 {
		c.MaxDepth = 12
	}
	if c.MinPersistBytes < 1024*1024 {
		c.MinPersistBytes = 1024 * 1024
	}
	if c.LargeFileThresholdBytes < 10*1024*1024 {
		c.LargeFileThresholdBytes = 10 * 1024 * 1024
	}
	if c.TopLargeFiles < 10 {
		c.TopLargeFiles = 10
	}
	if c.ScannerWorkers < 1 {
		c.ScannerWorkers = 1
	}
	if c.ScannerWorkers > 8 {
		c.ScannerWorkers = 8
	}
	if c.Port < 1024 || c.Port > 65535 {
		c.Port = DefaultPort
	}
	if c.Alerts.LowSpacePercent <= 0 {
		c.Alerts.LowSpacePercent = 15
	}
	if c.Alerts.CriticalSpacePercent <= 0 {
		c.Alerts.CriticalSpacePercent = 8
	}
	if c.Alerts.SpikeBytes < 100*1024*1024 {
		c.Alerts.SpikeBytes = 100 * 1024 * 1024
	}
	if c.Alerts.CreepingMinRuns < 2 {
		c.Alerts.CreepingMinRuns = 2
	}
	if c.Alerts.CreepingMinBytes < 100*1024*1024 {
		c.Alerts.CreepingMinBytes = 100 * 1024 * 1024
	}
	if c.Alerts.CooldownHours < 1 {
		c.Alerts.CooldownHours = 1
	}
	cleaned := make([]string, 0, len(c.Drives))
	seen := make(map[string]struct{})
	for _, d := range c.Drives {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if len(d) == 2 && d[1] == ':' {
			d += `\`
		}
		d = strings.ToUpper(d[:1]) + d[1:]
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			cleaned = append(cleaned, d)
		}
	}
	c.Drives = cleaned
}
