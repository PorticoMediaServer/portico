package httpapi

import (
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"strings"
	"testing"
)

func TestCreateLibraryFoldersAndLocalKinds(t *testing.T) {
	f := agentTestFixture(t)
	a, b := t.TempDir(), t.TempDir()
	var created struct {
		ID string `json:"id"`
	}
	response := f.call("POST", "/v1/libraries", nil, map[string]any{"name": "Two folders", "kind": "movie", "folders": []string{a, b}, "metadataAgent": "local"}, 201, &created)
	assertSpecResponse(t, "POST", "/v1/libraries", response)
	var sources, jobs int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM library_sources WHERE library_id=?`, created.ID).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE library_id=?`, created.ID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if sources != 2 || jobs != 2 {
		t.Fatalf("creation did not commit both folders/jobs: %d %d", sources, jobs)
	}
	var before, after int
	f.db.QueryRow(`SELECT COUNT(*) FROM libraries`).Scan(&before)
	for _, body := range []map[string]any{
		{"name": "Missing second", "kind": "movie", "folders": []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")}},
		{"name": "Two modes", "kind": "movie", "folders": []string{a}, "path": b},
		{"name": "Duplicate", "kind": "movie", "folders": []string{a, a}},
		{"name": "Online book", "kind": "book", "path": t.TempDir(), "metadataAgent": "online"},
	} {
		w := f.raw("POST", "/v1/libraries", f.owner.AccessToken, nil, body)
		if w.Code < 400 {
			t.Fatalf("invalid creation admitted: %d %s", w.Code, w.Body.String())
		}
	}
	f.db.QueryRow(`SELECT COUNT(*) FROM libraries`).Scan(&after)
	if before != after {
		t.Fatal("failed request left a library")
	}
	for _, kind := range []string{"audiobook", "book", "comic"} {
		f.call("POST", "/v1/libraries", nil, map[string]any{"name": kind, "kind": kind, "folders": []string{t.TempDir()}}, 201, &created)
		var agent metadata.LibraryAgent
		f.call("GET", "/v1/libraries/"+created.ID+"/metadata/agent", nil, nil, 200, &agent)
		if agent.Agent != "local" || len(agent.Agents) != 1 || agent.Agents[0].ID != "local" {
			t.Fatalf("online source exposed to local kind: %+v", agent)
		}
	}
	member, _ := f.member()
	w := f.raw("POST", "/v1/libraries", member, nil, map[string]any{"name": "Denied", "kind": "movie", "folders": []string{t.TempDir()}})
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("member creation: %d %s", w.Code, w.Body.String())
	}
	list := f.raw("GET", "/v1/libraries", f.owner.AccessToken, nil, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"summary"`) || strings.Contains(list.Body.String(), a) || strings.Contains(list.Body.String(), b) {
		t.Fatalf("summary path privacy: %d %s", list.Code, list.Body.String())
	}
	for _, path := range []string{a, b} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) > 0 {
			t.Fatal("creation modified media")
		}
	}
}

// MOB-111: scanAfter only controls whether creation queues the first scan.
// Omitting it and sending true keep the first scan; false creates the same
// library with nothing queued.
func TestCreateLibraryScanAfter(t *testing.T) {
	f := agentTestFixture(t)
	for _, probe := range []struct {
		name string
		body map[string]any
		want int
	}{
		{"omitted", map[string]any{"name": "Omitted", "kind": "movie", "folders": []string{t.TempDir()}, "metadataAgent": "local"}, 1},
		{"true", map[string]any{"name": "Explicit", "kind": "movie", "folders": []string{t.TempDir()}, "metadataAgent": "local", "scanAfter": true}, 1},
		{"false", map[string]any{"name": "Deferred", "kind": "movie", "folders": []string{t.TempDir()}, "metadataAgent": "local", "scanAfter": false}, 0},
	} {
		t.Run(probe.name, func(t *testing.T) {
			var created struct {
				ID string `json:"id"`
			}
			response := f.call("POST", "/v1/libraries", nil, probe.body, 201, &created)
			assertSpecResponse(t, "POST", "/v1/libraries", response)
			var sources, jobs int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM library_sources WHERE library_id=?`, created.ID).Scan(&sources); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE library_id=?`, created.ID).Scan(&jobs); err != nil {
				t.Fatal(err)
			}
			if sources != 1 || jobs != probe.want {
				t.Fatalf("creation committed %d sources and %d scans, want 1 and %d", sources, jobs, probe.want)
			}
		})
	}
}
