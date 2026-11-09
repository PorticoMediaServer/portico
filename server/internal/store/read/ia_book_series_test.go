package read

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/bookseries"
)

func TestIABookSeriesInputRefusals(t *testing.T) {
	var nilReader *ForegroundReader
	if _, _, err := nilReader.IABookSeries(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, IABookSeriesInput{}); !errors.Is(err, ErrInput) {
		t.Fatal("nil reader", err)
	}
	r := &ForegroundReader{}
	for name, in := range map[string]IABookSeriesInput{
		"zero_entity":   {View: BrowseInput{Library: 1, Kind: 8}},
		"zero_library":  {View: BrowseInput{Kind: 8}, Entity: 1},
		"wrong_kind":    {View: BrowseInput{Library: 1, Kind: 7}, Entity: 1},
		"keep_articles": {View: BrowseInput{Library: 1, Kind: 8, KeepArticles: true}, Entity: 1},
		"descending":    {View: BrowseInput{Library: 1, Kind: 8, Descending: true}, Entity: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := r.iaBookSeries(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) {
				t.Fatal(err)
			}
		})
	}
}

// The served cohort is exactly the accepted one: a changed count, byte ledger,
// digest or pair is refused instead of served truncated.
func TestIABookSeriesVerifyExactCohort(t *testing.T) {
	pairs := []IABookSeriesPair{{Name: "Saga", Position: "1"}, {Name: "Émile", Position: ""}}
	converted := []bookseries.Pair{{Name: pairs[0].Name, Position: pairs[0].Position}, {Name: pairs[1].Name, Position: pairs[1].Position}}
	count, retained, digest, err := bookseries.Cohort(converted)
	if err != nil {
		t.Fatal(err)
	}
	head := IABookSeriesHead{Present: true, Asset: 41, InfoEpoch: 1, PolicyRevision: 4, Revision: 1,
		MemberCount: count, MemberBytes: retained, Digest: digest, SourceRevision: "book-token"}
	if err := iaBookSeriesVerify(head, pairs); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*IABookSeriesHead){
		"absent":   func(h *IABookSeriesHead) { h.Present = false },
		"revision": func(h *IABookSeriesHead) { h.Revision = 0 },
		"count":    func(h *IABookSeriesHead) { h.MemberCount++ },
		"bytes":    func(h *IABookSeriesHead) { h.MemberBytes++ },
		"digest":   func(h *IABookSeriesHead) { h.Digest[0] ^= 1 },
		"negative": func(h *IABookSeriesHead) { h.MemberBytes = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := head
			mutate(&tampered)
			if err := iaBookSeriesVerify(tampered, pairs); !errors.Is(err, ErrUnqualified) {
				t.Fatal(err)
			}
		})
	}
	if err := iaBookSeriesVerify(head, []IABookSeriesPair{{Name: "Saga", Position: "2"}, pairs[1]}); !errors.Is(err, ErrUnqualified) {
		t.Fatal("changed pair", err)
	}
	if err := iaBookSeriesVerify(head, nil); !errors.Is(err, ErrUnqualified) {
		t.Fatal("missing pairs", err)
	}
}

// An accepted empty cohort carries the canonical empty digest and zero bytes;
// a fabricated head is refused.
func TestIABookSeriesVerifyEmptyCohort(t *testing.T) {
	head := IABookSeriesHead{Present: true, Revision: 1, MemberCount: 0, MemberBytes: 0, Digest: bookseries.EmptyDigest()}
	if err := iaBookSeriesVerify(head, nil); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*IABookSeriesHead){
		"bytes":  func(h *IABookSeriesHead) { h.MemberBytes = 1 },
		"digest": func(h *IABookSeriesHead) { h.Digest[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := head
			mutate(&tampered)
			if err := iaBookSeriesVerify(tampered, nil); !errors.Is(err, ErrUnqualified) {
				t.Fatal(err)
			}
		})
	}
	if err := iaBookSeriesVerify(head, []IABookSeriesPair{{Name: "Saga"}}); !errors.Is(err, ErrUnqualified) {
		t.Fatal("fabricated pair on an empty head", err)
	}
}

// The method must not reach a snapshot when the caller input is already invalid.
func TestIABookSeriesDoesNotOpenSnapshotOnInputRefusal(t *testing.T) {
	r := &ForegroundReader{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.iaBookSeries(ctx, governor.Request{}, time.Second, BrowseOutput{}, IABookSeriesInput{View: BrowseInput{Library: 1, Kind: 8}, Entity: -1}); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}
