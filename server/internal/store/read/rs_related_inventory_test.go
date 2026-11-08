package read

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

//go:embed rs_related_inventory_test.go
var rsRelatedInventoryJournal string

func rsRelatedInventoryHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(foregroundFixtureHash(t)+rsRelatedInventoryJournal)))
}

// rsRelatedInventorySource is the plan/fixture baseline: one current source,
// one accepted ready object and the foreground asset/link. Individual controls
// mutate only this owned state.
func rsRelatedInventorySource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health,generation)VALUES('source','movies','local','/media','/media','inc','ready',1)`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)VALUES('object','source','asset','inc','title.mkv','r','{}',1000,1,'available')`)
	return f
}

// rsRelatedInventoryQuery exposes exactly the family's registered definitions.
// It is a test-only local query adapter; production readers still need an
// actual Reader Capture and a bounded governor scope.
func rsRelatedInventoryQuery(t *testing.T, f *browseFixture) browseQuery {
	t.Helper()
	defs := map[stmt.ID]string{}
	for _, d := range RSRelatedInventoryDefinitions() {
		defs[d.ID] = d.SQL
	}
	return func(name string, args ...any) ([][]any, error) {
		id := readStatementID(name)
		sql, ok := defs[id]
		if !ok {
			return nil, fmt.Errorf("unregistered related inventory statement %s", id)
		}
		rows, err := f.conn.QueryContext(t.Context(), sql, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		var out [][]any
		for rows.Next() {
			row := make([]any, len(cols))
			scan := make([]any, len(cols))
			for i := range row {
				scan[i] = &row[i]
			}
			if err := rows.Scan(scan...); err != nil {
				return nil, err
			}
			out = append(out, row)
		}
		return out, rows.Err()
	}
}

func rsRelatedInventoryRun(t *testing.T, v *rsRelatedInventory, q browseQuery) rsRelatedInventoryVerdict {
	t.Helper()
	for n := 0; n < 4096; n++ {
		done, err := v.step(q, nil)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return v.Verdict()
		}
	}
	t.Fatal("related inventory reducer did not terminate")
	return rsRelatedInventoryUnresolved
}

