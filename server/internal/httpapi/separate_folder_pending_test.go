package httpapi

// P2-59 / O-022 SeparateFolder over the real HTTP surface.
//
// The bounded command is reached through the actual administration route, the
// actual owner admission and the actual catalogue writer. Nothing here inserts
// a catalogue_entity_mutations row by hand: every pending or completed split is
// the result of a real POST to
//
//	POST /v1/admin/libraries/{id}/inventory/{object}/separate-folder
//
// A pending call must answer 409 request_in_progress with a two-second retry
// delay and must not publish an itemId. A call that reaches finalize must answer
// the separated id, and a retry must return that same id.
//
// The negative controls keep the new 409 branch narrow: a stale revision, a
// missing object, an unknown action and a caller below the administration tier
// must not be folded into honest backpressure and must not admit a command.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

type separateFolderHTTPFixture struct {
	administrationFixture
	c       *catalogtest.Catalog
	source  string
	album   int64
	objects []string
}

// newSeparateFolderHTTPFixture builds the reported shape: every track has one
// file in a separate folder and one elsewhere in the album. The command is the
// real one for the selected folder; the fixture adds no command rows.
func newSeparateFolderHTTPFixture(t *testing.T, files int) separateFolderHTTPFixture {
	t.Helper()
	f := separateFolderHTTPFixture{administrationFixture: administrationFixtureNew(t)}
	f.c = catalogtest.New(t, f.deps.DB)
	music := f.c.Library("music", "Music", "music", "/music")
	if err := f.deps.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='music'`).Scan(&f.source); err != nil {
		t.Fatal(err)
	}
	artist := f.c.Artist(music, "Artist")
	album := f.c.Album(artist, "Release", 2020)
	f.album = album.ID
	for n := 1; n <= files; n++ {
		song := f.c.Song(album, n, fmt.Sprintf("/music/flac/%02d.flac", n), fmt.Sprintf("Song %d", n))
		separateFolderInventoryObject(t, f.deps.DB, f.source, song.Token, fmt.Sprintf("flac/%02d.flac", n), "flac")
		_, token := f.c.File(song.ID, fmt.Sprintf("/music/mp3/%02d.mp3", n), 5400)
		f.objects = append(f.objects, separateFolderInventoryObject(t, f.deps.DB, f.source, token, fmt.Sprintf("mp3/%02d.mp3", n), "mp3"))
	}
	f.c.Drain()
	return f
}

// separateFolderInventoryObject mirrors the scanner's inventory row: the object
// revision and size/modified evidence must match the linked asset, because the
// accepted command rechecks them per turn.
func separateFolderInventoryObject(t *testing.T, db *sql.DB, source, token, relative, directory string) string {
	t.Helper()
	object := "obj-" + token
	if _, err := db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,directory_path)
 VALUES(?,?,?,(SELECT incarnation FROM library_sources WHERE id=?),?,'r1','{}',1000,1,?)`, object, source, token, source, relative, directory); err != nil {
		t.Fatalf("inventory object %s: %v", relative, err)
	}
	return object
}

func (f separateFolderHTTPFixture) separateFolder(object, revision string) *httptest.ResponseRecorder {
	path := "/v1/admin/libraries/music/inventory/" + url.PathEscape(object) + "/separate-folder"
	return f.call("POST", path, f.owner, `{"revision":"`+revision+`"}`)
}

