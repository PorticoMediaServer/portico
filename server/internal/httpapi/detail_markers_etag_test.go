package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func TestDetailETagTracksMarkerCreationEditAndDismissal(t *testing.T) {
	f := newV1Fixture(t, 1)
	c := f.catalogTest
	c.Exec(`UPDATE library_sources SET incarnation='incarnation',generation=4 WHERE id=?`, f.library)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('detail-object',?,?,'incarnation','film.mp4','revision','{}',10,1,'available')`, f.library, f.records[0].Token)
	c.Drain()
	var libraryRevision int64
	if err := f.db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id=?`, f.library).Scan(&libraryRevision); err != nil {
		t.Fatal(err)
	}
	path := "/v1/items/" + f.items[0] + "/detail"
	tag := ""
	read := func(wantMarkers int, endSeconds float64) {
		t.Helper()
		w := f.raw("GET", path, f.owner.AccessToken, map[string]string{"If-None-Match": tag}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("changed detail: %d %s", w.Code, w.Body.String())
		}
		next := w.Header().Get("ETag")
		if next == "" || next == tag {
			t.Fatalf("changed markers retained validator %q", next)
		}
		var detail catalog.Detail
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if len(detail.Markers) != wantMarkers || wantMarkers > 0 && detail.Markers[0].EndSeconds != endSeconds {
			t.Fatalf("stale detail markers: %+v", detail.Markers)
		}
		tag = next
		w = f.raw("GET", path, f.owner.AccessToken, map[string]string{"If-None-Match": tag}, nil)
		if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			t.Fatalf("unchanged detail: %d %s", w.Code, w.Body.String())
		}
	}
	read(0, 0)
	// This is the analysis transaction's own revision, with no catalogue or
	// viewer revision bump. The binding is token("incarnation", "4", "").
	binding := sha256.Sum256([]byte("11:incarnation1:40:"))
	c.Exec(`INSERT INTO analysis_marker_sets(object_id,source_revision,revision) VALUES('detail-object','revision',1)`)
	c.Exec(`INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,approved) VALUES('detail-intro','detail-object','revision',?,'intro',0,10000000,1,'owner:manual','',1)`, hex.EncodeToString(binding[:]))
	read(1, 10)
	c.Exec(`UPDATE analysis_markers SET end_us=12000000,edited=1 WHERE id='detail-intro'`)
	c.Exec(`UPDATE analysis_marker_sets SET revision=2 WHERE object_id='detail-object' AND source_revision='revision'`)
	read(1, 12)
	c.Exec(`UPDATE analysis_markers SET deleted=1,edited=1 WHERE id='detail-intro'`)
	c.Exec(`UPDATE analysis_marker_sets SET revision=3 WHERE object_id='detail-object' AND source_revision='revision'`)
	read(0, 0)
	var after int64
	if err := f.db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id=?`, f.library).Scan(&after); err != nil || after != libraryRevision {
		t.Fatalf("marker fixture changed the library revision: %d -> %d (%v)", libraryRevision, after, err)
	}
}
