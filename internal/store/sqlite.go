package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"diskpulse/internal/model"
	"diskpulse/internal/scanner"
)

// Store persists disk snapshots, pruned directory trees, large files, and alerts in SQLite.
type Store struct {
	db *sql.DB
}

// Open initializes or migrates the SQLite database at dbPath.
func Open(dbPath string) (*Store, error) {
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, err
		}
	}

	dsn := dbPath
	if dbPath == ":memory:" {
		dsn = "file::memory:?cache=shared&_pragma=foreign_keys(ON)"
	} else {
		dsn = fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", filepath.ToSlash(dbPath))
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			drive_root TEXT NOT NULL,
			created_at TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			drive_total_bytes INTEGER NOT NULL,
			drive_free_bytes INTEGER NOT NULL,
			drive_used_bytes INTEGER NOT NULL,
			scanned_bytes INTEGER NOT NULL,
			file_count INTEGER NOT NULL,
			dir_count INTEGER NOT NULL,
			error_count INTEGER NOT NULL,
			status TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_drive_time ON snapshots (drive_root, created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS dir_nodes (
			snapshot_id INTEGER NOT NULL,
			path TEXT NOT NULL,
			parent_path TEXT NOT NULL,
			name TEXT NOT NULL,
			depth INTEGER NOT NULL,
			size_bytes INTEGER NOT NULL,
			other_bytes INTEGER NOT NULL,
			file_count INTEGER NOT NULL,
			dir_count INTEGER NOT NULL,
			tag TEXT NOT NULL,
			PRIMARY KEY (snapshot_id, path),
			FOREIGN KEY (snapshot_id) REFERENCES snapshots(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_dir_nodes_parent ON dir_nodes (snapshot_id, parent_path);`,
		`CREATE TABLE IF NOT EXISTS large_files (
			snapshot_id INTEGER NOT NULL,
			path TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			modified_at TEXT NOT NULL,
			tag TEXT NOT NULL,
			PRIMARY KEY (snapshot_id, path),
			FOREIGN KEY (snapshot_id) REFERENCES snapshots(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TEXT NOT NULL,
			drive_root TEXT NOT NULL,
			rule_type TEXT NOT NULL,
			severity TEXT NOT NULL,
			target_path TEXT NOT NULL,
			tag TEXT NOT NULL,
			delta_bytes INTEGER NOT NULL,
			message TEXT NOT NULL,
			acknowledged INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_created ON alerts (created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_cooldown ON alerts (drive_root, rule_type, target_path, created_at DESC);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

// SaveScanOutput writes a completed snapshot, its pruned nodes, and large files in a single transaction.
func (s *Store) SaveScanOutput(out scanner.ScanOutput) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.Exec(
		`INSERT INTO snapshots (
			drive_root, created_at, duration_ms,
			drive_total_bytes, drive_free_bytes, drive_used_bytes,
			scanned_bytes, file_count, dir_count, error_count, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		out.Snapshot.DriveRoot,
		out.Snapshot.CreatedAt.UTC().Format(time.RFC3339Nano),
		out.Snapshot.DurationMs,
		out.Snapshot.DriveTotalBytes,
		out.Snapshot.DriveFreeBytes,
		out.Snapshot.DriveUsedBytes,
		out.Snapshot.ScannedBytes,
		out.Snapshot.FileCount,
		out.Snapshot.DirCount,
		out.Snapshot.ErrorCount,
		out.Snapshot.Status,
	)
	if err != nil {
		return 0, err
	}
	snapID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	nodeStmt, err := tx.Prepare(
		`INSERT INTO dir_nodes (
			snapshot_id, path, parent_path, name, depth,
			size_bytes, other_bytes, file_count, dir_count, tag
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return 0, err
	}
	defer nodeStmt.Close()

	for _, n := range out.Nodes {
		if _, err = nodeStmt.Exec(
			snapID, n.Path, n.ParentPath, n.Name, n.Depth,
			n.SizeBytes, n.OtherBytes, n.FileCount, n.DirCount, n.Tag,
		); err != nil {
			return 0, err
		}
	}

	fileStmt, err := tx.Prepare(
		`INSERT INTO large_files (snapshot_id, path, size_bytes, modified_at, tag)
		 VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return 0, err
	}
	defer fileStmt.Close()

	for _, lf := range out.LargeFiles {
		if _, err = fileStmt.Exec(
			snapID, lf.Path, lf.SizeBytes, lf.ModifiedAt.UTC().Format(time.RFC3339Nano), lf.Tag,
		); err != nil {
			return 0, err
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return snapID, nil
}

// ListSnapshots returns snapshots for driveRoot ordered newest first. If driveRoot is empty, lists across all drives.
func (s *Store) ListSnapshots(driveRoot string, limit int) ([]model.Snapshot, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if driveRoot == "" {
		rows, err = s.db.Query(
			`SELECT id, drive_root, created_at, duration_ms, drive_total_bytes, drive_free_bytes, drive_used_bytes, scanned_bytes, file_count, dir_count, error_count, status
			 FROM snapshots ORDER BY created_at DESC LIMIT ?`, limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, drive_root, created_at, duration_ms, drive_total_bytes, drive_free_bytes, drive_used_bytes, scanned_bytes, file_count, dir_count, error_count, status
			 FROM snapshots WHERE drive_root = ? ORDER BY created_at DESC LIMIT ?`, driveRoot, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.Snapshot
	for rows.Next() {
		snap, scanErr := scanSnapshotRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		list = append(list, snap)
	}
	return list, rows.Err()
}

// GetSnapshot returns a single snapshot by ID.
func (s *Store) GetSnapshot(id int64) (model.Snapshot, error) {
	row := s.db.QueryRow(
		`SELECT id, drive_root, created_at, duration_ms, drive_total_bytes, drive_free_bytes, drive_used_bytes, scanned_bytes, file_count, dir_count, error_count, status
		 FROM snapshots WHERE id = ?`, id,
	)
	return scanSnapshotRow(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSnapshotRow(r rowScanner) (model.Snapshot, error) {
	var snap model.Snapshot
	var createdStr string
	err := r.Scan(
		&snap.ID, &snap.DriveRoot, &createdStr, &snap.DurationMs,
		&snap.DriveTotalBytes, &snap.DriveFreeBytes, &snap.DriveUsedBytes,
		&snap.ScannedBytes, &snap.FileCount, &snap.DirCount, &snap.ErrorCount, &snap.Status,
	)
	if err != nil {
		return snap, err
	}
	snap.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	return snap, nil
}

// GetSnapshotNodes returns all persisted directory nodes for snapshotID.
func (s *Store) GetSnapshotNodes(snapshotID int64) ([]model.DirNode, error) {
	rows, err := s.db.Query(
		`SELECT snapshot_id, path, parent_path, name, depth, size_bytes, other_bytes, file_count, dir_count, tag
		 FROM dir_nodes WHERE snapshot_id = ?`, snapshotID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []model.DirNode
	for rows.Next() {
		var n model.DirNode
		if err := rows.Scan(
			&n.SnapshotID, &n.Path, &n.ParentPath, &n.Name, &n.Depth,
			&n.SizeBytes, &n.OtherBytes, &n.FileCount, &n.DirCount, &n.Tag,
		); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetSnapshotChildren returns immediate persisted child directories under parentPath sorted largest first.
func (s *Store) GetSnapshotChildren(snapshotID int64, parentPath string) ([]model.DirNode, error) {
	rows, err := s.db.Query(
		`SELECT snapshot_id, path, parent_path, name, depth, size_bytes, other_bytes, file_count, dir_count, tag
		 FROM dir_nodes WHERE snapshot_id = ? AND lower(parent_path) = lower(?) ORDER BY size_bytes DESC`,
		snapshotID, parentPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []model.DirNode
	for rows.Next() {
		var n model.DirNode
		if err := rows.Scan(
			&n.SnapshotID, &n.Path, &n.ParentPath, &n.Name, &n.Depth,
			&n.SizeBytes, &n.OtherBytes, &n.FileCount, &n.DirCount, &n.Tag,
		); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetSnapshotLargeFiles returns top large files recorded in snapshotID ordered largest first.
func (s *Store) GetSnapshotLargeFiles(snapshotID int64) ([]model.LargeFile, error) {
	rows, err := s.db.Query(
		`SELECT snapshot_id, path, size_bytes, modified_at, tag
		 FROM large_files WHERE snapshot_id = ? ORDER BY size_bytes DESC`, snapshotID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.LargeFile
	for rows.Next() {
		var f model.LargeFile
		var modStr string
		if err := rows.Scan(&f.SnapshotID, &f.Path, &f.SizeBytes, &modStr, &f.Tag); err != nil {
			return nil, err
		}
		f.ModifiedAt, _ = time.Parse(time.RFC3339Nano, modStr)
		files = append(files, f)
	}
	return files, rows.Err()
}

// SaveAlerts inserts generated alerts and populates their assigned IDs.
func (s *Store) SaveAlerts(alerts []model.Alert) error {
	if len(alerts) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(
		`INSERT INTO alerts (
			created_at, drive_root, rule_type, severity, target_path, tag, delta_bytes, message, acknowledged
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i := range alerts {
		ack := 0
		if alerts[i].Acknowledged {
			ack = 1
		}
		res, execErr := stmt.Exec(
			alerts[i].CreatedAt.UTC().Format(time.RFC3339Nano),
			alerts[i].DriveRoot,
			alerts[i].RuleType,
			alerts[i].Severity,
			alerts[i].TargetPath,
			alerts[i].Tag,
			alerts[i].DeltaBytes,
			alerts[i].Message,
			ack,
		)
		if execErr != nil {
			err = execErr
			return err
		}
		id, _ := res.LastInsertId()
		alerts[i].ID = id
	}
	return tx.Commit()
}

// ListAlerts returns recent alerts ordered newest first.
func (s *Store) ListAlerts(limit int, unacknowledgedOnly bool) ([]model.Alert, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, created_at, drive_root, rule_type, severity, target_path, tag, delta_bytes, message, acknowledged FROM alerts`
	if unacknowledgedOnly {
		query += ` WHERE acknowledged = 0`
	}
	query += ` ORDER BY created_at DESC LIMIT ?`

	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.Alert
	for rows.Next() {
		var a model.Alert
		var createdStr string
		var ack int
		if err := rows.Scan(
			&a.ID, &createdStr, &a.DriveRoot, &a.RuleType, &a.Severity,
			&a.TargetPath, &a.Tag, &a.DeltaBytes, &a.Message, &ack,
		); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		a.Acknowledged = ack != 0
		list = append(list, a)
	}
	return list, rows.Err()
}

// AcknowledgeAlert marks a single alert as read/acknowledged.
func (s *Store) AcknowledgeAlert(id int64) error {
	_, err := s.db.Exec(`UPDATE alerts SET acknowledged = 1 WHERE id = ?`, id)
	return err
}

// AcknowledgeAllAlerts marks all unread alerts as acknowledged.
func (s *Store) AcknowledgeAllAlerts() error {
	_, err := s.db.Exec(`UPDATE alerts SET acknowledged = 1 WHERE acknowledged = 0`)
	return err
}

// HasRecentAlert checks if an alert with the same (driveRoot, ruleType, targetPath) was raised since cutoff.
func (s *Store) HasRecentAlert(driveRoot, ruleType, targetPath string, since time.Time) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM alerts
		 WHERE drive_root = ? AND rule_type = ? AND lower(target_path) = lower(?) AND created_at >= ?`,
		driveRoot, ruleType, targetPath, since.UTC().Format(time.RFC3339Nano),
	).Scan(&count)
	return count > 0, err
}

// ListTrackedDrives returns distinct drive_root values present in snapshots.
func (s *Store) ListTrackedDrives() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT drive_root FROM snapshots ORDER BY drive_root ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var drives []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		drives = append(drives, d)
	}
	return drives, rows.Err()
}

// ApplyRetention thins older snapshots: keep all <=14d, 1/day for 15-90d, 1/week for >90d.
func (s *Store) ApplyRetention(driveRoot string, now time.Time) (int64, error) {
	snaps, err := s.ListSnapshots(driveRoot, 5000)
	if err != nil || len(snaps) <= 1 {
		return 0, err
	}

	cutoffRecent := now.Add(-14 * 24 * time.Hour)
	cutoffDaily := now.Add(-90 * 24 * time.Hour)

	keepDay := make(map[string]int64)
	keepWeek := make(map[string]int64)
	var toDelete []int64

	// snaps is newest-first; always keep snaps[0] and iterate oldest-first to keep earliest per bucket.
	for i := len(snaps) - 1; i >= 0; i-- {
		snap := snaps[i]
		if i == 0 || !snap.CreatedAt.Before(cutoffRecent) {
			continue
		}
		if !snap.CreatedAt.Before(cutoffDaily) {
			dayKey := snap.CreatedAt.Local().Format("2006-01-02")
			if _, ok := keepDay[dayKey]; !ok {
				keepDay[dayKey] = snap.ID
				continue
			}
			toDelete = append(toDelete, snap.ID)
			continue
		}
		year, week := snap.CreatedAt.Local().ISOWeek()
		weekKey := fmt.Sprintf("%04d-W%02d", year, week)
		if _, ok := keepWeek[weekKey]; !ok {
			keepWeek[weekKey] = snap.ID
			continue
		}
		toDelete = append(toDelete, snap.ID)
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(toDelete)), ",")
	args := make([]any, len(toDelete))
	for i, id := range toDelete {
		args[i] = id
	}
	res, err := s.db.Exec(fmt.Sprintf(`DELETE FROM snapshots WHERE id IN (%s)`, placeholders), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
