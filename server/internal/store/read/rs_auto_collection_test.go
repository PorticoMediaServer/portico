package read

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestRSAutoCollectionAcceptedSet(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	p := rsBeginCollection(t, f, r)
	rsCompleteCollection(t, f, p)
	var got []int64
	for n := 0; n < len(p.nodes); n++ {
		got = append(got, rsNodeRank(p.nodes, p.root, n))
	}
	if !slices.Equal(got, []int64{1, 4}) {
		t.Fatal("wrong effective membership", got)
	}
	d, _, e := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
	if e != nil || d.ExactCount != 2 || d.Origin != "automatic" || len(d.Items) != 2 || !p.closed {
		t.Fatal(d, e)
	}
}
func TestRSAutoCollectionCurrentSources(t *testing.T) {
	for _, tt := range []struct {
		name, sql string
		want      []int64
	}{
		{"provider-disabled", `UPDATE metadata_providers SET enabled=0`, []int64{4}},
		{"disabled", `UPDATE metadata_provider_policy SET enabled=0`, []int64{4}},
		{"policy-incarnation", `UPDATE metadata_provider_policy SET incarnation=x'01000000000000000000000000000000'`, []int64{4}},
		{"policy-revision", `UPDATE metadata_provider_policy SET revision=2`, []int64{4}},
		{"unsealed", `UPDATE metadata_evidence SET sealed=0`, []int64{4}},
		{"language", `UPDATE libraries SET metadata_language='fr'`, []int64{4}},
		{"head", `UPDATE metadata_subject_providers SET accepted_evidence_id=NULL,accepted_publication_id=NULL WHERE entity_id=1`, []int64{4}},
		{"unmatched", `UPDATE metadata_subject_providers SET status='selected' WHERE entity_id=1`, []int64{4}},
		{"secondary", `UPDATE metadata_subject_providers SET role='secondary' WHERE entity_id=1`, []int64{4}},
		{"origin", `UPDATE metadata_field_origins SET field='description' WHERE entity_id=1`, []int64{4}},
		{"unapplied", `UPDATE metadata_provider_operations SET status='ready'`, []int64{4}},
		{"local-suppresses", `UPDATE catalog_entities SET automatic_collection_name=' Local ' WHERE id=1`, []int64{4}},
		{"empty-local-fallback", `UPDATE catalog_entities SET automatic_collection_name='  ' WHERE id=1`, []int64{1, 4}},
		{"explicit-add-disjoint", `UPDATE catalog_automatic_member_overrides SET present=1 WHERE item_id=2`, []int64{1, 2, 4}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, r := newRSAutoFixture(t, func(f *browseFixture) { f.exec(t, tt.sql) })
			p := rsBeginCollection(t, f, r)
			rsCompleteCollection(t, f, p)
			var got []int64
			for i := range p.nodes {
				got = append(got, rsNodeRank(p.nodes, p.root, i))
			}
			if !slices.Equal(got, tt.want) {
				t.Fatal(got, tt.want)
			}
		})
	}
}
func TestRSAutoCollectionPendingStaleAndCapacity(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	p := rsBeginCollection(t, f, r)
	step, _, e := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if e != nil || step.Ready {
		t.Fatal(step, e)
	}
	d, _, e := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatal("pending became exact", d, e)
	}
	f.exec(t, `UPDATE catalog_entities SET collection_revision=collection_revision+1 WHERE id=1101`)
	step, _, e = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if !errors.Is(e, ErrStale) || step.Ready || !p.closed || p.nodes != nil {
		t.Fatal("stale retained", e)
	}
	p = rsBeginCollection(t, f, r)
	p.nodes = p.nodes[:0:0]
	_, _, e = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 100; n++ {
		_, _, e = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
		if e != nil {
			break
		}
	}
	if !errors.Is(e, governor.ErrSnapshotBudget) || !p.closed {
		t.Fatal("capacity truncated successfully", e)
	}
}
func TestRSCollectionSetExactRanks(t *testing.T) {
	nodes := make([]rsCollectionNode, 0, 1001)
	var root int32
	for n := 1; n <= 1000; n++ {
		id := int64(n * 571 % 1001)
		root = rsNodeInsert(&nodes, root, id, id)
	}
	for n := 1; n <= 1000; n++ {
		root = rsNodeInsert(&nodes, root, int64(n), int64(n))
	}
	if len(nodes) != 1000 {
		t.Fatal("duplicate inserted", len(nodes))
	}
	for n := 0; n < 1000; n++ {
		if rsNodeRank(nodes, root, n) != int64(n+1) {
			t.Fatal("wrong rank", n)
		}
	}
	if nodes[root-1].height > 16 {
		t.Fatal("unbounded tree depth")
	}
}

func TestRSAutoCollectionRejectedTailAndIndependentOracle(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	f.exec(t, `INSERT INTO metadata_field_origins(subject_kind,provider_id,entity_id,field,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,publication_id,evidence_id) SELECT 'movie','3',id,'relations','tmdb','fixture','fixture',1,'now','en','',1,NULL,'e3' FROM catalog_entities WHERE id BETWEEN 6 AND 80`)
	p := rsBeginCollection(t, f, r)
	raw, quanta := 0, 0
	for ; quanta < 100; quanta++ {
		step, _, e := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
		if e != nil {
			t.Fatal(e)
		}
		if step.RawPositions > 4 {
			t.Fatal("unbounded raw source positions")
		}
		raw += step.RawPositions
		if step.Ready {
			break
		}
	}
	if quanta < 20 || quanta >= 100 || raw < 80 {
		t.Fatal("rejected tail did not advance", quanta, raw)
	}
	// The compound oracle is test-only; production definitions use base tables.
	rows, e := f.conn.QueryContext(t.Context(), `SELECT item_id FROM catalog_automatic_members_current WHERE parent_id=1101 ORDER BY item_id`)
	if e != nil {
		t.Fatal(e)
	}
	var want []int64
	for rows.Next() {
		var id int64
		if e := rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		want = append(want, id)
	}
	if e := rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	var got []int64
	for i := range p.nodes {
		got = append(got, rsNodeRank(p.nodes, p.root, i))
	}
	if !slices.Equal(got, want) {
		t.Fatal("source recipe differs from independent oracle", got, want)
	}
}
