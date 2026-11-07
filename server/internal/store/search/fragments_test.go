package search_test

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestFragmentsRefuseBeforeWholeFieldAllocation(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		reserve     bool
	}{
		{"invalid-unreserved", strings.Repeat("z", 4<<20), false},
		{"valid-unreserved", strings.Repeat("ok ", 1<<20), false},
		// Give the original reproducer its full finite forecast to prove the
		// token preflight, independently of the memory-admission rejection.
		{"invalid-reserved", strings.Repeat("z", 4<<20), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			budget := search.DefaultFragmentBudget()
			if tt.reserve {
				var err error
				budget.PreparationBytes, err = search.PreparationMemoryBytes(int64(len(tt.value)))
				if err != nil {
					t.Fatal(err)
				}
			}
			plan, err, allocated := prepareAllocated(tt.value, budget)
			if !errors.Is(err, search.ErrFragmentBudget) || plan.Len() != 0 {
				t.Fatalf("must refuse without a partial plan: len=%d err=%v", plan.Len(), err)
			}
			if errors.Is(err, search.ErrPreparationBudget) == tt.reserve {
				t.Fatalf("wrong refusal path: reserve=%v err=%v", tt.reserve, err)
			}
			limit := uint64(64 << 10)
			if tt.reserve {
				// The bounded 64 KiB SearchDocument preflight itself allocates
				// about 1.1 MB on this pinned engine-independent Go recipe.
				limit = 2 << 20
			}
			if allocated > limit {
				t.Fatalf("refusal allocated %d bytes (limit %d)", allocated, limit)
			}
			t.Logf("refusal allocated %d bytes", allocated)
		})
	}
}

func prepareAllocated(value string, budget search.FragmentBudget) (search.FragmentPlan, error, uint64) {
	// These tests are deliberately not parallel. Construct the source outside
	// this interval; measure cumulative Go allocation, not peak RSS or a hold.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	plan, err := search.PrepareFragments(value, budget)
	runtime.ReadMemStats(&after)
	return plan, err, after.TotalAlloc - before.TotalAlloc
}

func TestPreparationMemoryReservation(t *testing.T) {
	for _, n := range []int64{-1, 1<<63 - 1} {
		if _, err := search.PreparationMemoryBytes(n); !errors.Is(err, search.ErrPreparationBudget) {
			t.Fatalf("invalid/overflow input size %d accepted: %v", n, err)
		}
	}
	previous := int64(0)
	for _, n := range []int64{0, 1, 65535, 65536, 65537, 4 << 20} {
		need, err := search.PreparationMemoryBytes(n)
		if err != nil || need <= previous {
			t.Fatalf("reservation must grow past lexical clip: size=%d need=%d err=%v", n, need, err)
		}
		previous = need
	}
	// Full raw-source custody remains charged even though searchable normalized
	// text stops at 64KiB. The limit is not a cheaper preparation admission.
	var cjk strings.Builder
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&cjk, "%c山 ", rune(0x4e00+i))
	}
	for _, value := range []string{"", strings.Repeat("abcd ", 16000) + "omega", cjk.String()} {
		need, err := search.PreparationMemoryBytes(int64(len(value)))
		if err != nil {
			t.Fatal(err)
		}
		budget := search.DefaultFragmentBudget()
		budget.PreparationBytes = need - 1
		plan, err, allocated := prepareAllocated(value, budget)
		if !errors.Is(err, search.ErrPreparationBudget) || plan.Len() != 0 || allocated > 64<<10 {
			t.Fatalf("underreservation was not refused up front: err=%v allocated=%d", err, allocated)
		}
		budget.PreparationBytes = need
		plan, err, allocated = prepareAllocated(value, budget)
		if err != nil || uint64(need) < allocated {
			t.Fatalf("admitted preparation: err=%v allocated=%d reservation=%d", err, allocated, need)
		}
		if len(value) > 0 {
			end := 0
			for i := 0; i < plan.Len(); i++ {
				_, b := plan.At(i).NormalizedRange()
				end = max(end, b)
			}
			if end != utf8.RuneCountInString(facettext.SearchFold(value)) {
				t.Fatal("normalized streams use different field prefixes")
			}
		}
		t.Logf("source=%d allocation=%d reservation=%d", len(value), allocated, need)
	}
}

