package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestViewerMarkerRevisionMatchesProjectionAndTracksEdits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := viewerMarkerDB(t)
	a := viewerMarkerAccess(f.item)
	read := func() string {
		t.Helper()
		revision, err := ViewerMarkerRevision(context.Background(), f.db, a, "")
		if err != nil || revision == "" {
			t.Fatalf("marker revision: %q %v", revision, err)
		}
		set, err := ViewerMarkers(context.Background(), f.db, a, "")
		if err != nil || set.Revision != revision {
			t.Fatalf("revision %q does not match projection %+v: %v", revision, set, err)
		}
		return revision
	}
	before := read()
	if _, err := f.db.Exec(`UPDATE analysis_marker_sets SET revision=revision+1 WHERE object_id='object' AND source_revision='revision'`); err != nil {
		t.Fatal(err)
	}
	if after := read(); after == before {
		t.Fatal("marker edit left the viewer validator unchanged")
	}
	a.Authorize = func(*sql.Tx) error { return identity.ErrUnauthorized }
	if _, err := ViewerMarkerRevision(context.Background(), f.db, a, ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("revision bypassed authorization: %v", err)
	}
}
