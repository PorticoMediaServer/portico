package read

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// The complete collection domain is not activated for native search or detail
// until RS-05's automatic reducer lands. These tests pin the explicit refusal:
// an automatic identity, or a missing owned registry, must never become a ready
// empty collection. They also pin the accepted-publication recheck.

func TestRSNativeCollectionAutomaticRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*browseFixture)
	}{
		{"automatic-identity", func(f *browseFixture) {
			f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id) VALUES(1,'nfo','auto',1101)`)
		}},
		{"counts-only-auto-member", func(f *browseFixture) {
			// The accepted aggregate publication reports counts while the member
			// set stays inferred. RS-05 owns the reducer that could prove it.
			f.exec(t, `INSERT INTO catalog_aggregate_operations(id,library_id,source_kind,source_entity_id,source_incarnation,source_revision,phase) VALUES(1,1,'entity',1101,'i','r','complete')`)
			f.exec(t, `INSERT INTO catalog_aggregate_publications(id,operation_id,parent_id,captured_revision,generation,phase,duration_key,community_rating_key,critic_rating_key,file_size_key,episode_count,latest_aired_key,admission_member_count,admission_unrated_members,admission_min_known_age,admission_allow_age,admission_known_age) VALUES(1,1,1101,1,1,'published',0,0,0,0,0,'',3,0,3,-1,-1)`)
			f.exec(t, `UPDATE catalog_entities SET aggregate_publication_id=1,admission_member_count=3,admission_unrated_members=0,admission_min_known_age=3,admission_allow_age=-1,admission_known_age=-1 WHERE id=1101`)
			f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id) VALUES(1,'nfo','auto',1101)`)
		}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f, r := newRSCollectionCompleteFixture(t, tc.mutate)
			p := rsBeginCompleteCollection(t, f, r)
			var err error
			for step := 0; step < 8; step++ {
				var advanced CollectionAdvance
				advanced, _, err = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
				if err != nil {
					break
				}
				if advanced.Ready {
					t.Fatalf("unproved automatic collection became ready: %+v", advanced)
				}
			}
			if !errors.Is(err, ErrUnqualified) {
				t.Fatalf("automatic collection refusal = %v, want %v", err, ErrUnqualified)
			}
			if p.ready || p.nonempty || len(p.nodes) != 0 {
				t.Fatalf("automatic refusal retained invented membership ready=%v nonempty=%v nodes=%d", p.ready, p.nonempty, len(p.nodes))
			}
			detail, _, derr := rsAuthoredDetail(t, f, p, nil)
			if !errors.Is(derr, ErrInput) || !reflect.DeepEqual(detail, CollectionDetail{}) {
				t.Fatalf("automatic refusal delivered a page %+v (%v)", detail, derr)
			}
		})
	}
}

func TestRSNativeCollectionAcceptedPublicationDriftRefused(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, func(f *browseFixture) {
		f.exec(t, `INSERT INTO catalog_aggregate_operations(id,library_id,source_kind,source_entity_id,source_incarnation,source_revision,phase) VALUES(1,1,'entity',1101,'i','r','complete')`)
		f.exec(t, `INSERT INTO catalog_aggregate_publications(id,operation_id,parent_id,captured_revision,generation,phase,duration_key,community_rating_key,critic_rating_key,file_size_key,episode_count,latest_aired_key,admission_member_count,admission_unrated_members,admission_min_known_age,admission_allow_age,admission_known_age) VALUES(1,1,1101,1,1,'published',0,0,0,0,0,'',5,0,5,-1,-1)`)
		f.exec(t, `UPDATE catalog_entities SET aggregate_publication_id=1,admission_member_count=5,admission_unrated_members=0,admission_min_known_age=5,admission_allow_age=-1,admission_known_age=-1 WHERE id=1101`)
	})
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if detail, _, err := rsAuthoredDetail(t, f, p, nil); err != nil || detail.ExactCount != 179 {
		t.Fatalf("matching publication refused %+v %v", detail, err)
	}
	// A new preparation must recheck the accepted tuple in its own final window;
	// drift can never fall back to an empty successful detail.
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	f.exec(t, `UPDATE catalog_aggregate_publications SET admission_member_count=6 WHERE id=1`)
	detail, _, err := rsAuthoredDetail(t, f, p, nil)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(detail, CollectionDetail{}) {
		t.Fatalf("changed publication released values %+v %v", detail, err)
	}
}

func TestRSNativeCollectionMissingRegistryRefused(t *testing.T) {
	f := newRSCollectionCompleteSource(t)
	defs := append(browseDefinitions(), ArtworkDefinitions()...)
	f = composeReadFixture(t, f, defs, rsCollectionCompleteConfig(), true, rsCollectionHash(t))
	r, err := NewCollectionReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	p := rsBeginCompleteCollection(t, f, r)
	var advanced CollectionAdvance
	for step := 0; step < 8; step++ {
		advanced, _, err = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
		if err != nil {
			break
		}
		if advanced.Ready {
			t.Fatalf("missing collection registry became ready: %+v", advanced)
		}
	}
	if !errors.Is(err, sqliteconn.ErrPolicy) {
		t.Fatalf("missing registry refusal = %v, want %v", err, sqliteconn.ErrPolicy)
	}
	if p.ready || len(p.nodes) != 0 {
		t.Fatalf("missing registry invented membership ready=%v nodes=%d", p.ready, len(p.nodes))
	}
}

func TestRSNativeSearchDefinitionsKeepCollectionDomainClosed(t *testing.T) {
	defs := RSNativeSearchDefinitions()
	if len(defs) == 0 {
		t.Fatal("Reading definitions are missing from the native registry")
	}
	for _, def := range defs {
		id := string(def.ID)
		if !strings.HasPrefix(id, "read.browse.rs.reading.") {
			t.Fatalf("unproved RS domain definition %q entered the native registry", id)
		}
	}
}
