package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var facetFixtureWitnessSequence atomic.Int64

type facetFixtureProducer struct {
	facts []FacetBaseFact
	fail  bool
}

func (p *facetFixtureProducer) Next(_ context.Context, _ *sql.Tx, _ FacetBaseSnapshot, cursor string) (FacetBaseBatch, error) {
	n := 0
	if cursor != "" {
		n, _ = strconv.Atoi(cursor)
	}
	end := n
	bytes := 0
	for end < len(p.facts) && end < n+8 {
		f := p.facts[end]
		width := len(f.Value) + len(f.Label)
		if end > n && bytes+width > 256<<10 {
			break
		}
		bytes += width
		end++
	}
	return FacetBaseBatch{Facts: p.facts[n:end], Cursor: strconv.Itoa(end), Done: end == len(p.facts)}, nil
}
func (p *facetFixtureProducer) ValidatePublication(context.Context, *sql.Tx, FacetBaseSnapshot) error {
	if p.fail {
		return errors.New("source moved")
	}
	return nil
}
func facetBaseFixture(t *testing.T) (*sql.DB, FacetBaseScope) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "facets.db")+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(facetBaseTestDDL); err != nil {
		t.Fatal(err)
	}
	scope := FacetBaseScope{LibraryID: 1, Library: "films", Field: "studio", Policy: FacetBasePolicy(identity.ContentRestrictions{}), Source: "fixture-exact-source-v1"}
	return db, scope
}
func facetBaseBuild(t *testing.T, db *sql.DB, scope FacetBaseScope, facts []FacetBaseFact) int64 {
	t.Helper()
	ctx := context.Background()
	p := &facetFixtureProducer{facts: facts}
	b := &FacetBases{DB: db, Producer: p, MaxBytes: 64 << 20}
	var id int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		var err error
		id, err = b.Begin(ctx, tx, scope, "source-image:"+fmt.Sprint(facetFixtureWitnessSequence.Add(1)), "authority:1")
		return err
	})
	for {
		done := false
		facetBaseTx(t, db, func(tx *sql.Tx) error { var err error; done, err = b.Step(ctx, tx, id); return err })
		if done {
			break
		}
	}
	return id
}
func facetBaseTx(t *testing.T, db *sql.DB, f func(*sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = f(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestFacetBaseTypedPolicy(t *testing.T) {
	p := func(r identity.ContentRestrictions) string { return FacetBasePolicy(r) }
	zero := 0
	pairs := [][2]identity.ContentRestrictions{
		{{}, {MaximumAge: &zero}},
		{{MemberDeniedLabels: []string{" x"}}, {MemberDeniedLabels: []string{"x"}}},
		{{BlockedLabels: []string{"a,b"}}, {BlockedLabels: []string{"a", "b"}}},
		{{MemberDeniedLabels: []string{""}}, {}},
		{{BlockedLabels: []string{"a|b"}}, {BlockedLabels: []string{"a", "b"}}},
	}
	for _, pair := range pairs {
		if p(pair[0]) == p(pair[1]) {
			t.Fatalf("policy collision: %#v", pair)
		}
	}
	a := identity.ContentRestrictions{MemberDeniedLabels: []string{" B", "a", "A"}}
	b := identity.ContentRestrictions{ProfileID: "other", Revision: 9, MemberDeniedLabels: []string{"a", " b"}}
	if p(a) != p(b) {
		t.Fatal("equivalent lowercased sets did not share")
	}
}
func TestFacetBasePublicationSharingAndRetention(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"Acme", "Old label", 7}, {"Beta", "Beta", 2}})
	var count int
	if err := db.QueryRow(`SELECT total FROM catalog_facet_bases WHERE id=?`, id).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	other := scope
	other.Policy = FacetBasePolicy(identity.ContentRestrictions{BlockUnrated: true})
	id2 := facetBaseBuild(t, db, other, []FacetBaseFact{{"Acme", "Old label", 3}})
	var values, texts int
	db.QueryRow(`SELECT count(*) FROM catalog_facet_base_values`).Scan(&values)
	db.QueryRow(`SELECT count(*) FROM catalog_facet_base_texts`).Scan(&texts)
	if values != 2 || texts != 2 {
		t.Fatal("not shared", values, texts)
	}
	id3 := facetBaseBuild(t, db, scope, []FacetBaseFact{{"Acme", "Renamed label", 8}})
	var old string
	if err := db.QueryRow(`SELECT x.label FROM catalog_facet_base_rows r JOIN catalog_facet_base_texts x ON x.id=r.text_id WHERE r.base_id=? ORDER BY r.value_id LIMIT 1`, id).Scan(&old); err != nil || old != "Old label" {
		t.Fatal(old, err)
	}
	if _, err := db.Exec(`DELETE FROM catalog_facet_base_rows WHERE base_id=?`, id); err == nil {
		t.Fatal("complete basis mutable")
	}
	b := &FacetBases{DB: db}
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		_, err := b.RetireStep(context.Background(), tx, id3)
		if !errors.Is(err, ErrFacetBaseInput) {
			t.Fatal(err)
		}
		return nil
	})
	for {
		done := false
		facetBaseTx(t, db, func(tx *sql.Tx) error {
			var err error
			done, err = b.RetireStep(context.Background(), tx, id)
			return err
		})
		if done {
			break
		}
	}
	if _, err := facetBaseSnapshot(context.Background(), db, id, true); !errors.Is(err, ErrFacetBaseUnavailable) {
		t.Fatal(err)
	}
	if _, err := facetBaseSnapshot(context.Background(), db, id2, true); err != nil {
		t.Fatal(err)
	}
}
func TestFacetBaseRejectsMissingProducerAndFailedProof(t *testing.T) {
	db, scope := facetBaseFixture(t)
	ctx := context.Background()
	tx, _ := db.Begin()
	defer tx.Rollback()
	if _, err := (&FacetBases{}).Begin(ctx, tx, scope, "image", "authority"); !errors.Is(err, ErrFacetBaseUnavailable) {
		t.Fatal(err)
	}
	p := &facetFixtureProducer{facts: []FacetBaseFact{{"v", "v", 1}}, fail: true}
	b := &FacetBases{DB: db, Producer: p, MaxBytes: 1 << 20}
	id, err := b.Begin(ctx, tx, scope, "image", "authority")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Step(ctx, tx, id); err == nil {
		t.Fatal("published without coherent source proof")
	}
	tx.Rollback()
	var n int
	db.QueryRow(`SELECT count(*) FROM catalog_facet_bases`).Scan(&n)
	if n != 0 {
		t.Fatal("failed producer leaked accepted state")
	}
}

