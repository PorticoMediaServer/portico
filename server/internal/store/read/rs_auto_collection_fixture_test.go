package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"testing"
	"time"
)

//go:embed rs_auto_collection_fixture_test.go
var rsAutoJournal string

func rsAutoHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsCollectionHash(t)+rsAutoJournal)))
}
func newRSAutoSource(t *testing.T) *browseFixture {
	f := newRSCollectionSource(t)
	f.exec(t, `UPDATE libraries SET metadata_language='en'`)
	f.exec(t, `UPDATE catalog_entities SET metadata_language='en'`)
	f.exec(t, `INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','fixture',1)`)
	f.exec(t, `INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(1,'tmdb',zeroblob(16),1,1,0,'','fixture','fill_missing')`)
	f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id) VALUES(1,'tmdb','C',1101)`)
	// Sources 1 and2 are accepted; source3 is fetched but not accepted.
	// Authored edges deliberately disagree with the automatic truth.
	for id := 1; id <= 3; id++ {
		ev := fmt.Sprintf("e%d", id)
		op := fmt.Sprintf("op%d", id)
		pub := fmt.Sprintf("p%d", id)
		f.exec(t, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(?,'tmdb','movie',?,'en','','detail',zeroblob(16),1)`, id, fmt.Sprint(id))
		f.exec(t, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,sealed) VALUES(?,?,'1','tmdb','movie',?,'en','','detail',printf('%064d',?), 'now','fixture',1)`, ev, id, fmt.Sprint(id), id)
		f.exec(t, `INSERT INTO metadata_evidence_collections(evidence_id,provider,evidence_kind,collection_kind,collection_id,name) VALUES(?,'tmdb','movie','collection','C','Collection')`, ev)
		f.exec(t, `INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,selected_id,manual,review_search,qualification,created_at,source_content_revision,command_incarnation) SELECT ?,id,'tmdb','accept','applied',1,'lease','later',metadata_incarnation,source_revision,identity_revision,metadata_revision,1,1,1,zeroblob(16),1,1,'fixture','fill_missing','','',0,'query','input',?,0,0,'fixture','now',1,zeroblob(16) FROM catalog_entities WHERE id=?`, op, fmt.Sprint(id), id)
		f.exec(t, `INSERT INTO metadata_publications(id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,selection_revision,generation,decision,observed_at) VALUES(?, ?,?,'tmdb','movie',?,?,1,1,'accepted','now')`, pub, op, id, fmt.Sprint(id), ev)
		f.exec(t, `INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,status,observed_at,accepted_evidence_id,accepted_publication_id) VALUES(?,'tmdb','primary',zeroblob(16),1,'movie',?,'matched','now',?,?)`, id, fmt.Sprint(id), ev, pub)
		f.exec(t, `INSERT INTO metadata_field_origins(entity_id,field,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,subject_kind,provider_id,evidence_id,publication_id) VALUES(?,'relations','tmdb','provider','fixture',1,'now','en','',1,'movie',?,?,?)`, id, fmt.Sprint(id), ev, pub)
	}
	f.exec(t, `UPDATE metadata_subject_providers SET accepted_evidence_id=NULL,accepted_publication_id=NULL WHERE entity_id=3`)
	f.exec(t, `INSERT INTO catalog_automatic_member_overrides(collection_id,item_id,present) VALUES(1101,2,0),(1101,4,1),(1101,1040,1)`)
	f.exec(t, `UPDATE catalog_entities SET automatic_collection_name=' Local ' WHERE id=5`)
	return f
}
func rsAutoConfig() governor.Config {
	c := readfixture.LiveConfig(time.Now)
	c.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}
	for i := range c.Partitions {
		c.Partitions[i][governor.MemoryBytes] = 128 << 20
	}
	return c
}
func newRSAutoFixture(t *testing.T, mutate func(*browseFixture)) (*browseFixture, *CollectionReader) {
	f := newRSAutoSource(t)
	f = composeReadFixture(t, f, append(browseDefinitions(), AutomaticCollectionDefinitions()...), rsAutoConfig(), true, rsAutoHash(t))
	if mutate != nil {
		mutate(f)
	}
	r, e := NewCollectionReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func rsBeginCollection(t *testing.T, f *browseFixture, r *CollectionReader) *CollectionPreparation {
	t.Helper()
	p, ref, e := r.BeginCollectionPreparation(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 1<<20, CollectionInput{View: f.input, Collection: 1101})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	t.Cleanup(func() {
		if e := p.Close(); e != nil {
			t.Error(e)
		}
	})
	return p
}
func rsCompleteCollection(t *testing.T, f *browseFixture, p *CollectionPreparation) {
	t.Helper()
	for n := 0; n < 2000; n++ {
		step, cost, e := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
		if e != nil {
			t.Fatalf("advance%d stage%d: %v %+v", n, p.cursor.stage, e, cost)
		}
		if step.RawPositions > MaxRaw {
			t.Fatal("unbounded raw window", step)
		}
		for _, w := range cost.LookupWindows {
			if w.Ended.Sub(w.Started) > time.Second {
				t.Fatal("long snapshot")
			}
		}
		if step.Ready {
			return
		}
	}
	t.Fatal("did not finish")
}
