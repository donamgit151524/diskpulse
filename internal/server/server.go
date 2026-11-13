package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"diskpulse/internal/alert"
	"diskpulse/internal/app"
	"diskpulse/internal/config"
	"diskpulse/internal/diff"
	"diskpulse/internal/model"
	"diskpulse/internal/scanner"
	"diskpulse/internal/store"
	"diskpulse/web"
)

// Server serves the local HTTP API and embedded web dashboard on 127.0.0.1.
type Server struct {
	engine   *app.Engine
	store    *store.Store
	httpSrv  *http.Server
	listener net.Listener
	url      string
}

// NewHandler constructs the HTTP handler for the local API and embedded dashboard assets.
func NewHandler(engine *app.Engine, st *store.Store) (http.Handler, error) {
	s := &Server{
		engine: engine,
		store:  st,
	}
	return s.buildMux()
}

func (s *Server) buildMux() (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/scan", s.handleScan)
	mux.HandleFunc("/api/snapshots", s.handleSnapshots)
	mux.HandleFunc("/api/snapshot-children", s.handleSnapshotChildren)
	mux.HandleFunc("/api/snapshot-large-files", s.handleSnapshotLargeFiles)
	mux.HandleFunc("/api/diff", s.handleDiff)
	mux.HandleFunc("/api/alerts", s.handleAlerts)
	mux.HandleFunc("/api/alerts/ack", s.handleAlertAck)
	mux.HandleFunc("/api/alerts/ack-all", s.handleAlertAckAll)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/open-explorer", s.handleOpenExplorer)
	mux.HandleFunc("/api/backup/download", s.handleBackupDownload)

	staticSub, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		return nil, err
	}
	mux.Handle("/", http.FileServer(http.FS(staticSub)))
	return mux, nil
}

// Start binds an HTTP server on 127.0.0.1 starting from cfgPort (falling back to nearby ports if busy).
func Start(ctx context.Context, cfgPort int, engine *app.Engine, st *store.Store) (*Server, error) {
	ln, boundPort, err := listenLocal(cfgPort)
	if err != nil {
		return nil, err
	}

	s := &Server{
		engine:   engine,
		store:    st,
		listener: ln,
		url:      fmt.Sprintf("http://127.0.0.1:%d", boundPort),
	}

	mux, err := s.buildMux()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()

	go func() {
		_ = s.httpSrv.Serve(ln)
	}()

	return s, nil
}

// URL returns the bound local dashboard URL.
func (s *Server) URL() string {
	return s.url
}

func listenLocal(preferredPort int) (net.Listener, int, error) {
	if preferredPort > 0 {
		for offset := 0; offset < 12; offset++ {
			p := preferredPort + offset
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
			if err == nil {
				return ln, ln.Addr().(*net.TCPAddr).Port, nil
			}
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	addr := ln.Addr().(*net.TCPAddr)
	return ln, addr.Port, nil
}

type driveOverview struct {
	Drive          model.DriveInfo      `json:"drive"`
	LatestSnapshot *model.Snapshot      `json:"latest_snapshot,omitempty"`
	Delta24hBytes  int64                `json:"delta_24h_bytes"`
	Forecast       model.ForecastResult `json:"forecast"`
	History        []model.Snapshot     `json:"history"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	drives, err := s.engine.TargetDrives()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	overviews := make([]driveOverview, 0, len(drives))
	for _, d := range drives {
		snaps, _ := s.store.ListSnapshots(d.Root, 60)
		var latest *model.Snapshot
		var delta24h int64
		if len(snaps) > 0 {
			cp := snaps[0]
			latest = &cp
			if len(snaps) >= 2 {
				base := pickNearestBefore(snaps, snaps[0].CreatedAt.Add(-24*time.Hour))
				delta24h = snaps[0].DriveUsedBytes - base.DriveUsedBytes
			}
		}
		forecast := alert.ComputeForecast(d.Root, snaps, 30)

		// Chronological order for chart plotting.
		history := make([]model.Snapshot, len(snaps))
		for i := range snaps {
			history[i] = snaps[len(snaps)-1-i]
		}

		overviews = append(overviews, driveOverview{
			Drive:          d,
			LatestSnapshot: latest,
			Delta24hBytes:  delta24h,
			Forecast:       forecast,
			History:        history,
		})
	}

	unread, _ := s.store.ListAlerts(100, true)
	allFixed, _ := scanner.ListFixedDrives()

	writeJSON(w, http.StatusOK, map[string]any{
		"drives":           overviews,
		"available_drives": allFixed,
		"progress":         s.engine.Progress(),
		"unread_alerts":    len(unread),
		"config":           s.engine.Config(),
	})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Drive string `json:"drive"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	started := s.engine.TriggerScan(context.Background(), req.Drive)
	writeJSON(w, http.StatusOK, map[string]any{
		"started":  started,
		"progress": s.engine.Progress(),
	})
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	drive := r.URL.Query().Get("drive")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 60
	}
	snaps, err := s.store.ListSnapshots(drive, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) handleSnapshotChildren(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapID, err := strconv.ParseInt(r.URL.Query().Get("snapshot_id"), 10, 64)
	if err != nil || snapID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("invalid snapshot_id"))
		return
	}
	parentPath := r.URL.Query().Get("path")
	if parentPath == "" {
		snap, err := s.store.GetSnapshot(snapID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		parentPath = snap.DriveRoot
	}
	children, err := s.store.GetSnapshotChildren(snapID, parentPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"children": children})
}

