package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// The source-health ruling: browsing keeps its retained file evidence, while an
// actual play and its pinned byte reads check the exact selected source and say
// "storage offline" only when everything else about the binding still holds.
func TestPlaybackV1OfflineStorageIsTypedAndRecovers(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	f.catalogTest.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'offline',s.id,a.token,s.incarnation,a.path,'r','{}',a.size,a.modified_ns FROM catalog_assets a JOIN library_sources s ON s.library_id=? WHERE a.token=? LIMIT 1`, f.library, f.records[0].Token)
	confirmPlaybackInventory(t, f, "offline")
	var source string
	if err := f.db.QueryRow(`SELECT source_id FROM inventory_objects WHERE id='offline'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	f.catalogTest.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, source)

	// Initial play acceptance names the exact state and leaves no session behind.
	w := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "offline-start-0001"}, startBody(f.items[0], nil))
	var refusal struct{ Error struct{ Code, Retry string } }
	if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	if w.Code != 503 || refusal.Error.Code != "storage_offline" || refusal.Error.Retry != "same_request" {
		t.Fatalf("offline acceptance: %d %s", w.Code, w.Body.String())
	}
	var sessions int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("offline refusal left %d sessions: %v", sessions, err)
	}

	// Reconnecting is immediate: the play is accepted with no other change.
	f.catalogTest.Exec(`UPDATE library_sources SET health='ready' WHERE id=?`, source)
	var started playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "offline-start-0002"}, startBody(f.items[0], nil), 201, &started)

	// A pinned byte read checks the selected source again, and recovers with it.
	f.catalogTest.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, source)
	w = f.raw("GET", started.Presentation.URL, "", map[string]string{"Range": "bytes=0-3"}, nil)
	if w.Code != 503 || v1Code(w) != "storage_offline" {
		t.Fatalf("offline byte read: %d %s", w.Code, w.Body.String())
	}
	f.catalogTest.Exec(`UPDATE library_sources SET health='ready' WHERE id=?`, source)
	w = f.raw("GET", started.Presentation.URL, "", map[string]string{"Range": "bytes=0-3"}, nil)
	if w.Code != 206 && w.Code != 200 {
		t.Fatalf("byte read after reconnect: %d %s", w.Code, w.Body.String())
	}
}

// A file with no inventory object is admitted through the catalogue's folder
// fallback, which keeps advertising it while the source is only offline. The
// play itself must still name that state, and recover without a rescan when the
// source reconnects; the pinned byte read checks the same association again.
func TestPlaybackV1NoInventoryOfflineSourceIsTypedAndRecovers(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	var objects int
	if err := f.db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE asset_id=?`, f.records[0].Token).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if objects != 0 {
		t.Fatalf("fixture inventoried the asset: %d objects", objects)
	}
	var source string
	if err := f.db.QueryRow(`SELECT id FROM library_sources WHERE library_id=?`, f.library).Scan(&source); err != nil {
		t.Fatal(err)
	}
	f.catalogTest.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, source)

	w := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "fallback-start-01"}, startBody(f.items[0], nil))
	var refusal struct{ Error struct{ Code, Retry string } }
	if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	if w.Code != 503 || refusal.Error.Code != "storage_offline" || refusal.Error.Retry != "same_request" {
		t.Fatalf("offline acceptance: %d %s", w.Code, w.Body.String())
	}
	var sessions int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("offline refusal left %d sessions: %v", sessions, err)
	}

	// Reconnecting is immediate: the same source-less play is accepted, with no
	// scan, inventory row or new acceptance needed.
	f.catalogTest.Exec(`UPDATE library_sources SET health='ready' WHERE id=?`, source)
	var started playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "fallback-start-02"}, startBody(f.items[0], nil), 201, &started)

	// The grant's pinned read checks the selected item's library again.
	f.catalogTest.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, source)
	w = f.raw("GET", started.Presentation.URL, "", map[string]string{"Range": "bytes=0-3"}, nil)
	if w.Code != 503 || v1Code(w) != "storage_offline" {
		t.Fatalf("offline byte read: %d %s", w.Code, w.Body.String())
	}
	f.catalogTest.Exec(`UPDATE library_sources SET health='ready' WHERE id=?`, source)
	w = f.raw("GET", started.Presentation.URL, "", map[string]string{"Range": "bytes=0-3"}, nil)
	if w.Code != 206 && w.Code != 200 {
		t.Fatalf("byte read after reconnect: %d %s", w.Code, w.Body.String())
	}
}
