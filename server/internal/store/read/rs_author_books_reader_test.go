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
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

//go:embed rs_author_books_reader_test.go
var rsAuthorBookReaderJournal string

// authorBookFixture builds the current-library population the reader must page.
// Membership is deliberately made hard to guess: duplicate file children,
// fileless, retired, hidden-file, retired-file-only, wrong-author and
// foreign-library books sit beside the visible members. Title order is made
// different from both ID order and series position; two titles differ only in
// case, and one visible book carries real artwork.
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
	var _ []stmt.Definition = defs
	source := audioRelationsFixture(t)
	source.exec(t, `UPDATE catalog_entities SET author=' École ',title='Book',series_position='2.5' WHERE id=2002`)
	source.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=9`)
	for _, x := range []struct {
		id, kind, book     int64
		author, title, pos string
		retired            int64
	}{{14001, 9, 2002, "", "", "", 0}, {14002, 9, 2002, "", "", "", 0}, {14003, 8, 0, " ÉCOLE ", "First", "1", 0}, {14004, 9, 14003, "", "", "", 0}, {14005, 8, 0, "École", "Fileless", "3", 0}, {14006, 8, 0, "other", "Wrong author", "1", 0}, {14007, 9, 14006, "", "", "", 0}, {14008, 8, 0, "École", "Retired", "1", 1}, {14009, 9, 14008, "", "", "", 0}, {14010, 8, 0, "École", "Hidden file", "1", 0}, {14011, 9, 14010, "", "", "", 0}, {14014, 8, 0, "École", "Retired file only", "1", 0}, {14015, 9, 14014, "", "", "", 1}, {14016, 8, 0, "", "", "", 0}, {14020, 8, 0, "École", "apple", "", 0}, {14021, 9, 14020, "", "", "", 0}, {14022, 8, 0, "École", "book", "0", 0}, {14023, 9, 14022, "", "", "", 0}, {14024, 8, 0, "École", "Zebra", "1", 0}, {14025, 9, 14024, "", "", "", 0}, {14030, 8, 0, "Solo", "Only fileless", "", 0}, {14032, 8, 0, "Solo", "Only retired file", "2", 0}, {14033, 9, 14032, "", "", "", 1}} {
		source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(?,randomblob(16),1,?)`, x.id, x.kind)
		var book any
		if x.book != 0 {
			book = x.book
		}
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,author,series_position,book_id,retired,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,?,?,?,?,?,'x','x','x','x',0,public_id,public_id FROM catalog_subjects WHERE id=?`, x.title, x.author, x.pos, book, x.retired, x.id)
	}
	// 13002 has only fileless and retired-file books; 13003 has no book at all.
	for _, a := range []struct {
		id    int64
		title string
	}{{13002, "Solo"}, {13003, "Nobody"}} {
		source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(?,randomblob(16),1,13)`, a.id)
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,?,'x','x','x','x',0,public_id,public_id FROM catalog_subjects WHERE id=?`, a.title, a.id)
	}
	source.exec(t, `UPDATE catalog_entities SET admission_own_age=99 WHERE id=14011`)
	for _, x := range []struct {
		id, kind int64
		book     any
	}{{14012, 8, nil}, {14013, 9, int64(14012)}} {
		source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(?,randomblob(16),2,?)`, x.id, x.kind)
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,author,series_position,book_id,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,'Wrong library','École','1',?,'x','x','x','x',0,public_id,public_id FROM catalog_subjects WHERE id=?`, x.book, x.id)
	}
	// Real accepted artwork on the first visible book, plus explicit locked-empty
	// absence on the other page books: an empty ArtworkFact would drop a
	// delivered fact, not an optional decoration. Seed both through the shared
	// rs_art helpers so the E artwork statements are planned over a realistic
	// population; a single-row table lets ANALYZE choose a scan instead of the
	// declared primary-key witness for rs.art.verify_empty. 14024 carries the
	// aggregate publication proof below and deliberately stays art-free, so the
	// shared publication window's eight-call fence is never asked to fit a
	// verify witness as well.
	rsArtSelect(t, source, 14020, "poster", "matched", 1)
	for _, id := range []int64{2002, 14003, 14010, 14022} {
		rsArtSelectEmpty(t, source, id, "poster")
	}

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

// authorPage polls one Begin/Page preparation to completion and returns the
// delivered page. Page is the single terminal delivery path (task 10
// lifecycle): it hands off and retires the preparation once the page is ready.
func authorPage(t *testing.T, f *browseFixture, r *RSAuthorBookReader, in RSAuthorBookInput) RSAuthorBookPage {
	t.Helper()
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	for i := 0; i < 400; i++ {
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
			return page
		}
		if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal("pending output", page)
		}
	}
	t.Fatal("not complete")
	return RSAuthorBookPage{}
}

// authorBookIDs is an independent copy of the legacy author books section's
// order: plain SQL over the fixture, title COLLATE NOCASE in the requested
// direction, then entity_id. It deliberately shares no Go ordering helper with
// the reader.
func authorBookIDs(t *testing.T, f *browseFixture, author int64, descending bool) []int64 {
	t.Helper()
	dir := "ASC"
	if descending {
		dir = "DESC"
	}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT b.id FROM catalog_entities b WHERE b.library_id=1 AND b.kind=8 AND b.retired=0 AND lower(trim(b.author))=(SELECT lower(trim(title)) FROM catalog_entities WHERE id=?) AND EXISTS(SELECT 1 FROM catalog_entities f WHERE f.book_id=b.id AND f.library_id=b.library_id AND f.kind=9 AND f.retired=0) ORDER BY b.title COLLATE NOCASE `+dir+`,b.id `+dir, author)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// authorPageFailure polls to a terminal refusal and returns it. A completed