func TestRSRelatedInventoryCanonicalAvailability(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
		want rsRelatedInventoryVerdict
	}{
		{"current-object-overrides-false-asset", `UPDATE catalog_assets SET available=0 WHERE id=1`, rsRelatedInventoryAvailable},
		{"asset-fallback", `DELETE FROM inventory_objects`, rsRelatedInventoryAvailable},
		{"asset-fallback-unavailable", `DELETE FROM inventory_objects; UPDATE catalog_assets SET available=0 WHERE id=1`, rsRelatedInventoryUnavailable},
		{"link-flag-is-not-truth", `UPDATE catalog_asset_links SET available=0 WHERE entity_id=1`, rsRelatedInventoryAvailable},
		{"stale-incarnation-forbids-fallback", `UPDATE inventory_objects SET root_incarnation='old'`, rsRelatedInventoryUnavailable},
		{"missing-state-forbids-fallback", `UPDATE inventory_objects SET state='missing'; UPDATE catalog_assets SET available=1`, rsRelatedInventoryUnavailable},
		{"unsupported-forbids-fallback", `UPDATE inventory_objects SET unsupported_reason='unsupported'`, rsRelatedInventoryUnavailable},
		{"retired-object-forbids-fallback", `UPDATE inventory_objects SET retired=1`, rsRelatedInventoryUnavailable},
		{"disabled-source", `DELETE FROM inventory_objects; UPDATE library_sources SET enabled=0`, rsRelatedInventoryUnavailable},
		{"root-changed-source", `DELETE FROM inventory_objects; UPDATE library_sources SET health='root_changed'`, rsRelatedInventoryUnavailable},
		{"removing-source", `DELETE FROM inventory_objects; UPDATE library_sources SET health='removing'`, rsRelatedInventoryUnavailable},
		{"unmatched-healthy-source", `DELETE FROM inventory_objects; UPDATE library_sources SET root='/elsewhere',configured_root='/elsewhere'`, rsRelatedInventoryAvailable},
		{"unmatched-disabled-source", `DELETE FROM inventory_objects; UPDATE library_sources SET root='/elsewhere',configured_root='/elsewhere',enabled=0`, rsRelatedInventoryUnavailable},
		{"no-sources", `DELETE FROM inventory_objects; DELETE FROM library_sources`, rsRelatedInventoryAvailable},
		{"no-sources-unavailable", `DELETE FROM inventory_objects; DELETE FROM library_sources; UPDATE catalog_assets SET available=0 WHERE id=1`, rsRelatedInventoryUnavailable},
		{"dvr-retirement", `INSERT INTO dvr_catalog_retirements(item_id,requested_ms)VALUES(1,1)`, rsRelatedInventoryUnavailable},
		{"accepted-publication-pending", `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=1`, rsRelatedInventoryPending},
		{"source-toggle-off-is-not-fallback", `UPDATE library_sources SET enabled=0`, rsRelatedInventoryUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := rsRelatedInventorySource(t)
			f.exec(t, tc.sql)
			q := rsRelatedInventoryQuery(t, f)
			if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != tc.want {
				t.Fatalf("verdict %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRSRelatedInventoryLinkPublicationPending(t *testing.T) {
	f := rsRelatedInventorySource(t)
	f.exec(t, `INSERT INTO catalog_info_changes(id,asset_id,asset_token,slot,expected_epoch,fingerprint,created_ms)VALUES(1,1,'asset',0,1,zeroblob(32),1)`)
	f.exec(t, `UPDATE catalog_asset_links SET info_pending_change=1 WHERE entity_id=1 AND asset_id=1`)
	q := rsRelatedInventoryQuery(t, f)
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryPending {
		t.Fatalf("link publication verdict %s", got)
	}
}

func TestRSRelatedInventoryAmbiguousLinks(t *testing.T) {
	f := rsRelatedInventorySource(t)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id)VALUES(2,'asset-2','/media/2.mkv',1000,1,'matroska','','',1,1,1,1)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(1,2,1)`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)VALUES('object-2','source','asset-2','inc','2.mkv','r','{}',1000,1,'missing')`)
	q := rsRelatedInventoryQuery(t, f)
	// Any current accepted link keeps the item available even when another
	// edition is stale; a stale edition alone is still a proven negative.
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryAvailable {
		t.Fatalf("mixed links %s", got)
	}
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE id='object'`)
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryUnavailable {
		t.Fatalf("stale edition admitted %s", got)
	}
	f.exec(t, `INSERT INTO catalog_info_changes(id,asset_id,asset_token,slot,expected_epoch,fingerprint,created_ms)VALUES(1,2,'asset-2',0,1,zeroblob(32),1)`)
	f.exec(t, `UPDATE catalog_asset_links SET info_pending_change=1 WHERE entity_id=1 AND asset_id=1`)
	f.exec(t, `DELETE FROM inventory_objects WHERE id='object-2'`)
	f.exec(t, `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=2`)
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryPending {
		t.Fatalf("all-pending links %s", got)
	}
}

func TestRSRelatedInventoryRetiredLatestChild(t *testing.T) {
	f := rsRelatedInventorySource(t)
	for id, asset := range map[int64]string{2: "asset-2", 3: "asset-3"} {
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id)VALUES(?,?,?,1000,1,'matroska','','',1,1,1,1)`, id, asset, fmt.Sprintf("/media/%d.mkv", id))
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(?,?,1)`, id, id)
	}
	// The newer child is retired; the older child remains current. A retired
	// current object must not fall back to its still-true asset flag.
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)VALUES('child-2','source','asset-2','inc','2.mkv','r','{}',1000,1,'available'),('child-3','source','asset-3','inc','3.mkv','r','{}',1000,1,'available')`)
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE id='child-3'`)
	q := rsRelatedInventoryQuery(t, f)
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(2, 1, "movies"), q); got != rsRelatedInventoryAvailable {
		t.Fatalf("older child %s", got)
	}
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(3, 1, "movies"), q); got != rsRelatedInventoryUnavailable {
		t.Fatalf("retired latest child %s", got)
	}
}

func TestRSRelatedInventoryAcceptedUnavailableReading(t *testing.T) {
	f := rsRelatedInventorySource(t)
	f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format)VALUES(1,1,'epub')`)
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=1`)
	q := rsRelatedInventoryQuery(t, f)
	// Accepted Reading metadata stays readable; only file availability changes.
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryAvailable {
		t.Fatalf("ready Reading %s", got)
	}
	f.exec(t, `UPDATE inventory_objects SET retired=1`)
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryUnavailable {
		t.Fatalf("accepted unavailable Reading %s", got)
	}
}

func TestRSRelatedInventoryStaleProof(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"source-root", `UPDATE library_sources SET root='/other'`},
		{"source-configured-root", `UPDATE library_sources SET configured_root='/other'`},
		{"source-incarnation", `UPDATE library_sources SET incarnation='other'`},
		{"source-generation", `UPDATE library_sources SET generation=2`},
		{"source-enabled", `UPDATE library_sources SET enabled=0`},
		{"source-set", `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health)VALUES('new','movies','local','/other','/other','inc','ready')`},
		{"asset-size", `UPDATE catalog_assets SET size=1001 WHERE id=1`},
		{"asset-mtime", `UPDATE catalog_assets SET modified_ns=2 WHERE id=1`},
		{"asset-path", `UPDATE catalog_assets SET path='/media/other.mkv' WHERE id=1`},
		{"asset-publication", `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=1`},
		{"object-state", `UPDATE inventory_objects SET state='missing'`},
		{"object-incarnation", `UPDATE inventory_objects SET root_incarnation='other'`},
		{"link-window", `DELETE FROM catalog_asset_links WHERE entity_id=1`},
		{"retirement", `INSERT INTO dvr_catalog_retirements(item_id,requested_ms)VALUES(1,1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := rsRelatedInventorySource(t)
			q := rsRelatedInventoryQuery(t, f)
			v := newRSRelatedInventory(1, 1, "movies")
			if got := rsRelatedInventoryRun(t, v, q); got != rsRelatedInventoryAvailable {
				t.Fatalf("initial %s", got)
			}
			f.exec(t, tc.sql)
			if err := v.verify(q); !errors.Is(err, ErrStale) {
				t.Fatalf("final proof accepted drift: %v", err)
			}
		})
	}
}

