package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"sync/atomic"
	"testing"
	"time"
)

//go:embed rs_author_books_reader_test.go
var rsAuthorBookReaderJournal string

func authorBookFixture(t *testing.T, clocks ...func() time.Time) (*browseFixture, *RSAuthorBookReader) {
	t.Helper()
	now := time.Now
	if len(clocks) > 0 {
		now = clocks[0]
	}
	cfg := readfixture.LiveConfig(now)
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
	defs := append(browseDefinitions(), RSAuthorBookDefinitions()...)
	defs = append(defs, TitleFactsDefinitions()...)
	defs = append(defs, ArtworkDefinitions()...)
	source := audioRelationsFixture(t)
	source.exec(t, `UPDATE catalog_entities SET author=' École ',title='Book',series_position='2.5' WHERE id=1108`)
	source.exec(t, `UPDATE catalog_entities SET book_id=1108 WHERE id=1109`)
	f := composeReadFixture(t, source, defs, cfg, true, fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal))))
	f.exec(t, "ANALYZE")
	g := captureBrowse(t, f, defs, plan.Analyzed, fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal))))
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	r, e := NewRSAuthorBookReader(f.snapshots, f.authority)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func TestRSAuthorBookReaderLIVEAndMembership(t *testing.T) {
	f, r := authorBookFixture(t)
	v := f.input
	v.Kind = 13
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	for i := 0; i < 200; i++ {
		q, req, out := liveRequests("author-page")
		var page RSAuthorBookPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
			var err error
			page, _, err = r.Page(ctx, req, time.Second, o, p)
			return err
		})
		if e != nil {
			t.Fatal(i, e)
		}
		if page.Complete {
			if page.Total != 1 || len(page.Cards) != 1 || page.Cards[0].Facts.Book == nil {
				t.Fatal(page)
			}
			return
		}
		if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal(page)
		}
	}
	t.Fatal("not complete")
}
func TestRSAuthorBookReaderInput(t *testing.T) {
	if _, e := NewRSAuthorBookReader(nil, nil); e != ErrInput {
		t.Fatal(e)
	}
}
func TestRSAuthorBookReaderWideBytes(t *testing.T) {
	a := RSAuthorBookCard{}
	b := a
	b.Facts.Item.Title = "é\x00"
	if authorBookCardBytes(b)-authorBookCardBytes(a) != 12 {
		t.Fatal("wide bytes")
	}
}
func TestRSAuthorBookReaderCursorIdentity(t *testing.T) {
	p := RSAuthorBookPreparation{}
	a := p.identity()
	p.entries = []rsAuthorBookEntry{{id: 1}}
	if a == p.identity() {
		t.Fatal("union absent from provenance")
	}
}

func TestRSAuthorBookReaderLifecycle(t *testing.T) {
	f, r := authorBookFixture(t)
	v := f.input
	v.Kind = 13
	in := RSAuthorBookInput{View: v, Author: 13001, Limit: 2}
	if p, _, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 1, in); e == nil && p != nil {
		p.Close()
		t.Fatal("retention exceeded tiny lease")
	}
	ctx, cancel := context.WithCancel(t.Context())
	p, ref, e := r.Begin(ctx, 1, time.Now().Add(time.Minute), 4<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	cancel()
	_, _, e = r.poll(t.Context(), foregroundRequest(), f.output, p, v.Now)
	if e == nil {
		t.Fatal("cancelled original job read")
	}
}

func TestRSAuthorBookReaderFenceAndCursorControls(t *testing.T) {
	for _, mode := range []string{"source", "authority", "limit", "artist", "LIVE", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			f, r := authorBookFixture(t)
			v := f.input
			v.Kind = 13
			p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
			if e != nil || ref != nil {
				t.Fatal(e, ref)
			}
			defer p.Close()
			if mode == "LIVE" {
				_, _, e = r.Page(t.Context(), foregroundRequest(), time.Second, f.output, p)
				if !errors.Is(e, ErrInput) {
					t.Fatal(e)
				}
				return
			}
			var cost BrowseReadResult
			if e = r.fence(t.Context(), foregroundRequest(), f.output, p, v.Now, &cost); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "source":
				f.exec(t, `UPDATE catalog_entities SET metadata_revision=metadata_revision+1 WHERE id=13001`)
			case "authority":
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
			case "limit", "artist":
				p.phase = 2
				p.entries = []rsAuthorBookEntry{{id: 1108}}
				p.input.After = &RSAuthorBookCursor{identity: p.identity(), author: 13001, library: v.Library, kind: 8, limit: 2}
				if mode == "limit" {
					p.input.After.limit = 3
				} else {
					p.input.After.author = 1998
				}
			case "capacity":
				p.used = p.lease.PayloadBytes()
			}
			page, _, e := r.poll(t.Context(), foregroundRequest(), f.output, p, v.Now)
			if e == nil || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
				t.Fatal("control leaked", page, e)
			}
		})
	}
}
func TestRSAuthorBookReaderPublicationDrift(t *testing.T) {
	for _, mode := range []string{"authority", "source"} {
		t.Run(mode, func(t *testing.T) {
			f, r := authorBookFixture(t)
			v := f.input
			v.Kind = 13
			p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
			if e != nil || ref != nil {
				t.Fatal(e, ref)
			}
			defer p.Close()
			r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() {
				if mode == "authority" {
					f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
				} else {
					f.exec(t, `UPDATE catalog_entities SET metadata_revision=metadata_revision+1 WHERE id=13001`)
				}
			}}
			q, req, out := liveRequests("artist-final-drift")
			_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
				page, _, err := r.Page(ctx, req, time.Second, o, p)
				if len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
					t.Fatal(page)
				}
				return err
			})
			if e == nil {
				t.Fatal("publication drift accepted")
			}
		})
	}
}

func TestRSAuthorBookReaderAbsoluteExpiry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	f, r := authorBookFixture(t, func() time.Time { return time.Unix(0, clock.Load()) })
	v := f.input
	v.Kind = 13
	p, ref, e := r.Begin(t.Context(), 1, time.Unix(0, clock.Load()).Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	clock.Add(int64(2 * time.Minute))
	page, _, e := r.poll(t.Context(), foregroundRequest(), f.output, p, v.Now)
	if e == nil || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
		t.Fatal("expired original lease", page, e)
	}
}
