package workload

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// FullSearchClass names what a query is expected to do on the canonical
// population. Report latency per class: a broad term is by design a bounded
// partial preview, never an exact population, and must not be averaged with
// exact rare lookups.
type FullSearchClass uint8

const (
	// FullSearchRare names one entity through its unique ordinal token (exact).
	FullSearchRare FullSearchClass = iota + 1
	// FullSearchConjunction pairs a phrase word with an ordinal: 0 or 1 hit.
	FullSearchConjunction
	// FullSearchBroad matches far more than the 2,000-candidate preview budget.
	FullSearchBroad
	// FullSearchNone matches nothing.
	FullSearchNone
)

func (c FullSearchClass) String() string {
	switch c {
	case FullSearchRare:
		return "rare"
	case FullSearchConjunction:
		return "conjunction"
	case FullSearchBroad:
		return "broad"
	case FullSearchNone:
		return "none"
	}
	return "unknown"
}

// FullSearchTerm is one query of the mix.
type FullSearchTerm struct {
	Class FullSearchClass
	Text  string
	Query search.Query
}

// broadWords are phrase words of the canonical movie title generator
// ("The Amber River", "A Quiet Orbit", ...). At one million residents each of
// these is a six-figure population, so the 2,000-candidate budget always ends
// the walk. "etoile" needs accent fold. The generator's explicit "Archive"
// cohort is a SORT title, not a catalog title, so it is deliberately not a term.
var broadWords = []string{"the", "amber", "river", "harbour", "lanterns", "etoile"}

const (
	fullSearchNoneText = "zzqxvw"
	// A preview retains at most this many expected IDs; the exact classes never
	// exceed it, and a larger population is checked by count and soundness.
	fullSearchOracleIDs = 64
)

// FullSearchOracle is the independent exact check for the mix. Feed it every
// catalog (id, title) pair once, outside any timed window, then Check pages.
// Matching is the logical rule the native read verifies: every folded query
// term occurs in the folded title. It reads no bank, FTS index or plan, so a
// missing or mis-built bank shows up as a disagreement, not as agreement.
type FullSearchOracle struct {
	Terms  []FullSearchTerm
	folded [][]string
	counts []int64
	ids    [][]int64
}

// NewFullSearchOracle builds the mix. sampleTitles are the titles of a handful
// of entities chosen by the caller (for example by deterministic ordinals read
// by point query): each contributes one rare and one conjunction query derived
// from its own digit token, so the terms follow the population actually built.
func NewFullSearchOracle(sampleTitles []string) (*FullSearchOracle, error) {
	var texts []struct {
		class FullSearchClass
		text  string
	}
	for _, title := range sampleTitles {
		token := ""
		for _, f := range strings.Fields(title) {
			if len(f) >= 6 && strings.Trim(f, "0123456789") == "" {
				token = f
			}
		}
		fields := strings.Fields(title)
		if token == "" || len(fields) < 2 {
			return nil, fmt.Errorf("sample title %q has no ordinal token", title)
		}
		texts = append(texts, struct {
			class FullSearchClass
			text  string
		}{FullSearchRare, token}, struct {
			class FullSearchClass
			text  string
		}{FullSearchConjunction, fields[0] + " " + token})
		// A word of a different phrase makes a deliberate zero-hit conjunction.
		other := "orbit"
		if strings.Contains(strings.ToLower(title), other) {
			other = "river"
		}
		texts = append(texts, struct {
			class FullSearchClass
			text  string
		}{FullSearchConjunction, other + " " + token})
	}
	for _, w := range broadWords {
		texts = append(texts, struct {
			class FullSearchClass
			text  string
		}{FullSearchBroad, w})
	}
	texts = append(texts, struct {
		class FullSearchClass
		text  string
	}{FullSearchNone, fullSearchNoneText})
	o := &FullSearchOracle{}
	for _, t := range texts {
		q, err := search.PlanQuery(t.text, false)
		if err != nil {
			return nil, err
		}
		if q.Mode() != search.ReadFull {
			return nil, fmt.Errorf("query %q is not a full-mode query", t.text)
		}
		o.Terms = append(o.Terms, FullSearchTerm{Class: t.class, Text: t.text, Query: q})
		o.folded = append(o.folded, q.Terms())
	}
	o.counts = make([]int64, len(o.Terms))
	o.ids = make([][]int64, len(o.Terms))
	return o, nil
}

func titleMatches(foldedTitle string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(foldedTitle, t) {
			return false
		}
	}
	return true
}