// page is not a refusal.
func authorPageFailure(t *testing.T, f *browseFixture, r *RSAuthorBookReader, in RSAuthorBookInput) error {
	t.Helper()
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	for i := 0; i < 400; i++ {
		q, req, out := liveRequests("author-failure")
		var page RSAuthorBookPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
			var err error
			page, _, err = r.Page(ctx, req, time.Second, o, p)
			return err
		})
		if e != nil {
			return e
		}
		if page.Complete {
			return nil
		}
		if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal("pending output", page)
		}
	}
	t.Fatal("not refused")
	return nil
}

func TestRSAuthorBookReaderLIVEAndMembership(t *testing.T) {
	synctest.Test(t, rsAuthorBookReaderLIVEAndMembership)
}

func rsAuthorBookReaderLIVEAndMembership(t *testing.T) {
	f, r := authorBookFixture(t)
	v := f.input
	v.Kind = 13
	page := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
	ids := authorBookIDs(t, f, 13001, false)
	if page.Total != len(ids) || len(page.Cards) != min(2, len(ids)) {
		t.Fatal(page, ids)
	}
	if page.Total != 6 || !slices.Equal(ids, []int64{14020, 2002, 14022, 14003, 14010, 14024}) {
		t.Fatal("complete membership", page.Total, ids)
	}
	for j, c := range page.Cards {
		if c.Facts.Item.ID != ids[j] || c.Personal == nil || c.Facts.Book == nil || c.Facts.Book.ProviderMatchStatus != "deferred" || !c.Facts.Book.EffectiveSeriesPending || !c.ResumePending || len(c.MissingCanonicalFacts) == 0 {
			t.Fatal(c, ids)
		}
		for _, fact := range c.MissingCanonicalFacts {
			if fact == "catalog_book_context.series_index" {
				t.Fatal("missing facts name a table absent from the accepted schema", c)
			}
		}
		if c.Facts.Item.ID == 14020 && (!c.Artwork.Present || c.Artwork.Role != "poster" || c.Artwork.Small.MIME != "image/jpeg") {
			t.Fatal("book artwork", c)
		}
		if c.Facts.Item.ID == 14010 && c.Facts.Book.Author != "École" {
			t.Fatal("current facts", c)
		}
	}
}

// Order parity is checked against the legacy SQL itself in both directions, so
// ordering by series position, ID or raw bytes fails.
func TestRSAuthorBookReaderOrderParity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := authorBookFixture(t)
		for _, desc := range []bool{false, true} {
			v := f.input
			v.Kind = 13
			v.Descending = desc
			page := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 6})
			if page.Total != 6 || len(page.Cards) != 6 || page.Next != nil {
				t.Fatal(desc, page)
			}
			var got []int64
			for _, c := range page.Cards {
				got = append(got, c.Facts.Item.ID)
			}
			if want := authorBookIDs(t, f, 13001, desc); !slices.Equal(got, want) {
				t.Fatalf("direction %v: got %v want legacy SQL %v", desc, got, want)
			}
		}
	})
}