func TestFacetBaseBoundedReclamationAndCapacity(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"a", "old", 1}})
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"b", "new", 2}})
	b := &FacetBases{DB: db}
	for {
		done := false
		facetBaseTx(t, db, func(tx *sql.Tx) error {
			var err error
			done, err = b.RetireStep(context.Background(), tx, id)
			return err
		})
		if done {
			break
		}
	}
	facetBaseTx(t, db, func(tx *sql.Tx) error { _, _, err := b.CollectTextStep(context.Background(), tx, 0); return err })
	facetBaseTx(t, db, func(tx *sql.Tx) error { _, _, err := b.CollectValueStep(context.Background(), tx, 0); return err })
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_facet_base_values`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	b = &FacetBases{DB: db, Producer: &facetFixtureProducer{}, MaxBytes: 1}
	if _, err := b.Begin(context.Background(), tx, scope, "other-image", "authority"); !errors.Is(err, ErrFacetBaseCapacity) {
		t.Fatal("metadata bypassed base budget", err)
	}
}

func TestFacetBaseEquivalentDemandSharesPendingAndCompleteBase(t *testing.T) {
	db, scope := facetBaseFixture(t)
	ctx := context.Background()
	b := &FacetBases{DB: db, Producer: &facetFixtureProducer{facts: []FacetBaseFact{{"a", "a", 1}}}, MaxBytes: 64 << 20}
	var first, again int64
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		var err error
		first, err = b.Begin(ctx, tx, scope, "same-image", "authority")
		return err
	})
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		var err error
		again, err = b.Begin(ctx, tx, scope, "same-image", "authority")
		return err
	})
	if again != first {
		t.Fatal("duplicated pending class image", first, again)
	}
	facetBaseTx(t, db, func(tx *sql.Tx) error { _, err := b.Step(ctx, tx, first); return err })
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		var err error
		again, err = b.Begin(ctx, tx, scope, "same-image", "authority")
		if again != first {
			t.Fatal("duplicated complete class image")
		}
		return err
	})
	facetBaseTx(t, db, func(tx *sql.Tx) error {
		done, err := b.Step(ctx, tx, again)
		if !done {
			t.Fatal("complete demand was rebuilt")
		}
		return err
	})
}