// Observe records one catalog entity. Call it once per entity.
func (o *FullSearchOracle) Observe(id int64, title string) {
	fold := search.PrepareTitle(title).Fold()
	for i, terms := range o.folded {
		if titleMatches(fold, terms) {
			o.counts[i]++
			if len(o.ids[i]) < fullSearchOracleIDs {
				o.ids[i] = append(o.ids[i], id)
			}
		}
	}
}

// Expected is the true number of matching entities after Observe.
func (o *FullSearchOracle) Expected(i int) int64 { return o.counts[i] }

// Validate reports a mix that does not exercise its classes on the observed
// population (for example a "broad" term that is not broad), so a mislabelled
// number is never published.
func (o *FullSearchOracle) Validate() error {
	var errs []error
	for i, t := range o.Terms {
		c := o.counts[i]
		switch t.Class {
		case FullSearchRare:
			if c != 1 {
				errs = append(errs, fmt.Errorf("rare %q matches %d entities, want 1", t.Text, c))
			}
		case FullSearchConjunction:
			if c > 1 {
				errs = append(errs, fmt.Errorf("conjunction %q matches %d entities, want 0 or 1", t.Text, c))
			}
		case FullSearchBroad:
			if c <= read.NativePreviewCandidates {
				errs = append(errs, fmt.Errorf("broad %q matches %d entities, not above the %d-candidate budget", t.Text, c, read.NativePreviewCandidates))
			}
		case FullSearchNone:
			if c != 0 {
				errs = append(errs, fmt.Errorf("none %q matches %d entities", t.Text, c))
			}
		}
	}
	return errors.Join(errs...)
}

// ErrFullSearchBankAbsent marks a SearchUnavailable page: no usable v1 bank.
// Screening records it as a gap; it is never a ready sample and never a pass.
var ErrFullSearchBankAbsent = errors.New("full search unavailable: no v1 bank")

// Check verifies one returned page for term i. Pending pages with a
// continuation are legal intermediate polls and are not checked for
// completeness; a page that claims Exhausted must be exactly right.
func (o *FullSearchOracle) Check(i int, page read.FullSearchPage) error {
	if i < 0 || i >= len(o.Terms) {
		return errors.New("term index out of range")
	}
	t, want := o.Terms[i], o.counts[i]
	if page.Disposition == read.SearchUnavailable {
		return ErrFullSearchBankAbsent
	}
	if page.NativeCandidates < 0 || page.NativeCandidates > read.NativePreviewCandidates {
		return fmt.Errorf("%s %q used %d candidates, over the %d budget", t.Class, t.Text, page.NativeCandidates, read.NativePreviewCandidates)
	}
	if page.CountStatus == read.SearchCountLowerBound && int64(page.Count) > want {
		return fmt.Errorf("%s %q lower bound %d exceeds the true count %d", t.Class, t.Text, page.Count, want)
	}
	if page.CountStatus == read.SearchCountExact && int64(page.Count) != want {
		return fmt.Errorf("%s %q exact count %d, true count %d", t.Class, t.Text, page.Count, want)
	}
	for _, hit := range page.Hits {
		if hit.Subject != search.ManifestEntity {
			continue
		}
		if !titleMatches(search.PrepareTitle(hit.Item.Title).Fold(), o.folded[i]) {
			return fmt.Errorf("%s %q returned entity %d titled %q, which does not match", t.Class, t.Text, hit.ID, hit.Item.Title)
		}
		if want <= fullSearchOracleIDs && !slices.Contains(o.ids[i], hit.ID) {
			return fmt.Errorf("%s %q returned unexpected entity %d", t.Class, t.Text, hit.ID)
		}
	}
	switch {
	case want == 0 && len(page.Hits) > 0:
		return fmt.Errorf("%s %q returned hits for an empty population", t.Class, t.Text)
	case page.Exhausted && !page.Partial && want <= fullSearchOracleIDs:
		got := make([]int64, 0, len(page.Hits))
		for _, hit := range page.Hits {
			got = append(got, hit.ID)
		}
		slices.Sort(got)
		wantIDs := slices.Clone(o.ids[i])
		slices.Sort(wantIDs)
		if !slices.Equal(got, wantIDs) {
			return fmt.Errorf("%s %q exhausted with %v, want %v", t.Class, t.Text, got, wantIDs)
		}
	case want > read.NativePreviewCandidates && page.Exhausted && !page.Partial:
		return fmt.Errorf("%s %q claims an exact exhausted walk of %d candidates", t.Class, t.Text, want)
	}
	if len(page.Hits) > 20 {
		return fmt.Errorf("%s %q returned %d hits, over the page limit", t.Class, t.Text, len(page.Hits))
	}
	return nil
}