func separateFolderErrorBody(t *testing.T, w *httptest.ResponseRecorder) struct {
	Code         string `json:"code"`
	RetryAfterMs int64  `json:"retryAfterMs"`
} {
	t.Helper()
	var envelope struct {
		Error struct {
			Code         string `json:"code"`
			RetryAfterMs int64  `json:"retryAfterMs"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope %q: %v", w.Body.String(), err)
	}
	return envelope.Error
}

func separateFolderCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSeparateFolderHTTPPendingAnswersRetryableInProgress drives a real split
// against a source that is offline. Admission itself does not require an online
// source: it admits the bounded command and creates the separated container.
// The next quantum meets the offline source and makes no write, so the call ends
// without finalize. That is the command's own SourcePaused state, not a test
// cap, and the route must answer honest backpressure rather than a split id.
func TestSeparateFolderHTTPPendingAnswersRetryableInProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.77s")
	}
	f := newSeparateFolderHTTPFixture(t, 2)
	f.c.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, f.source)

	w := f.separateFolder(f.objects[0], "r1")
	if w.Code != http.StatusConflict {
		t.Fatalf("pending split answered %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("pending split Retry-After %q", got)
	}
	body := separateFolderErrorBody(t, w)
	if body.Code != "request_in_progress" {
		t.Fatalf("pending split code %q: %s", body.Code, w.Body.String())
	}
	if body.RetryAfterMs != 2000 {
		t.Fatalf("pending split retryAfterMs %d", body.RetryAfterMs)
	}
	var document map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if _, published := document["itemId"]; published || strings.Contains(w.Body.String(), `"itemId"`) {
		t.Fatalf("pending split published an itemId: %s", w.Body.String())
	}

	// The command is really admitted and still active. This is the evidence
	// that the 409 describes an actual progressing split, not a fabricated row.
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE mode='separate' AND owner_kind='separate' AND status='active' AND parent_mutation_id IS NULL`); n != 1 {
		t.Fatalf("pending call left %d active separate commands", n)
	}
	// Admission committed the parent at "children" and no child quantum ran:
	// the offline pause is the first turn after admission, so the parent has
	// no current child and no earlier file moved.
	var phase string
	if err := f.deps.DB.QueryRow(`SELECT phase FROM catalog_entity_mutations WHERE mode='separate' AND status='active' AND parent_mutation_id IS NULL`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if phase != "children" {
		t.Fatalf("pending command paused at phase %q, want the admitted children phase", phase)
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE current_child IS NOT NULL`); n != 0 {
		t.Fatalf("pending call left %d current children", n)
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_albums`); n != 2 {
		t.Fatalf("pending call admitted %d albums, want the original and the separated container", n)
	}
	// No file moved. The separated container is still empty and the original
	// album kept every folder file.
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE s.album_id<>? AND a.path LIKE '/music/mp3/%'`, f.album); n != 0 {
		t.Fatalf("pending split moved %d folder files", n)
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, f.album); n != 2 {
		t.Fatalf("original album kept %d folder files, want 2", n)
	}
}

// TestSeparateFolderHTTPCompletesAndRepeatsTheSameID is the positive control
// for the pending answer: an online folder that fits in the natural foreground
// window really finishes over HTTP and publishes exactly one separated id.
func TestSeparateFolderHTTPCompletesAndRepeatsTheSameID(t *testing.T) {
	f := newSeparateFolderHTTPFixture(t, 2)

	w := f.separateFolder(f.objects[0], "r1")
	if w.Code != http.StatusOK {
		t.Fatalf("completed split answered %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		ItemID string `json:"itemId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.ItemID == "" {
		t.Fatalf("completed split published no id: %q %v", w.Body.String(), err)
	}
	separated := f.c.ID(body.ItemID)
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, separated); n != 2 {
		t.Fatalf("separated album holds %d folder files", n)
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE s.album_id=? AND a.path LIKE '/music/flac/%'`, f.album); n != 2 {
		t.Fatalf("original album kept %d non-folder files", n)
	}

	again := f.separateFolder(f.objects[0], "r1")
	if again.Code != http.StatusOK {
		t.Fatalf("retry answered %d: %s", again.Code, again.Body.String())
	}
	var repeated struct {
		ItemID string `json:"itemId"`
	}
	if err := json.Unmarshal(again.Body.Bytes(), &repeated); err != nil || repeated.ItemID != body.ItemID {
		t.Fatalf("retry published %q, first call %q: %v", repeated.ItemID, body.ItemID, err)
	}
}

// TestSeparateFolderHTTPKeepsRevisionAuthAndFaultAnswers holds the new 409 to
// the pending state only. Every case must refuse before admitting a command,
// which the zero separate-mutation count proves.
func TestSeparateFolderHTTPKeepsRevisionAuthAndFaultAnswers(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 3.08s")
	}
	t.Run("stale revision is not pending", func(t *testing.T) {
		f := newSeparateFolderHTTPFixture(t, 1)
		w := f.separateFolder(f.objects[0], "r2")
		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("stale revision answered %d: %s", w.Code, w.Body.String())
		}
		if code := separateFolderErrorBody(t, w).Code; code != "revision_mismatch" {
			t.Fatalf("stale revision code %q: %s", code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"itemId"`) {
			t.Fatalf("stale revision published an id: %s", w.Body.String())
		}
		if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE mode='separate'`); n != 0 {
			t.Fatalf("stale revision admitted %d commands", n)
		}
		if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_albums`); n != 1 {
			t.Fatalf("stale revision created %d albums", n)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		f := newSeparateFolderHTTPFixture(t, 1)
		path := "/v1/admin/libraries/music/inventory/" + url.PathEscape(f.objects[0]) + "/separate-folder"
		w := f.call("POST", path, "", `{"revision":"r1"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated split answered %d: %s", w.Code, w.Body.String())
		}
		if code := separateFolderErrorBody(t, w).Code; code != "unauthorized" {
			t.Fatalf("unauthenticated split code %q: %s", code, w.Body.String())
		}
		if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE mode='separate'`); n != 0 {
			t.Fatalf("unauthenticated split admitted %d commands", n)
		}
	})

	t.Run("member below the administration tier", func(t *testing.T) {
		f := newSeparateFolderHTTPFixture(t, 1)
		path := "/v1/admin/libraries/music/inventory/" + url.PathEscape(f.objects[0]) + "/separate-folder"
		w := f.call("POST", path, f.member, `{"revision":"r1"}`)
		if w.Code != http.StatusForbidden {
			t.Fatalf("member split answered %d: %s", w.Code, w.Body.String())
		}
		// This route uses the legacy failure() mapper, which keeps the shared
		// refusal's own code rather than the registry's not_permitted.
		if code := separateFolderErrorBody(t, w).Code; code != "forbidden" {
			t.Fatalf("member split code %q: %s", code, w.Body.String())
		}
		if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE mode='separate'`); n != 0 {
			t.Fatalf("member split admitted %d commands", n)
		}
	})

	t.Run("unknown object", func(t *testing.T) {
		f := newSeparateFolderHTTPFixture(t, 1)
		w := f.separateFolder("obj-no-such-object", "r1")
		if w.Code != http.StatusNotFound {
			t.Fatalf("unknown object answered %d: %s", w.Code, w.Body.String())
		}
		if code := separateFolderErrorBody(t, w).Code; code != "not_found" {
			t.Fatalf("unknown object code %q: %s", code, w.Body.String())
		}
		if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE mode='separate'`); n != 0 {
			t.Fatalf("unknown object admitted %d commands", n)
		}
	})

	t.Run("unknown action", func(t *testing.T) {
		f := newSeparateFolderHTTPFixture(t, 1)
		path := "/v1/admin/libraries/music/inventory/" + url.PathEscape(f.objects[0]) + "/not-an-action"
		w := f.call("POST", path, f.owner, `{"revision":"r1"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("unknown action answered %d: %s", w.Code, w.Body.String())
		}
		if code := separateFolderErrorBody(t, w).Code; code != "invalid_request" {
			t.Fatalf("unknown action code %q: %s", code, w.Body.String())
		}
		if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE mode='separate'`); n != 0 {
			t.Fatalf("unknown action admitted %d commands", n)
		}
	})
}

