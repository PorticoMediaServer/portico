package read

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestRBHomeBinding(t *testing.T) {
	f := newRBHomeSource(t)
	f.exec(t, `UPDATE catalog_entities SET playback_origin_incarnation=x'01010101010101010101010101010101',playback_origin_revision=7 WHERE id=1`)
	f.exec(t, `UPDATE catalog_assets SET playback_origin_incarnation=x'02020202020202020202020202020202',playback_origin_revision=9,prepared_source_revision=13 WHERE id=1`)
	f.exec(t, `UPDATE catalog_asset_links SET playback_origin_incarnation=x'03030303030303030303030303030303',playback_origin_revision=11 WHERE entity_id=1`)
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,generation,identity_confirmed) VALUES('binding-source','movies','/media','/media','binding-inc',3,1)`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('binding-object','binding-source','home-1','binding-inc','home-1','object-rev','{}',1000,1)`)
	f.exec(t, `UPDATE libraries SET playback_origin_incarnation=x'04040404040404040404040404040404',playback_origin_revision=17 WHERE id='movies'`)
	defs := rbHomeBindingDefinitions()
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), true, rbBindingFixtureHash(t))
	read := func() (homeBindingResult, error) {
		var result homeBindingResult
		var bindingErr error
		_, e := f.snapshots.Read(t.Context(), homeRequest(), time.Second, func(scope *governor.ReadScope) error {
			q := func(name string, args ...any) ([][]any, error) { return scope.Query(readStatementID(name), args...) }
			result, bindingErr = readHomeWholePlayableBinding(q, 1, 1)
			return bindingErr
		})
		return result, e
	}
	value, e := read()
	if e != nil || value.Binding.DurationSeconds != 120 || value.Binding.ItemRevision != 7 || value.Binding.AssetRevision != 9 || value.Binding.LinkRevision != 11 || value.Binding.PreparedSourceRevision != 13 || value.Binding.SourceGeneration != 3 || value.Binding.ItemIncarnation[0] != 1 || value.Binding.AssetIncarnation[0] != 2 || value.Binding.LinkIncarnation[0] != 3 || value.Binding.Root.LibraryIncarnation[0] != 4 || value.Binding.Root.LibraryOriginRevision != 17 || value.Binding.Object != "binding-object" || !value.Binding.Root.RootsAgree || value.Progress.LinkRows != 1 || value.Progress.ObjectRows != 1 || !value.Progress.ObjectsExhausted {
		t.Fatal(value, e)
	}
	if value.Binding.PreparedSourceIncarnation != value.Binding.AssetIncarnation {
		t.Fatal("invented prepared namespace")
	}
	// No session/profile is consulted by this source proof.
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	if _, e := read(); e != nil {
		t.Fatal("source proof fabricated viewer authority", e)
	}
	refusal := func(sql, restore string) {
		t.Helper()
		f.exec(t, sql)
		r, e := read()
		if !errors.Is(e, errHomeBindingUnresolved) || !reflect.DeepEqual(r.Binding, homeWholePlayableBinding{}) {
			t.Fatal(sql, r, e)
		}
		f.exec(t, restore)
	}
	refusal(`UPDATE catalog_asset_links SET part_index=1 WHERE entity_id=1`, `UPDATE catalog_asset_links SET part_index=0 WHERE entity_id=1`)
	refusal(`UPDATE catalog_asset_links SET start_seconds=1 WHERE entity_id=1`, `UPDATE catalog_asset_links SET start_seconds=0 WHERE entity_id=1`)
	refusal(`UPDATE catalog_asset_links SET end_seconds=100 WHERE entity_id=1`, `UPDATE catalog_asset_links SET end_seconds=NULL WHERE entity_id=1`)
	refusal(`UPDATE catalog_assets SET duration=604801 WHERE id=1`, `UPDATE catalog_assets SET duration=120 WHERE id=1`)
	refusal(`UPDATE catalog_assets SET duration=1e999 WHERE id=1`, `UPDATE catalog_assets SET duration=120 WHERE id=1`)
	refusal(`UPDATE catalog_assets SET info_publication_pending=1 WHERE id=1`, `UPDATE catalog_assets SET info_publication_pending=0 WHERE id=1`)
	refusal(`UPDATE library_sources SET library_id='other' WHERE id='binding-source'`, `UPDATE library_sources SET library_id='movies' WHERE id='binding-source'`)
	refusal(`UPDATE library_sources SET health='removing' WHERE id='binding-source'`, `UPDATE library_sources SET health='ready' WHERE id='binding-source'`)
	refusal(`UPDATE inventory_objects SET retired=1 WHERE id='binding-object'`, `UPDATE inventory_objects SET retired=0 WHERE id='binding-object'`)
	refusal(`UPDATE library_sources SET enabled=0 WHERE id='binding-source'`, `UPDATE library_sources SET enabled=1 WHERE id='binding-source'`)
	refusal(`UPDATE library_sources SET health='root_changed' WHERE id='binding-source'`, `UPDATE library_sources SET health='ready' WHERE id='binding-source'`)
	refusal(`UPDATE inventory_objects SET root_incarnation='old' WHERE id='binding-object'`, `UPDATE inventory_objects SET root_incarnation='binding-inc' WHERE id='binding-object'`)
	refusal(`UPDATE catalog_libraries SET root='/other' WHERE id=1`, `UPDATE catalog_libraries SET root='/media' WHERE id=1`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,2,1)`)
	r, e := read()
	if !errors.Is(e, errHomeBindingUnresolved) || r.Progress.LinkRows != 2 || r.Binding.Asset != 0 {
		t.Fatal("ambiguous links", r, e)
	}
	f.exec(t, `DELETE FROM catalog_asset_links WHERE entity_id=1 AND asset_id=2`)
	f.exec(t, `INSERT INTO dvr_catalog_provenance(item_id,recording_id,asset_id,source_id,channel_id,programme_id,guide_generation,artifact_digest,captured_start_ms,captured_end_ms,state) VALUES(1,'binding-recording','home-1','live','channel','programme','guide','digest',0,1,'completed')`)
	r, e = read()
	if !errors.Is(e, errHomeBindingUnresolved) || r.Binding.Asset != 0 {
		t.Fatal("DVR policy omitted", r, e)
	}
	f.exec(t, `DELETE FROM dvr_catalog_provenance WHERE item_id=1`)
	for n := 0; n < 64; n++ {
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,retired) VALUES(?,'binding-source','home-1','binding-inc',?,'1','{}',1000,1,1)`, fmt.Sprintf("a-%03d", n), fmt.Sprintf("old-%03d", n))
	}
	r, e = read()
	if !errors.Is(e, errHomeBindingBudget) || r.Progress.ObjectRows != 64 || r.Progress.ObjectsExhausted || r.Progress.LastObject != "a-063" || r.Binding.Asset != 0 {
		t.Fatal("raw budget lost progress", r, e)
	}
	// Both native statistics profiles authorize these exact inert statements.
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, defs, stats, rbBindingFixtureHash(t))
	}

}

func rbBindingFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_home_binding_test.go")
	if e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(rbHomeFixtureHash(t)), data...)))
}