func TestFragmentsPreflightMinimumEmptyRow(t *testing.T) {
	// Large whitespace tails are folded too, so even an input with one token
	// must be preflighted before full-field work. Give each its full forecast.
	value := "abcdefghijklm " + strings.Repeat(" ", 4<<20)
	need, err := search.PreparationMemoryBytes(int64(len(value)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name        string
		bytes, work int
	}{
		{"lexical", 12, 8192},
		{"lexical", 6144, 51},
		{"prefix", 24, 8192},
	} {
		plan, err, allocated := prepareAllocated(value, search.FragmentBudget{Bytes: tt.bytes, TokenWork: tt.work, PreparationBytes: need})
		if !errors.Is(err, search.ErrFragmentBudget) || !strings.Contains(err.Error(), tt.name) || plan.Len() != 0 || allocated > 64<<10 {
			t.Fatalf("minimum row preflight: err=%v allocated=%d", err, allocated)
		}
	}
}

func TestFragmentsReconstructExactStreams(t *testing.T) {
	// Distinct terms prevent a deduplicated repeated-word fixture from hiding
	// truncation/reordering. Literal words define the independent oracle.
	var words []string
	for i := 0; i < 1800; i++ {
		words = append(words, fmt.Sprintf("word%06d", i))
	}
	for _, value := range []string{"", strings.Join(words, " "), strings.Repeat("東 京 𠀀 山 ", 1000)} {
		plan, err := search.PrepareFragments(value, search.DefaultFragmentBudget())
		if err != nil {
			t.Fatal(err)
		}
		var p, l []string
		fold := []rune(strings.TrimSpace(value))
		for i := 0; i < plan.Len(); i++ {
			f := plan.At(i)
			if f.Bytes() > 6144 || f.TokenWork() > 8192 || !utf8.ValidString(f.Normalized()) {
				t.Fatalf("unbounded fragment %d: bytes=%d work=%d", i, f.Bytes(), f.TokenWork())
			}
			p = append(p, strings.Fields(f.PrefixTokens())...)
			l = append(l, strings.Fields(f.LexicalTokens())...)
			start, end := f.NormalizedRange()
			if f.Normalized() != string(fold[start:end]) {
				t.Fatalf("wrong scalar range %d", i)
			}
		}
		expected := strings.Join(words, " ")
		if value == "" {
			expected = ""
		}
		if strings.HasPrefix(value, "東") {
			expected = "東 京 𠀀 山"
		}
		if strings.Join(p, " ") != scalarHex(expected) || strings.Join(l, " ") != expected {
			t.Fatal("complete prefix or lexical sequence changed")
		}
		again, err := search.PrepareFragments(value, search.DefaultFragmentBudget())
		if err != nil || !reflect.DeepEqual(again, plan) {
			t.Fatal("fragment plan is not deterministic")
		}
	}
}

func TestFragmentsEnforceBytesAndAmplifiedTokens(t *testing.T) {
	value := strings.Repeat("a b c d e f g h i j k l m n o p ", 180)
	for _, budget := range []search.FragmentBudget{{Bytes: 1100, TokenWork: 8192, PreparationBytes: 32 << 20}, {Bytes: 6144, TokenWork: 1100, PreparationBytes: 32 << 20}} {
		plan, err := search.PrepareFragments(value, budget)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < plan.Len(); i++ {
			f := plan.At(i)
			if f.Bytes() > budget.Bytes || f.TokenWork() > budget.TokenWork {
				t.Fatalf("budget not enforced at %d", i)
			}
		}
	}
	for _, tt := range []struct {
		value  string
		budget search.FragmentBudget
	}{
		{"a", search.FragmentBudget{}},
		{"a", search.FragmentBudget{Bytes: -1, TokenWork: 1}},
		{strings.Repeat("z", 1025), search.DefaultFragmentBudget()},
		{strings.Repeat("𠀀", 1100), search.DefaultFragmentBudget()},
		{strings.Repeat("abc ", 400), search.FragmentBudget{Bytes: 6144, TokenWork: 1, PreparationBytes: 32 << 20}},
	} {
		plan, err := search.PrepareFragments(tt.value, tt.budget)
		if !errors.Is(err, search.ErrFragmentBudget) || plan.Len() != 0 {
			t.Fatalf("must refuse without partial plan: len=%d err=%v", plan.Len(), err)
		}
	}
}

func TestFragmentsCrossBoundaryMembership(t *testing.T) {
	// Check every possible start for all accepted folded query widths through
	// 384 scalars (128 input scalars at the pinned fold's 3x expansion).
	value := strings.Repeat("abc def 𠀀山 東京 ", 180)
	plan, err := search.PrepareFragments(value, search.DefaultFragmentBudget())
	if err != nil {
		t.Fatal(err)
	}
	runes := []rune(strings.TrimSpace(value))
	for width := 1; width <= 384; width++ {
		for start := 0; start+width <= len(runes); start++ {
			covered := false
			for i := 0; i < plan.Len(); i++ {
				a, b := plan.At(i).NormalizedRange()
				if a <= start && b >= start+width {
					covered = true
					break
				}
			}
			if !covered {
				t.Fatalf("lost boundary phrase at %d width %d", start, width)
			}
		}
	}
}

func TestFragmentsChargeNativePrefixAmplification(t *testing.T) {
	plan, err := search.PrepareFragments("abcdefghijkl 1234567890", search.DefaultFragmentBudget())
	if err != nil {
		t.Fatal(err)
	}
	// ASCII prefix columns: (1+12)+(1+6); lexical upper bound: 4*(12+10);
	// normalized trigrams: 23-2. Raw word count (2) is not the work budget.
	if plan.Len() != 1 || plan.At(0).TokenWork() != 129 {
		t.Fatalf("native expansion was not charged: %+v", plan)
	}
}
