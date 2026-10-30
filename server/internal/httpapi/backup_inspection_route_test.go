package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestBackupInspectionRouteUsesBytesAndCreatesNoRestore(t *testing.T) {
	f := administrationFixtureNew(t)
	state := t.TempDir()
	service := backup.New(state, f.deps.DB, f.deps.Identity.ID(), "fixture", persistence.SchemaVersion())
	d := f.deps
	d.Backups = service
	handler := New(d)
	var file string
	if err := f.deps.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(t.TempDir(), "plain.pba")
	if err = os.WriteFile(selected, data, 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"source": map[string]string{"path": selected}})
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {f.member, 403}, {f.owner, 200}} {
		r := httptest.NewRequest("POST", "http://host/v1/admin/backups/inspect", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("inspection status=%d want=%d", w.Code, tc.status)
		}
		if tc.status == 200 && (!strings.Contains(w.Body.String(), `"encrypted":false`) || strings.Contains(w.Body.String(), selected)) {
			t.Fatal("inspection inferred protection from extension or disclosed the selected path")
		}
	}
	if progress := service.RestoreProgress(); progress != nil && progress.JobID != "" {
		t.Fatal("inspection started a restore")
	}
	if _, err = os.Stat(filepath.Join(state, backup.RestoreStagedDir)); !os.IsNotExist(err) {
		t.Fatal("inspection created staging", err)
	}
}
