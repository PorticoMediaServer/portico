package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type viewerMarkerFixtureState struct {
	db   *sql.DB
	item catalogtest.Item
}

func viewerMarkerDB(t *testing.T) viewerMarkerFixtureState {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("library", "Movies", "movie", "/fixture")
	item := c.Movie(library, "/fixture/movie.mp4", "Movie", 2024)
	c.Drain()
	if _, err := c.DB.Exec(`UPDATE library_sources SET incarnation='incarnation',generation=4 WHERE id='library'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('object','library',?,'incarnation','movie.mp4','revision','{}',1000,1,'available')`, item.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO analysis_marker_sets(object_id,source_revision,revision) VALUES('object','revision',7)`); err != nil {
		t.Fatal(err)
	}
	binding := token("incarnation", "4", "")
	for _, m := range []struct {
		id, kind   string
		start, end int64
		confidence float64
		provenance string
		approved   int
		deleted    int
	}{
		{"approved-intro", "intro", 5000000, 35000000, .3, "luminance_opening_boundary:v1", 1, 0},
		{"measured-credits", "credits", 100000000, 120000000, .9, "season_audio_match:credits:v1", 0, 0},
		{"picture-credits", "credits", 101000000, 120000000, .65, "visual_credits:v1", 0, 0},
		{"single-file-guess", "intro", 200000000, 230000000, .97, "sustained_end_title_luminance:v1", 0, 0},
		{"weak-commercial", "commercial", 40000000, 50000000, .25, "paired_black_boundaries_semantic_ambiguous:v1", 0, 0},
		{"titled-recap", "recap", 0, 4000000, .85, "embedded_chapter_label:v1", 0, 0},
		{"dismissed-outro", "outro", 60000000, 70000000, 1, "sustained_end_title_luminance:v1", 1, 1},
		{"owner-chapter", "chapter", 80000000, 90000000, 1, "owner:manual", 1, 0},
	} {
		if _, err := c.DB.Exec(`INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,approved,deleted) VALUES(?,'object','revision',?,?,?,?,?,?,'',?,?)`, m.id, binding, m.kind, m.start, m.end, m.confidence, m.provenance, m.approved, m.deleted); err != nil {
			t.Fatal(err)
		}
	}
	return viewerMarkerFixtureState{db: c.DB, item: item}
}

func viewerMarkerAccess(item catalogtest.Item) Access {
	return Access{LibraryID: "library", ItemID: item.Public, AccountID: "account", ProfileID: "profile", Authority: "local", Authorize: func(*sql.Tx) error { return nil }}
}

func TestViewerMarkersProjectSkipDecisionAndHideDismissedAndOwnerKinds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := viewerMarkerDB(t)
	set, err := ViewerMarkers(context.Background(), f.db, viewerMarkerAccess(f.item), "")
	if err != nil {
		t.Fatal(err)
	}
	if set.SourceID != f.item.Token || set.Revision == "" {
		t.Fatalf("marker set was not fenced to the played source: %+v", set)
	}
	safe := map[string]bool{}
	for _, m := range set.Markers {
		safe[m.ID] = m.AutomaticSafe
	}
	if len(set.Markers) != 4 {
		t.Fatalf("projected markers=%+v", set.Markers)
	}
	if _, ok := safe["picture-credits"]; ok {
		t.Fatal("a single-file guess overlapping a measurement of the same credits made a second button")
	}
	if _, ok := safe["dismissed-outro"]; ok {
		t.Fatal("a dismissed marker reached the viewer path")
	}
	if _, ok := safe["owner-chapter"]; ok {
		t.Fatal("an owner-only kind reached the viewer path")
	}
	if _, ok := safe["weak-commercial"]; ok {
		t.Fatal("an unreviewed scene guess reached the viewer")
	}
	for id, want := range map[string]bool{"approved-intro": true, "measured-credits": true, "titled-recap": false, "single-file-guess": false} {
		if got, ok := safe[id]; !ok || got != want {
			t.Fatalf("%s automaticSafe=%v present=%v want %v", id, got, ok, want)
		}
	}
	first := set.Markers[0]
	if first.ID != "titled-recap" || first.StartSeconds != 0 || first.EndSeconds != 4 {
		t.Fatalf("marker clock is not viewer seconds in order: %+v", set.Markers)
	}
}

func TestViewerMarkersStayHiddenWithoutLibraryAuthorization(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := viewerMarkerDB(t)
	denied := viewerMarkerAccess(f.item)
	denied.Authorize = func(*sql.Tx) error { return identity.ErrUnauthorized }
	if _, err := ViewerMarkers(context.Background(), f.db, denied, ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("unauthorized viewer read markers: %v", err)
	}
	missing := viewerMarkerAccess(f.item)
	missing.Authorize = nil
	if _, err := ViewerMarkers(context.Background(), f.db, missing, ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("markers projected without an authorization seam: %v", err)
	}
	moved := viewerMarkerAccess(f.item)
	moved.LibraryID = "other-library"
	if _, err := ViewerMarkers(context.Background(), f.db, moved, ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("markers projected for an item outside the authorized library: %v", err)
	}
}

func TestViewerMarkersFollowSourceSelectionAndClockRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := viewerMarkerDB(t)
	db := f.db
	set, err := ViewerMarkers(context.Background(), db, viewerMarkerAccess(f.item), "other-asset")
	if err != nil || len(set.Markers) != 0 || set.SourceID != "" {
		t.Fatalf("an unrelated source projected markers: %+v %v", set, err)
	}
	if _, err = db.Exec(`INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(?,?,'unknown_multi_episode')`, f.item.ID, f.item.Token); err != nil {
		t.Fatal(err)
	}
	set, err = ViewerMarkers(context.Background(), db, viewerMarkerAccess(f.item), f.item.Token)
	if err != nil || len(set.Markers) != 0 || set.SourceID != "" {
		t.Fatalf("a shared episode file without a boundary projected a guessed clock: %+v %v", set, err)
	}
	if _, err = db.Exec(`DELETE FROM episode_asset_boundaries WHERE item_id=? AND asset_id=?`, f.item.ID, f.item.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE library_sources SET generation=5 WHERE id='library'`); err != nil {
		t.Fatal(err)
	}
	set, err = ViewerMarkers(context.Background(), db, viewerMarkerAccess(f.item), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Markers) != 0 {
		t.Fatalf("markers survived a changed source binding: %+v", set.Markers)
	}
}

func TestViewerMarkersKeepOnlyOwnerMarkersWhenDetectionIsOff(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := viewerMarkerDB(t)
	before, err := ViewerMarkers(context.Background(), f.db, viewerMarkerAccess(f.item), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:library',1,'{"segmentDetection":"off"}',1)`); err != nil {
		t.Fatal(err)
	}
	set, err := ViewerMarkers(context.Background(), f.db, viewerMarkerAccess(f.item), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Markers) != 1 || set.Markers[0].ID != "approved-intro" {
		t.Fatalf("detections survived a library turning detection off: %+v", set.Markers)
	}
	if set.Revision == before.Revision {
		t.Fatal("the marker set kept its revision when detection was turned off")
	}
}
