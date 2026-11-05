package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestDispatchScriptThresholdsAndShowMore(t *testing.T) {
	for _, tt := range []struct {
		query string
		mode  search.ReadMode
	}{
		{"", search.ReadNothing}, {"  ", search.ReadNothing}, {"☆", search.ReadNothing},
		{"a", search.ReadTitles}, {"al", search.ReadTitles}, {" jo ", search.ReadTitles}, {"abc", search.ReadFull},
		{"東", search.ReadTitles}, {"東京", search.ReadFull}, {"王菲", search.ReadFull}, {"a東", search.ReadFull}, {"𠀀", search.ReadTitles},
		{"harbor ad", search.ReadFull}, {"123456789012345", search.ReadFull},
	} {
		q, e := search.PlanQuery(tt.query, false)
		if e != nil || q.Mode() != tt.mode {
			t.Fatalf("query%q: mode%d err%v", tt.query, q.Mode(), e)
		}
		more, e := search.PlanQuery(tt.query, true)
		if e != nil || tt.mode != search.ReadNothing && more.Mode() != search.ReadFull {
			t.Fatalf("explicit context%q", tt.query)
		}
	}
	q, e := search.PlanQuery("Harbor AD", false)
	if e != nil || !reflect.DeepEqual(q.Terms(), []string{"harbor", "ad"}) {
		t.Fatal("multiword terms must survive until exact population/lead choice")
	}
	if _, e = search.PlanQuery(strings.Repeat("ﬃ", 128), false); e != nil {
		t.Fatal("pinned384-scalar folded query rejected")
	}
	for _, s := range []string{strings.Repeat("a", 129), "\xff", "東\xed\xa0\x80"} {
		if _, e = search.PlanQuery(s, false); !errors.Is(e, search.ErrQuery) {
			t.Fatal("bad/overlong query accepted")
		}
	}
}

func TestReadPairingLogicalFieldOracle(t *testing.T) {
	var f struct {
		Query, Source string
		Token         string `json:"prefix_query_token"`
	}
	b, e := os.ReadFile("testdata/read-pairing.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	p, e := search.PrepareFragments(f.Source, search.DefaultFragmentBudget())
	if e != nil {
		t.Fatal(e)
	}
	key := search.FieldKey{Subject: search.ManifestEntity, SubjectID: 838, PublicationID: 17, BankID: 1, InputGeneration: 7, Field: 1, LogicalOrdinal: 0, Revision: "9"}
	m, e := search.NewCJKFieldMatcher(key, f.Query, 6144)
	if e != nil {
		t.Fatal(e)
	}
	candidates, paired, siblings := 0, 0, 0
	for i := 0; i < p.Len(); i++ {
		row := p.At(i)
		hit := strings.Contains(row.Normalized(), f.Query)
		if hit {
			siblings++
		}
		for _, token := range strings.Fields(row.PrefixTokens()) {
			if token == f.Token {
				candidates++
				if hit {
					paired++
				}
			}
		}
		if e = m.Observe(context.Background(), key, row.Normalized()); e != nil {
			t.Fatal(e)
		}
	}
	got, match, e := m.Result(context.Background())
	if e != nil || !match || got != key || candidates != 1 || paired != 0 || siblings != 1 {
		t.Fatalf("PB-RD-01 candidates%d row-local%d sibling%d logical%v err%v", candidates, paired, siblings, match, e)
	}
	// The row-local mutant observes no match, but the logical field emits
	// exactly one original source even when overlapping rows are repeated.
	for i := 0; i < p.Len(); i++ {
		if e = m.Observe(context.Background(), key, p.At(i).Normalized()); e != nil {
			t.Fatal(e)
		}
	}
	again, match, e := m.Result(context.Background())
	if e != nil || !match || again != key {
		t.Fatal("physical duplication changed the original logical identity")
	}
}

func TestReadPairingRejectsForeignSiblingAndCancellation(t *testing.T) {
	ctx := context.Background()
	key := search.FieldKey{Subject: search.ManifestEntity, SubjectID: 1, PublicationID: 3, BankID: 1, InputGeneration: 1, Field: 1, LogicalOrdinal: 0, Revision: "09"}
	for _, edit := range []func(*search.FieldKey){
		func(k *search.FieldKey) { k.Subject = search.ManifestPerson }, func(k *search.FieldKey) { k.SubjectID++ },
		func(k *search.FieldKey) { k.PublicationID++ }, func(k *search.FieldKey) { k.BankID++ }, func(k *search.FieldKey) { k.InputGeneration++ },
		func(k *search.FieldKey) { k.Field++ }, func(k *search.FieldKey) { k.LogicalOrdinal++ }, func(k *search.FieldKey) { k.Revision = "9" },
	} {
		m, e := search.NewCJKFieldMatcher(key, "辛哫", 6144)
		if e != nil {
			t.Fatal(e)
		}
		wrong := key
		edit(&wrong)
		if e = m.Observe(ctx, wrong, "辛哫"); !errors.Is(e, search.ErrFieldContinuation) {
			t.Fatal("foreign sibling rescued candidate")
		}
		if _, hit, e := m.Result(ctx); hit || !errors.Is(e, search.ErrFieldContinuation) {
			t.Fatal("failed field returned successful partial result")
		}
	}
	for _, term := range []string{"art", "辛 哫", ""} {
		if _, e := search.NewCJKFieldMatcher(key, term, 6144); !errors.Is(e, search.ErrQuery) {
			t.Fatal("wrong recipe admitted to CJK substring verifier")
		}
	}
	m, _ := search.NewCJKFieldMatcher(key, "辛哫", 6)
	if e := m.Observe(ctx, key, "辛哫辛"); !errors.Is(e, search.ErrFieldContinuation) {
		t.Fatal("oversized window accepted")
	}
	m, _ = search.NewCJKFieldMatcher(key, "辛哫", 6144)
	_ = m.Observe(ctx, key, "辛哫")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, hit, e := m.Result(canceled); hit || !errors.Is(e, context.Canceled) {
		t.Fatal("canceled result returned")
	}
	if _, hit, e := m.Result(ctx); hit || !errors.Is(e, search.ErrFieldContinuation) {
		t.Fatal("canceled reduction was resurrected with a fresh context")
	}
}

//go:noinline
func matcherWithBorrowedRevision() (*search.CJKFieldMatcher, error) {
	backing := strings.Repeat("9", 16<<20)
	key := search.FieldKey{Subject: search.ManifestPerson, SubjectID: 1, PublicationID: 3, BankID: 1, InputGeneration: 1, Field: 1, Revision: backing[:2]}
	return search.NewCJKFieldMatcher(key, "辛哫", 6144)
}

func TestReadPairingOwnsBoundedRevision(t *testing.T) {
	key := search.FieldKey{Subject: search.ManifestPerson, SubjectID: 1, PublicationID: 3, BankID: 1, InputGeneration: 1, Field: 1, Revision: "1234567"}
	if _, err := search.NewCJKFieldMatcher(key, "辛哫", 6); !errors.Is(err, search.ErrQuery) {
		t.Fatal("oversized retained revision accepted")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	m, err := matcherWithBorrowedRevision()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if retained := int64(after.HeapAlloc) - int64(before.HeapAlloc); retained > 1<<20 {
		t.Fatalf("short revision retained oversized backing storage: %d bytes", retained)
	}
	got, _, err := m.Result(context.Background())
	if err != nil || got.Revision != "99" {
		t.Fatal("owned revision changed")
	}
	runtime.KeepAlive(m)
}
