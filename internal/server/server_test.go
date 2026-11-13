package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"diskpulse/internal/app"
	"diskpulse/internal/config"
	"diskpulse/internal/model"
	"diskpulse/internal/notify"
	"diskpulse/internal/scanner"
	"diskpulse/internal/store"
)

func TestDiffAlertsAndStaticEndpoints(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	now := time.Now()
	const giB = int64(1024 * 1024 * 1024)
	_, err = st.SaveScanOutput(scanner.ScanOutput{
		Snapshot: model.Snapshot{
			DriveRoot:       `C:\`,
			CreatedAt:       now.Add(-48 * time.Hour),
			DriveTotalBytes: 200 * giB,
			DriveFreeBytes:  60 * giB,
			DriveUsedBytes:  140 * giB,
			ScannedBytes:    140 * giB,
			Status:          "completed",
		},
		Nodes: []model.DirNode{
			{Path: `C:\`, Name: `C:\`, Depth: 0, SizeBytes: 140 * giB},
			{Path: `C:\Downloads`, ParentPath: `C:\`, Name: `Downloads`, Depth: 1, SizeBytes: 10 * giB, Tag: "downloads-media"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = st.SaveScanOutput(scanner.ScanOutput{
		Snapshot: model.Snapshot{
			DriveRoot:       `C:\`,
			CreatedAt:       now,
			DriveTotalBytes: 200 * giB,
			DriveFreeBytes:  50 * giB,
			DriveUsedBytes:  150 * giB,
			ScannedBytes:    150 * giB,
			Status:          "completed",
		},
		Nodes: []model.DirNode{
			{Path: `C:\`, Name: `C:\`, Depth: 0, SizeBytes: 150 * giB},
			{Path: `C:\Downloads`, ParentPath: `C:\`, Name: `Downloads`, Depth: 1, SizeBytes: 20 * giB, Tag: "downloads-media"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SaveAlerts([]model.Alert{
		{CreatedAt: now, DriveRoot: `C:\`, RuleType: "sudden_spike", Severity: "warning", TargetPath: `C:\Downloads`, DeltaBytes: 10 * giB, Message: "Downloads +10 GB"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	eng := app.New(cfg, st, notify.NewDispatcher(nil))
	handler, err := NewHandler(eng, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Verify embedded dashboard static page serves properly.
	idxRec := httptest.NewRecorder()
	handler.ServeHTTP(idxRec, httptest.NewRequest(http.MethodGet, "/", nil))
	if idxRec.Code != http.StatusOK || !strings.Contains(idxRec.Body.String(), "DiskPulse") {
		t.Fatalf("GET / failed: status=%d", idxRec.Code)
	}

	// Verify status endpoint.
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/status failed: status=%d body=%s", statusRec.Code, statusRec.Body.String())
	}

	// Verify diff endpoint.
	diffRec := httptest.NewRecorder()
	handler.ServeHTTP(diffRec, httptest.NewRequest(http.MethodGet, "/api/diff?drive=C%3A%5C&preset=7d", nil))
	if diffRec.Code != http.StatusOK {
		t.Fatalf("GET /api/diff status=%d body=%s", diffRec.Code, diffRec.Body.String())
	}
	var diffPayload struct {
		NeedsMore bool             `json:"needs_more_snapshots"`
		Diff      model.DiffResult `json:"diff"`
	}
	if err := json.NewDecoder(diffRec.Body).Decode(&diffPayload); err != nil {
		t.Fatal(err)
	}
	if diffPayload.NeedsMore || len(diffPayload.Diff.RootCauses) != 1 || diffPayload.Diff.RootCauses[0].Path != `C:\Downloads` {
		t.Fatalf("unexpected diff payload: %+v", diffPayload)
	}

	// Verify acknowledge-all alerts endpoint.
	ackRec := httptest.NewRecorder()
	handler.ServeHTTP(ackRec, httptest.NewRequest(http.MethodPost, "/api/alerts/ack-all", nil))
	if ackRec.Code != http.StatusOK {
		t.Fatalf("POST /api/alerts/ack-all status=%d", ackRec.Code)
	}

	unread, err := st.ListAlerts(10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 0 {
		t.Errorf("expected 0 unread alerts after ack-all, got %d", len(unread))
	}

	// Verify backup download endpoint returns a valid zip containing diskpulse.db and config.json.
	bkRec := httptest.NewRecorder()
	handler.ServeHTTP(bkRec, httptest.NewRequest(http.MethodGet, "/api/backup/download", nil))
	if bkRec.Code != http.StatusOK {
		t.Fatalf("GET /api/backup/download status=%d body=%s", bkRec.Code, bkRec.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(bkRec.Body.Bytes()), int64(bkRec.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	found := map[string]bool{}
	for _, f := range zr.File {
		found[f.Name] = true
	}
	if !found["diskpulse.db"] || !found["config.json"] {
		t.Errorf("backup zip missing expected files: %+v", found)
	}
}