func TestRSRelatedInventoryVerifyUsesFinalRootProof(t *testing.T) {
	f := rsRelatedInventorySource(t)
	f.exec(t, `DELETE FROM inventory_objects`)
	q := rsRelatedInventoryQuery(t, f)
	v := newRSRelatedInventory(1, 1, "movies")
	if got := rsRelatedInventoryRun(t, v, q); got != rsRelatedInventoryAvailable {
		t.Fatalf("fallback %s", got)
	}
	if err := v.verify(q); err != nil {
		t.Fatalf("unchanged proof: %v", err)
	}
	f.exec(t, `UPDATE library_sources SET configured_root='/changed'`)
	if err := v.verify(q); !errors.Is(err, ErrStale) {
		t.Fatalf("source roots did not join final proof: %v", err)
	}
}

func TestRSRelatedInventoryCapacity(t *testing.T) {
	f := rsRelatedInventorySource(t)
	for n := 0; n < MaxRaw; n++ {
		f.exec(t, `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health)VALUES(?, 'movies','local',?,'/media','inc','ready')`, fmt.Sprintf("extra-%d", n), fmt.Sprintf("/media/%d", n+1))
	}
	q := rsRelatedInventoryQuery(t, f)
	v := newRSRelatedInventory(1, 1, "movies")
	for n := 0; n < 4096; n++ {
		done, err := v.step(q, nil)
		if errors.Is(err, ErrRelatedCapacity) {
			return
		}
		if err != nil || done {
			t.Fatalf("capacity not explicit: done=%v err=%v", done, err)
		}
	}
	t.Fatal("capacity refusal missing")
}

func TestRSRelatedInventoryOwnerPendingMarkers(t *testing.T) {
	for _, owner := range []string{rsRelatedOwnerIATypedChildren, rsRelatedOwnerMPTypedResume} {
		if got := rsRelatedInventoryOwnerPending(owner); got != rsRelatedInventoryPending {
			t.Fatalf("%s must stay pending, got %s", owner, got)
		}
	}
	if got := rsRelatedInventoryOwnerPending("other"); got != rsRelatedInventoryUnresolved {
		t.Fatalf("unknown owner %s", got)
	}
}

func TestRSRelatedInventoryNoSourceOpener(t *testing.T) {
	// Availability is proved from the same-snapshot catalogue only. The adapter
	// must never open, stat or execute a physical source path.
	source, err := os.ReadFile("rs_related_inventory.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"os.Open", "os.ReadFile", "os.Stat", "filepath.", "syscall.", "exec.Command", "net.Dial"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("source opener %q in related inventory", forbidden)
		}
	}
	f := rsRelatedInventorySource(t)
	f.exec(t, `UPDATE catalog_assets SET path='/media/a-file-that-does-not-exist.mkv' WHERE id=1`)
	f.exec(t, `DELETE FROM inventory_objects`)
	q := rsRelatedInventoryQuery(t, f)
	// The evidence is the exact source-root prefix from strings alone; no source
	// open or stat is attempted and no filesystem error can escape.
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryAvailable {
		t.Fatalf("path-only fallback %s", got)
	}
}