func (s *Server) handleSnapshotLargeFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapID, err := strconv.ParseInt(r.URL.Query().Get("snapshot_id"), 10, 64)
	if err != nil || snapID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("invalid snapshot_id"))
		return
	}
	files, err := s.store.GetSnapshotLargeFiles(snapID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	drive := q.Get("drive")
	preset := q.Get("preset")
	idAStr := q.Get("a")
	idBStr := q.Get("b")

	var snapA, snapB model.Snapshot
	var err error

	if idAStr != "" && idBStr != "" {
		idA, errA := strconv.ParseInt(idAStr, 10, 64)
		idB, errB := strconv.ParseInt(idBStr, 10, 64)
		if errA != nil || errB != nil {
			writeErr(w, http.StatusBadRequest, errors.New("invalid snapshot IDs"))
			return
		}
		snapA, err = s.store.GetSnapshot(idA)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		snapB, err = s.store.GetSnapshot(idB)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
	} else {
		if drive == "" {
			drives, _ := s.engine.TargetDrives()
			if len(drives) > 0 {
				drive = drives[0].Root
			}
		}
		snaps, listErr := s.store.ListSnapshots(drive, 200)
		if listErr != nil {
			writeErr(w, http.StatusInternalServerError, listErr)
			return
		}
		if len(snaps) < 2 {
			writeJSON(w, http.StatusOK, map[string]any{
				"needs_more_snapshots": true,
				"snapshot_count":       len(snaps),
			})
			return
		}
		snapB = snaps[0]
		dur := 24 * time.Hour
		switch preset {
		case "7d":
			dur = 7 * 24 * time.Hour
		case "30d":
			dur = 30 * 24 * time.Hour
		case "prev":
			snapA = snaps[1]
		}
		if preset != "prev" {
			snapA = pickNearestBefore(snaps, snapB.CreatedAt.Add(-dur))
		}
	}

	nodesA, err := s.store.GetSnapshotNodes(snapA.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	nodesB, err := s.store.GetSnapshotNodes(snapB.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	filesA, _ := s.store.GetSnapshotLargeFiles(snapA.ID)
	filesB, _ := s.store.GetSnapshotLargeFiles(snapB.ID)

	res := diff.CompareSnapshots(snapA, snapB, nodesA, nodesB, filesA, filesB)
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_more_snapshots": false,
		"diff":                 res,
	})
}

func pickNearestBefore(snapsNewestFirst []model.Snapshot, target time.Time) model.Snapshot {
	best := snapsNewestFirst[1]
	bestDist := math.Abs(best.CreatedAt.Sub(target).Seconds())
	for i := 2; i < len(snapsNewestFirst); i++ {
		dist := math.Abs(snapsNewestFirst[i].CreatedAt.Sub(target).Seconds())
		if dist < bestDist {
			bestDist = dist
			best = snapsNewestFirst[i]
		}
	}
	return best
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	unreadOnly := r.URL.Query().Get("unread") == "true"
	alerts, err := s.store.ListAlerts(100, unreadOnly)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

func (s *Server) handleAlertAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("invalid alert id"))
		return
	}
	if err := s.store.AcknowledgeAlert(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.engine.RefreshTrayState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAlertAckAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.store.AcknowledgeAllAlerts(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.engine.RefreshTrayState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.engine.Config())
	case http.MethodPost:
		var next config.Config
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.engine.UpdateConfig(next); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, s.engine.Config())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleOpenExplorer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	clean := filepath.Clean(strings.TrimSpace(req.Path))
	if clean == "" || strings.HasPrefix(clean, `\\`) || len(clean) < 2 || clean[1] != ':' {
		writeErr(w, http.StatusBadRequest, errors.New("only local drive paths are allowed"))
		return
	}
	info, err := os.Stat(clean)
	if err != nil {
		// Fall back to existing parent directory if target was recently moved or deleted.
		parent := filepath.Dir(clean)
		pInfo, pErr := os.Stat(parent)
		if pErr != nil || !pInfo.IsDir() {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		_ = exec.Command("explorer.exe", parent).Start()
		writeJSON(w, http.StatusOK, map[string]any{"opened": parent})
		return
	}

	if info.IsDir() {
		_ = exec.Command("explorer.exe", clean).Start()
	} else {
		_ = exec.Command("explorer.exe", "/select,", clean).Start()
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": clean})
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tmpDir, err := os.MkdirTemp("", "diskpulse-backup-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(tmpDir)

	backupDB := filepath.Join(tmpDir, "diskpulse.db")
	if err := s.store.ExportBackup(backupDB); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	dbFile, err := os.Open(backupDB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	dbEntry, err := zw.Create("diskpulse.db")
	if err != nil {
		_ = dbFile.Close()
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := io.Copy(dbEntry, dbFile); err != nil {
		_ = dbFile.Close()
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_ = dbFile.Close()

	cfgBytes, err := json.MarshalIndent(s.engine.Config(), "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cfgEntry, err := zw.Create("config.json")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := cfgEntry.Write(cfgBytes); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if err := zw.Close(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	filename := fmt.Sprintf("diskpulse-backup-%s.zip", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