// Retry the original object after its link moved but before the command finalized.
func TestSeparateFolderHTTPRetriesAfterSelectedFileMoved(t *testing.T) {
	f := newSeparateFolderHTTPFixture(t, 12)
	first := f.separateFolder(f.objects[0], "r1")
	if first.Code != http.StatusConflict || separateFolderErrorBody(t, first).Code != "request_in_progress" {
		t.Fatalf("first response %d: %s", first.Code, first.Body.String())
	}
	var command string
	var target int64
	if err := f.deps.DB.QueryRow(`SELECT mutation_id,target_id FROM catalog_entity_mutations WHERE owner_kind='separate' AND parent_mutation_id IS NULL AND status='active'`).Scan(&command, &target); err != nil {
		t.Fatal(err)
	}
	var movedAlbum int64
	if err := f.deps.DB.QueryRow(`SELECT s.album_id FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE o.id=?`, f.objects[0]).Scan(&movedAlbum); err != nil {
		t.Fatal(err)
	}
	if movedAlbum == f.album {
		t.Fatal("selected file has not moved before retry")
	}
	// Pause an already progressing command, then retry through its real route.
	before := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE s.album_id=?`, target)
	f.c.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, f.source)
	paused := f.separateFolder(f.objects[0], "r1")
	if paused.Code != http.StatusConflict || separateFolderErrorBody(t, paused).Code != "request_in_progress" {
		t.Fatalf("paused retry: %d %s", paused.Code, paused.Body.String())
	}
	if after := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE s.album_id=?`, target); after != before {
		t.Fatalf("paused command moved links: %d -> %d", before, after)
	}
	f.c.Exec(`UPDATE library_sources SET health='ready' WHERE id=?`, f.source)
	var completed string
	for attempt := 0; attempt < 20; attempt++ {
		w := f.separateFolder(f.objects[0], "r1")
		if w.Code == http.StatusOK {
			var body struct {
				ItemID string `json:"itemId"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			completed = body.ItemID
			break
		}
		if w.Code != http.StatusConflict || separateFolderErrorBody(t, w).Code != "request_in_progress" {
			t.Fatalf("retry %d: %d %s", attempt, w.Code, w.Body.String())
		}
	}
	if completed == "" || f.c.ID(completed) != target {
		t.Fatalf("completion %q does not match admitted target %d", completed, target)
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='separate' AND parent_mutation_id IS NULL`); n != 1 {
		t.Fatalf("retry created %d commands", n)
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE mutation_id=? AND status='terminal' AND outcome='separated'`, command); n != 1 {
		t.Fatal("original command did not finalize")
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_albums`); n != 2 {
		t.Fatalf("retry created %d albums", n)
	}
}