// A zero-byte book still needs the fixed 16-byte subject-admission incarnation
// (plus the small publication enum) reserved inside the armed read window.
// Dropping the floor fails the exact-byte preflight rather than under-charging.
func TestRSAuthorBookReaderFixedProofIncarnation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := authorBookFixture(t)
		var a, b int64
		if e := f.conn.QueryRowContext(t.Context(), rsAuthorBookOctetsSQL, int64(14016), int64(1)).Scan(&a, &b); e != nil {
			t.Fatal(e)
		}
		if a != 0 || b != 0 {
			t.Fatalf("fixture point is not empty: %d/%d", a, b)
		}
		v := f.input
		v.Kind = 13
		page := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
		if !page.Complete || page.Total != 6 {
			t.Fatal("fixed proof row was not reserved", page)
		}
	})
}

// An author with only fileless or retired-file books matches the legacy
// author surface: the page exists and its books section is a complete empty
// set. An author with no current book at all is still denied.
func TestRSAuthorBookReaderEmptyAuthor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := authorBookFixture(t)
		v := f.input
		v.Kind = 13
		page := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13002, Limit: 2})
		if !page.Complete || page.Total != 0 || len(page.Cards) != 0 || page.Next != nil {
			t.Fatal("fileless-only author was not a complete empty page", page)
		}

		if e := authorPageFailure(t, f, r, RSAuthorBookInput{View: v, Author: 13003, Limit: 2}); !errors.Is(e, ErrDenied) {
			t.Fatal("bookless author accepted", e)
		}
	})
}

// A current aggregate publication is part of the accepted admission proof for
// a container book. The read must consult it (a changed tuple refuses) and the
// book point is shorter than the fixed 16-byte proof floor.
func TestRSAuthorBookReaderPublicationProof(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := authorBookFixture(t)
		var a, b int64
		if e := f.conn.QueryRowContext(t.Context(), rsAuthorBookOctetsSQL, int64(14024), int64(1)).Scan(&a, &b); e != nil {
			t.Fatal(e)
		}
		if a+b >= 16 {
			t.Fatalf("fixture point is not below the fixed proof floor: %d+%d", a, b)
		}
		f.exec(t, `INSERT INTO catalog_aggregate_operations(id,library_id,source_kind,source_entity_id,source_incarnation,source_revision,phase) VALUES(1,1,'entity',14024,'i','r','complete')`)
		f.exec(t, `INSERT INTO catalog_aggregate_publications(id,operation_id,parent_id,captured_revision,generation,phase,duration_key,community_rating_key,critic_rating_key,file_size_key,episode_count,latest_aired_key,admission_member_count,admission_unrated_members,admission_min_known_age,admission_allow_age,admission_known_age) VALUES(1,1,14024,1,1,'published',0,0,0,0,0,'',0,0,NULL,-1,-1)`)
		f.exec(t, `UPDATE catalog_entities SET aggregate_publication_id=1,admission_member_count=0,admission_unrated_members=0,admission_min_known_age=NULL,admission_allow_age=-1,admission_known_age=-1 WHERE id=14024`)

		v := f.input
		v.Kind = 13
		page := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 6})
		if !page.Complete || page.Total != 6 {
			t.Fatal("published empty container refused", page)
		}
		var seen bool
		for _, c := range page.Cards {
			seen = seen || c.Facts.Item.ID == 14024
		}
		if !seen {
			t.Fatal("published book missing from membership", page)
		}

		f.exec(t, `UPDATE catalog_aggregate_publications SET admission_member_count=6 WHERE id=1`)
		if e := authorPageFailure(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 6}); !errors.Is(e, ErrUnqualified) {
			t.Fatal("mismatched publication proof accepted", e)
		}
	})
}

