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
	"unicode/utf8"

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

func TestDispatchRejectsRawPaddingBeforeWork(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input func() string
	}{
		{"all-ascii", func() string { return strings.Repeat(" ", 4<<20) }},
		{"leading-ascii", func() string { return strings.Repeat(" ", 4<<20) + "a" }},
		{"trailing-ideographic", func() string { return "a" + strings.Repeat("\u3000", 1<<20) }},
		{"mixed-unicode", func() string {
			padding := strings.Repeat(" \t\u00a0\u1680\u2000\u2003\u202f\u205f\u3000\n", 1<<17)
			return padding + "東" + padding
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input() // Build the hostile caller input outside admission.
			for _, more := range []bool{false, true} {
				allocs := testing.AllocsPerRun(10, func() {
					q, err := search.PlanQuery(input, more)
					if !errors.Is(err, search.ErrQuery) || q.Mode() != search.ReadNothing || q.Fold() != "" {
						t.Fatal("oversized raw padding accepted")
					}
				})
				if allocs != 0 {
					t.Fatalf("raw byte refusal allocated: %g", allocs)
				}
			}
		})
	}
	for _, input := range []string{strings.Repeat(" ", 511) + "a", strings.Repeat("𠀀", 128)} {
		if len(input) != 512 {
			t.Fatal("boundary fixture must be 512 bytes")
		}
		if _, err := search.PlanQuery(input, false); err != nil {
			t.Fatal("admitted raw byte boundary rejected")
		}
	}
	if _, err := search.PlanQuery(strings.Repeat(" ", 512)+"a", false); !errors.Is(err, search.ErrQuery) {
		t.Fatal("513 raw bytes accepted")
	}
}

func TestReadPairingAcceptsExpandedPlannerTerms(t *testing.T) {
	key := search.FieldKey{Subject: search.ManifestPerson, SubjectID: 1, PublicationID: 3, BankID: 1, InputGeneration: 1, Field: 1, Revision: "9"}
	// Literal witnesses cover case folding, explicit AE/OE transliteration,
	// Greek iota, Armenian and modifier-letter expansion, including 3x folds.
	for _, tt := range []struct{ raw, fold string }{
		{"ß", "ss"}, {"ẞ", "ss"},
		{"ﬀ", "ff"}, {"ﬁ", "fi"}, {"ﬂ", "fl"}, {"ﬃ", "ffi"}, {"ﬄ", "ffl"}, {"ﬅ", "st"}, {"ﬆ", "st"},
		{"Æ", "ae"}, {"æ", "ae"}, {"Œ", "oe"}, {"œ", "oe"},
		{"ᾈ", "αι"}, {"ῌ", "ηι"}, {"ῼ", "ωι"},
		{"և", "եւ"}, {"ﬓ", "մն"}, {"ﬔ", "մե"}, {"ﬕ", "մի"}, {"ﬖ", "վն"}, {"ﬗ", "մխ"},
		{"ŉ", "ʼn"},
		{strings.Repeat("ﬃ", 127), strings.Repeat("ffi", 127)},
	} {
		name := tt.raw
		if utf8.RuneCountInString(name) > 1 {
			name = "382-scalars"
		}
		t.Run(name, func(t *testing.T) {
			n := 127/utf8.RuneCountInString(tt.fold) + 1
			input, want := "東"+strings.Repeat(tt.raw, n), "東"+strings.Repeat(tt.fold, n)
			q, err := search.PlanQuery(input, true)
			if err != nil || !reflect.DeepEqual(q.Terms(), []string{want}) || utf8.RuneCountInString(want) <= 128 {
				t.Fatalf("planner expansion mismatch: %q, %v", q.Fold(), err)
			}
			m, err := search.NewCJKFieldMatcher(key, q.Terms()[0], 6144)
			if err != nil {
				t.Fatalf("admitted expanded term rejected: %v", err)
			}
			plan, err := search.PrepareFragments(input, search.DefaultFragmentBudget())
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < plan.Len(); i++ {
				if err := m.Observe(context.Background(), key, plan.At(i).Normalized()); err != nil {
					t.Fatal(err)
				}
			}
			if got, hit, err := m.Result(context.Background()); err != nil || !hit || got != key {
				t.Fatal("admitted expanded term missed matching prepared window")
			}
		})
	}
	if _, err := search.NewCJKFieldMatcher(key, "東"+strings.Repeat("a", 383), 6144); err != nil {
		t.Fatal("384-scalar folded envelope rejected")
	}
	for _, term := range []string{"東" + strings.Repeat("a", 384), strings.Repeat("𠀀", 385), "東\xff"} {
		if _, err := search.NewCJKFieldMatcher(key, term, 6144); !errors.Is(err, search.ErrQuery) {
			t.Fatal("over-budget or invalid folded term accepted")
		}
	}
}

func TestReadPairingCompleteWalkTrueNegative(t *testing.T) {
	key := search.FieldKey{Subject: search.ManifestPerson, SubjectID: 1, PublicationID: 3, BankID: 1, InputGeneration: 1, Field: 1, Revision: "9"}
	m, err := search.NewCJKFieldMatcher(key, "辛哫", 6144)
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"辛甲", "乙哫", "", "辛甲"} {
		if err := m.Observe(context.Background(), key, window); err != nil {
			t.Fatal(err)
		}
	}
	if got, hit, err := m.Result(context.Background()); err != nil || hit || got != key {
		t.Fatal("complete valid-key nonmatching sibling walk returned a hit/error")
	}
}

func TestReadPairingMidWalkCancellationPoisons(t *testing.T) {
	key := search.FieldKey{Subject: search.ManifestPerson, SubjectID: 1, PublicationID: 3, BankID: 1, InputGeneration: 1, Field: 1, Revision: "9"}
	for _, first := range []string{"甲乙", "辛哫"} {
		t.Run(first, func(t *testing.T) {
			m, err := search.NewCJKFieldMatcher(key, "辛哫", 6144)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Observe(context.Background(), key, first); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := m.Observe(ctx, key, "辛哫"); !errors.Is(err, context.Canceled) {
				t.Fatal("mid-walk canceled Observe did not return cancellation")
			}
			if _, hit, err := m.Result(context.Background()); hit || !errors.Is(err, search.ErrFieldContinuation) {
				t.Fatal("fresh-context result resurrected canceled sibling walk")
			}
			if err := m.Observe(context.Background(), key, "辛哫"); !errors.Is(err, search.ErrFieldContinuation) {
				t.Fatal("fresh-context observation resurrected canceled sibling walk")
			}
		})
	}
}
