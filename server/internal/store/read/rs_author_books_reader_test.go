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
	defs = append(defs, UOPersonalDefinitions()...)
	source := audioRelationsFixture(t)
	source.exec(t, `UPDATE catalog_entities SET author=' École ',title='Book',series_position='2.5' WHERE id=2002`)
	source.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=9`)
	for _, x := range []struct {
		id, kind, book     int64
		author, title, pos string
		retired            int64
	}{{14001, 9, 2002, "", "", "", 0}, {14002, 9, 2002, "", "", "", 0}, {14003, 8, 0, " ÉCOLE ", "First", "1", 0}, {14004, 9, 14003, "", "", "", 0}, {14005, 8, 0, "École", "Fileless", "3", 0}, {14006, 8, 0, "other", "Wrong author", "1", 0}, {14007, 9, 14006, "", "", "", 0}, {14008, 8, 0, "École", "Retired", "1", 1}, {14009, 9, 14008, "", "", "", 0}, {14010, 8, 0, "École", "Hidden file", "1", 0}, {14011, 9, 14010, "", "", "", 0}} {
		source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(?,randomblob(16),1,?)`, x.id, x.kind)
		var book any
		if x.book != 0 {
			book = x.book
		}
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,author,series_position,book_id,retired,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,?,?,?,?,?,'x','x','x','x',0,public_id,public_id FROM catalog_subjects WHERE id=?`, x.title, x.author, x.pos, book, x.retired, x.id)
	}
	source.exec(t, `UPDATE catalog_entities SET admission_own_age=99 WHERE id=14011`)

	f := composeReadFixture(t, source, defs, cfg, true, fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal+rsAuthorBookReaderJournal))))
	f.exec(t, "ANALYZE")
	g := captureBrowse(t, f, defs, plan.Analyzed, fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal+rsAuthorBookReaderJournal))))
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
			rows, err := f.conn.QueryContext(t.Context(), `SELECT b.id FROM catalog_entities b WHERE b.library_id=1 AND b.kind=8 AND b.retired=0 AND lower(trim(b.author))=(SELECT lower(trim(title)) FROM catalog_entities WHERE id=13001) AND EXISTS(SELECT 1 FROM catalog_entities f WHERE f.book_id=b.id AND f.library_id=b.library_id AND f.kind=9 AND f.retired=0) ORDER BY CASE WHEN CAST(b.series_position AS REAL)>0 THEN CAST(b.series_position AS REAL) ELSE 999999 END,b.title,b.id`)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			rows.Close()
			if page.Total != len(ids) || len(page.Cards) != min(2, len(ids)) {
				t.Fatal(page, ids)
			}
			for j, c := range page.Cards {
				if c.Facts.Item.ID != ids[j] || c.Personal == nil {
					t.Fatal(c, ids)
				}
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

func TestRSAuthorBookReaderPostEncodingFence(t *testing.T) {
	f, r := authorBookFixture(t)
	v := f.input
	v.Kind = 13
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	encoded, delivered := false, false
	for i := 0; i < 200; i++ {
		fg, req, out := liveRequests("author-encode")
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, o BrowseOutput) error {
			_, err := r.Publish(ctx, req, time.Second, o, p, func(page RSAuthorBookPage) ([]byte, error) {
				encoded = true
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
				return []byte("encoded"), nil
			}, func([]byte) error { delivered = true; return nil })
			return err
		})
		if encoded {
			if e == nil || delivered || !p.closed {
				t.Fatal("encoded revoked page delivered", e, delivered)
			}
			return
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	t.Fatal("no ready page")
}

func TestRSAuthorBookReaderCrossPage(t *testing.T) {
	f, r := authorBookFixture(t)
	v := f.input
	v.Kind = 13
	var cursor *RSAuthorBookCursor
	var got []int64
	for n := 0; n < 8; n++ {
		p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 1, After: cursor})
		if e != nil || ref != nil {
			t.Fatal(e, ref)
		}
		ready := false
		for i := 0; i < 200; i++ {
			fg, req, out := liveRequests("author-cross-page")
			_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, o BrowseOutput) error {
				var err error
				ready, err = r.Publish(ctx, req, time.Second, o, p, func(page RSAuthorBookPage) ([]byte, error) {
					if page.Total != 3 || len(page.Cards) != 1 {
						t.Fatal(page)
					}
					got = append(got, page.Cards[0].Facts.Item.ID)
					cursor = page.Next
					return []byte("encoded"), nil
				}, func([]byte) error { return nil })
				return err
			})
			if e != nil {
				p.Close()
				t.Fatal(e)
			}
			if ready {
				break
			}
		}
		p.Close()
		if !ready {
			t.Fatal("pending forever")
		}
		if cursor == nil {
			break
		}
	}
	if fmt.Sprint(got) != "[14003 2002 14010]" {
		t.Fatal("ordered complete traversal", got)
	}
}

func TestRSAuthorBookReaderRestrictedFiles(t *testing.T) {
	f, r := authorBookFixture(t)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,1) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=12,allow_unrated=1`)
	v := f.input
	v.Kind = 13
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	for i := 0; i < 200; i++ {
		fg, req, out := liveRequests("author-restricted")
		var page RSAuthorBookPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, o BrowseOutput) error {
			var err error
			page, _, err = r.Page(ctx, req, time.Second, o, p)
			return err
		})
		if e != nil {
			t.Fatal(e)
		}
		if page.Complete {
			if page.Total != 2 || len(page.Cards) != 2 || page.Next != nil {
				t.Fatal("hidden files earned membership", page)
			}
			return
		}
	}
	t.Fatal("not complete")
}

func TestRSAuthorSQLiteRealOracle(t *testing.T) {
	f := newBrowseSource(t)
	for _, s := range []string{"", " ", "2.5", "+2", "-2", "0x10", "1x", "2e+", "2e-2", ".5", "-.5", "+.", "1.2.3", "1e9999", "-1e9999", "\t\n2.5tail", "NaN", "Inf", "\u00a02", "1\x002", "9007199254740993", "0.0000001"} {
		var want float64
		if e := f.conn.QueryRowContext(t.Context(), `SELECT CAST(? AS REAL)`, s).Scan(&want); e != nil {
			t.Fatal(e)
		}
		if got := rsAuthorSQLiteReal(s); got != want {
			t.Fatalf("%q: got %v want SQLite %v", s, got, want)
		}
	}
}
