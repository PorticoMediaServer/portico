package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_collection_complete_fixture_test.go
var rsCollectionCompleteJournal string

func rsCollectionCompleteHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsCollectionHash(t)+rsCollectionCompleteJournal)))
}

func rsCollectionCompleteConfig() governor.Config {
	c := readfixture.LiveConfig(time.Now)
	c.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}
	for i := range c.Partitions {
		c.Partitions[i][governor.MemoryBytes] = 128 << 20
	}
	return c
}

func newRSCollectionCompleteSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newRSCollectionSource(t)
	// The collection's own public art is delivered only after complete
	// membership reduction; the authored members' art is unrelated.
	rsArtSelect(t, f, 1101, "poster", "matched", 1)
	return f
}

func newRSCollectionCompleteFixture(t *testing.T, mutate func(*browseFixture)) (*browseFixture, *CollectionReader) {
	t.Helper()
	f := newRSCollectionCompleteSource(t)
	definitions := append(browseDefinitions(), CollectionCompleteDefinitions()...)
	definitions = append(definitions, ArtworkDefinitions()...)
	f = composeReadFixture(t, f, definitions, rsCollectionCompleteConfig(), true, rsCollectionCompleteHash(t))
	if mutate != nil {
		mutate(f)
	}
	r, err := NewCollectionReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func newRSCollectionCompleteLiveFixture(t *testing.T, mutate func(*browseFixture)) (*browseFixture, *CollectionReader) {
	t.Helper()
	f := newRSCollectionCompleteSource(t)
	definitions := append(browseDefinitions(), CollectionCompleteDefinitions()...)
	definitions = append(definitions, ArtworkDefinitions()...)
	f = composeReadFixture(t, f, definitions, rsCollectionCompleteConfig(), false, rsCollectionCompleteHash(t))
	if mutate != nil {
		mutate(f)
	}
	r, err := NewCollectionReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rsBeginCompleteCollection(t *testing.T, f *browseFixture, r *CollectionReader) *CollectionPreparation {
	t.Helper()
	p, refusal, err := r.BeginCollectionPreparation(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 1<<20, CollectionInput{View: f.input, Collection: 1101})
	if err != nil || refusal != nil {
		t.Fatal(err, refusal)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}

// rsCompleteAuthored advances the exact authored reduction under one original
// lease. Functional tests use the private quantum entry so no fake LIVE grant is
// needed; the LIVE control exercises the public Advance.
func rsCompleteAuthored(t *testing.T, f *browseFixture, p *CollectionPreparation) {
	t.Helper()
	for n := 0; n < 2000; n++ {
		step, cost, err := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
		if err != nil {
			t.Fatalf("advance %d stage %d: %v %+v", n, p.cursor.stage, err, cost)
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
	t.Fatal("authored reduction did not finish")
}

func rsCompleteAuthoredLive(t *testing.T, f *browseFixture, p *CollectionPreparation) {
	t.Helper()
	q, request, output := liveRequests("rs-collection-complete")
	for n := 0; n < 2000; n++ {
		var step CollectionAdvance
		var inner error
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			step, _, inner = p.Advance(ctx, request, time.Second, out)
			if inner == nil && f.authority.ForegroundUsage().Live != 1 {
				t.Fatal("lost consumer custody")
			}
			return inner
		})
		if err != nil {
			t.Fatal(err)
		}
		if step.Ready {
			return
		}
	}
	t.Fatal("authored reduction did not finish under LIVE")
}

func rsAuthoredDetail(t *testing.T, f *browseFixture, p *CollectionPreparation, after *CollectionDetailCursor) (CollectionDetail, BrowseReadResult, error) {
	t.Helper()
	offset := 0
	if after != nil {
		offset = after.offset
	}
	return p.detail(t.Context(), foregroundRequest(), time.Second, f.output, offset, f.input.Now)
}

func rsAuthoredLiveDetail(t *testing.T, f *browseFixture, p *CollectionPreparation, after *CollectionDetailCursor) (CollectionDetail, BrowseReadResult, error) {
	t.Helper()
	q, request, output := liveRequests("rs-collection-complete")
	var detail CollectionDetail
	var cost BrowseReadResult
	var inner error
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		detail, cost, inner = p.Detail(ctx, request, time.Second, out, after)
		return inner
	})
	if err != nil {
		return detail, cost, err
	}
	return detail, cost, inner
}

func rsAuthoredVisibleIDs(t *testing.T) []int64 {
	t.Helper()
	var out []int64
	for id := int64(1); id <= 201; id++ {
		if id%9 != 0 {
			out = append(out, id)
		}
	}
	return out
}
