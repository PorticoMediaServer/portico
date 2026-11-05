package search_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

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
		if err != nil || again.Digest() != plan.Digest() {
			t.Fatal("manifest is not deterministic")
		}
	}
}

func TestFragmentsEnforceBytesAndAmplifiedTokens(t *testing.T) {
	value := strings.Repeat("a b c d e f g h i j k l m n o p ", 180)
	for _, budget := range []search.FragmentBudget{{Bytes: 1100, TokenWork: 8192}, {Bytes: 6144, TokenWork: 1100}} {
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
		{strings.Repeat("abc ", 400), search.FragmentBudget{Bytes: 6144, TokenWork: 1}},
	} {
		plan, err := search.PrepareFragments(tt.value, tt.budget)
		if !errors.Is(err, search.ErrFragmentBudget) || plan.Len() != 0 || plan.Digest() != [32]byte{} {
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

func TestFragmentManifestDistinguishesImagesAndBoundaries(t *testing.T) {
	a, err := search.PrepareFragments("alpha beta", search.DefaultFragmentBudget())
	if err != nil {
		t.Fatal(err)
	}
	b, err := search.PrepareFragments("alphabeta", search.DefaultFragmentBudget())
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() == b.Digest() {
		t.Fatal("manifest did not bind column boundaries")
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
