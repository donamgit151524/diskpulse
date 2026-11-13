package model

import "time"

// DriveInfo describes a fixed local drive on Windows.
type DriveInfo struct {
	Root       string  `json:"root"`
	Label      string  `json:"label"`
	TotalBytes int64   `json:"total_bytes"`
	FreeBytes  int64   `json:"free_bytes"`
	UsedBytes  int64   `json:"used_bytes"`
	FreePct    float64 `json:"free_pct"`
}

// Snapshot captures drive usage and pruned directory tree at a point in time.
type Snapshot struct {
	ID              int64     `json:"id"`
	DriveRoot       string    `json:"drive_root"`
	CreatedAt       time.Time `json:"created_at"`
	DurationMs      int64     `json:"duration_ms"`
	DriveTotalBytes int64     `json:"drive_total_bytes"`
	DriveFreeBytes  int64     `json:"drive_free_bytes"`
	DriveUsedBytes  int64     `json:"drive_used_bytes"`
	ScannedBytes    int64     `json:"scanned_bytes"`
	FileCount       int64     `json:"file_count"`
	DirCount        int64     `json:"dir_count"`
	ErrorCount      int64     `json:"error_count"`
	Status          string    `json:"status"`
}

// DirNode represents a persisted directory entry within a snapshot.
type DirNode struct {
	SnapshotID int64  `json:"snapshot_id"`
	Path       string `json:"path"`
	ParentPath string `json:"parent_path"`
	Name       string `json:"name"`
	Depth      int    `json:"depth"`
	SizeBytes  int64  `json:"size_bytes"`
	OtherBytes int64  `json:"other_bytes"`
	FileCount  int64  `json:"file_count"`
	DirCount   int64  `json:"dir_count"`
	Tag        string `json:"tag"`
}

// LargeFile represents one of the largest individual files seen during a snapshot.
type LargeFile struct {
	SnapshotID int64     `json:"snapshot_id"`
	Path       string    `json:"path"`
	SizeBytes  int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	Tag        string    `json:"tag"`
}

// Alert represents a generated disk warning or growth anomaly.
type Alert struct {
	ID           int64     `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	DriveRoot    string    `json:"drive_root"`
	RuleType     string    `json:"rule_type"`
	Severity     string    `json:"severity"`
	TargetPath   string    `json:"target_path"`
	Tag          string    `json:"tag"`
	DeltaBytes   int64     `json:"delta_bytes"`
	Message      string    `json:"message"`
	Acknowledged bool      `json:"acknowledged"`
}

// DiffEntry describes the size change of a directory between two snapshots.
type DiffEntry struct {
	Path                 string `json:"path"`
	ParentPath           string `json:"parent_path"`
	Name                 string `json:"name"`
	Depth                int    `json:"depth"`
	SizeBefore           int64  `json:"size_before"`
	SizeAfter            int64  `json:"size_after"`
	DeltaBytes           int64  `json:"delta_bytes"`
	AttributedDeltaBytes int64  `json:"attributed_delta_bytes"`
	Status               string `json:"status"`
	Tag                  string `json:"tag"`
	Hint                 string `json:"hint"`
}

// LargeFileDiff describes a newly appeared or grown large file between snapshots.
type LargeFileDiff struct {
	Path       string `json:"path"`
	SizeBefore int64  `json:"size_before"`
	SizeAfter  int64  `json:"size_after"`
	DeltaBytes int64  `json:"delta_bytes"`
	Status     string `json:"status"`
	Tag        string `json:"tag"`
}

// TagSummary aggregates growth attributed to a category tag.
type TagSummary struct {
	Tag        string `json:"tag"`
	Label      string `json:"label"`
	DeltaBytes int64  `json:"delta_bytes"`
	NodeCount  int    `json:"node_count"`
}

// DiffResult contains the full comparison between two snapshots.
type DiffResult struct {
	DriveRoot       string          `json:"drive_root"`
	SnapshotA       Snapshot        `json:"snapshot_a"`
	SnapshotB       Snapshot        `json:"snapshot_b"`
	DriveUsedDelta  int64           `json:"drive_used_delta"`
	ScannedDelta    int64           `json:"scanned_delta"`
	RootCauses      []DiffEntry     `json:"root_causes"`
	FreedSpaces     []DiffEntry     `json:"freed_spaces"`
	AllChanges      []DiffEntry     `json:"all_changes"`
	LargeFileDiffs  []LargeFileDiff `json:"large_file_diffs"`
	TagBreakdown    []TagSummary    `json:"tag_breakdown"`
}

// ForecastResult estimates daily disk growth and days remaining before full.
type ForecastResult struct {
	DriveRoot          string  `json:"drive_root"`
	DailyGrowthBytes   int64   `json:"daily_growth_bytes"`
	DaysUntilFull      float64 `json:"days_until_full"`
	HasEnoughData      bool    `json:"has_enough_data"`
	SampleCount        int     `json:"sample_count"`
	WindowDays         int     `json:"window_days"`
}

// ScanProgress reports live status while scanning a drive.
type ScanProgress struct {
	Active       bool      `json:"active"`
	DriveRoot    string    `json:"drive_root"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	DirsScanned  int64     `json:"dirs_scanned"`
	FilesScanned int64     `json:"files_scanned"`
	BytesScanned int64     `json:"bytes_scanned"`
	CurrentPath  string    `json:"current_path"`
}