func TestRSAuthorBookReaderInput(t *testing.T) {
	if _, e := NewRSAuthorBookReader(nil, nil); e != ErrInput {
		t.Fatal(e)
	}
	f, r := authorBookFixture(t)
	v := f.input
	v.Kind = 13
	for _, in := range []RSAuthorBookInput{
		{View: v, Author: 0, Limit: 2},
		{View: v, Author: 13001, Limit: 0},
		{View: v, Author: 13001, Limit: MaxPage + 1},
	} {
		if p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, in); e != ErrInput || p != nil || ref != nil {
			t.Fatal(in, p, ref, e)
		}
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
		t.Fatal("membership absent from provenance")
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
	for _, mode := range []string{"source", "authority", "limit", "author", "library", "kind", "LIVE", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
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
				case "limit", "author", "library", "kind":
					p.phase = 2
					p.entries = []rsAuthorBookEntry{{id: 1108, title: "x", titleKey: "x"}}
					p.input.After = &RSAuthorBookCursor{identity: p.identity(), author: 13001, library: v.Library, kind: 8, limit: 2, titleKey: "x", id: 1108}
					switch mode {
					case "limit":
						p.input.After.limit = 3
					case "author":
						p.input.After.author = 1998
					case "library":
						p.input.After.library = v.Library + 1
					case "kind":
						p.input.After.kind = 9
					}
				case "capacity":
					p.used = p.lease.PayloadBytes()
				}
				page, _, e := r.poll(t.Context(), foregroundRequest(), f.output, p, v.Now)
				if e == nil || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
					t.Fatal("control leaked", page, e)
				}
			})
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
			q, req, out := liveRequests("author-final-drift")
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

// Revocation after the selected page has been hydrated but before the final
// delivery fence must still refuse publication.
func TestRSAuthorBookReaderRevokedBeforeHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := authorBookFixture(t)
		v := f.input
		v.Kind = 13
		p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSAuthorBookInput{View: v, Author: 13001, Limit: 1})
		if e != nil || ref != nil {
			t.Fatal(e, ref)
		}
		defer p.Close()
		p.phase = 3
		p.selected = []int64{14003}
		p.cards = []RSAuthorBookCard{{Facts: TitleFacts{Book: &BookFacts{}}, Personal: &PersonalState{}}}
		p.hydrate = 1
		f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
		q, req, out := liveRequests("author-revoked")
		var page RSAuthorBookPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
			var err error
			page, _, err = r.Page(ctx, req, time.Second, o, p)
			return err
		})
		if e == nil || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal("hydrated revoked page escaped", page, e)
		}
		if !p.closed {
			t.Fatal("refused preparation stayed open")
		}
	})
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

// Every public continuation is re-prepared from the current source; the opaque
// cursor binds principal, author key, library, order and limit.
func TestRSAuthorBookReaderCrossPage(t *testing.T) {
	synctest.Test(t, rsAuthorBookReaderCrossPage)
}

func rsAuthorBookReaderCrossPage(t *testing.T) {
	f, r := authorBookFixture(t)
	for _, desc := range []bool{false, true} {
		v := f.input
		v.Kind = 13
		v.Descending = desc
		one := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 6})
		if one.Total != 6 || len(one.Cards) != 6 || one.Next != nil {
			t.Fatal(desc, one)
		}
		var want []int64
		for _, c := range one.Cards {
			want = append(want, c.Facts.Item.ID)
		}
		var cursor *RSAuthorBookCursor
		var got []int64
		for n := 0; n < 8; n++ {
			page := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 1, After: cursor})
			if page.Total != 6 || len(page.Cards) != 1 {
				t.Fatal(page)
			}
			got = append(got, page.Cards[0].Facts.Item.ID)
			cursor = page.Next
			if cursor == nil {
				break
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("direction %v: traversal %v != one-shot %v", desc, got, want)
		}
		if desc && !slices.Equal(got, []int64{14024, 14010, 14003, 14022, 2002, 14020}) {
			t.Fatal("descending traversal", got)
		}
	}
}

func TestRSAuthorBookReaderRestrictedFiles(t *testing.T) {
	synctest.Test(t, rsAuthorBookReaderRestrictedFiles)
}

func rsAuthorBookReaderRestrictedFiles(t *testing.T) {
	f, r := authorBookFixture(t)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,1) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=12,allow_unrated=1`)
	v := f.input
	v.Kind = 13
	page := authorPage(t, f, r, RSAuthorBookInput{View: v, Author: 13001, Limit: 2})
	if page.Total != 5 || len(page.Cards) != 2 || page.Next == nil {
		t.Fatal("hidden files earned membership", page)
	}
	for _, c := range page.Cards {
		if c.Facts.Item.ID == 14010 {
			t.Fatal("restricted file published", c)
		}
	}
}
