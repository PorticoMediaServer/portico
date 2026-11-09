package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
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

//go:embed rs_artist_albums_reader_test.go
var rsArtistAlbumReaderJournal string

func artistAlbumFixture(t *testing.T, clocks ...func() time.Time) (*browseFixture, *RSArtistAlbumReader) {
	t.Helper()
	now := time.Now
	if len(clocks) > 0 {
		now = clocks[0]
	}
	cfg := readfixture.LiveConfig(now)
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
	defs := append(browseDefinitions(), RSArtistAlbumDefinitions()...)
	defs = append(defs, TitleFactsDefinitions()...)
	defs = append(defs, ArtworkDefinitions()...)
	// composeReadFixture captures the actual composed registry under both native
	// statistics profiles and issues it from the resulting receipt.
	var _ []stmt.Definition = defs
	source := audioRelationsFixture(t)
	for _, x := range []struct {
		id, kind, template int64
		artist, album      any
	}{{1998, 5, 2006, nil, nil}, {1999, 6, 2001, int64(1998), nil}, {9902, 7, 4001, nil, int64(1999)}, {9901, 7, 4001, nil, int64(9001)}} {
		source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,randomblob(16),1,?)`, x.id, x.kind)
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id,album_id) SELECT id,public_id,library_id,kind,?,'x','x','x','x',0,public_id,public_id,?,? FROM catalog_subjects WHERE id=?`, "é\x00album", x.artist, x.album, x.id)
	}
	source.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(9902,2006)`)
	f := composeReadFixture(t, source, defs, cfg, true, fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal))))
	f.exec(t, "ANALYZE")
	g := captureBrowse(t, f, defs, plan.Analyzed, fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal))))
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	r, e := NewRSArtistAlbumReader(f.snapshots, f.authority)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func TestRSArtistAlbumReaderLIVEAndUnion(t *testing.T) {
	synctest.Test(t, rsArtistAlbumLIVEAndUnion)
}

func rsArtistAlbumLIVEAndUnion(t *testing.T) {
	f, r := artistAlbumFixture(t)
	v := f.input
	v.Kind = 5
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(30*time.Second), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 2})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	for i := 0; i < 200; i++ {
		q, req, out := liveRequests("artist-union")
		var page RSArtistAlbumPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
			var err error
			page, _, err = r.Page(ctx, req, time.Second, o, p)
			return err
		})
		if e != nil {
			t.Fatal(i, e)
		}
		if page.Complete {
			var oracle int
			err := f.conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM (SELECT a.id FROM catalog_entities a JOIN catalog_entities ar ON ar.id=a.artist_id WHERE a.kind=6 AND a.retired=0 AND a.library_id=1 AND ar.retired=0 AND (a.artist_id=2006 OR EXISTS(SELECT 1 FROM catalog_entities s JOIN catalog_song_artists sa ON sa.song_id=s.id WHERE s.album_id=a.id AND s.kind=7 AND s.retired=0 AND s.library_id=1 AND sa.artist_id=2006)) AND EXISTS(SELECT 1 FROM catalog_entities m WHERE m.album_id=a.id AND m.kind=7 AND m.retired=0 AND m.library_id=1))`).Scan(&oracle)
			if err != nil || page.Total != oracle {
				t.Fatal("independent current union oracle", page.Total, oracle, err)
			}
			if len(page.Cards) != 2 || page.Total < 2 || page.Next == nil {
				t.Fatal(page)
			}
			for _, c := range page.Cards {
				if c.Facts.Album == nil || c.PrimaryArtist.ID != c.Facts.Album.Artist || len(c.MissingCanonicalFacts) == 0 {
					t.Fatal(c)
				}
			}
			return
		}
		if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal("partial output", page)
		}
	}
	t.Fatal("did not finish")
}
func TestRSArtistAlbumReaderInput(t *testing.T) {
	if _, e := NewRSArtistAlbumReader(nil, nil); e != ErrInput {
		t.Fatal(e)
	}
}
func TestRSArtistAlbumReaderWideBytes(t *testing.T) {
	a := RSArtistAlbumCard{}
	b := a
	b.PrimaryArtist.Title = "é\x00"
	if artistAlbumCardBytes(b)-artistAlbumCardBytes(a) != 12 {
		t.Fatal("wide bytes")
	}
}
func TestRSArtistAlbumReaderCursorIdentity(t *testing.T) {
	p := RSArtistAlbumPreparation{}
	a := p.identity()
	p.set.ids = []int64{1}
	p.set.entries = []rsArtistAlbumEntry{{1, 0, "a"}}
	if a == p.identity() {
		t.Fatal("union absent from provenance")
	}
	// A changed year or title is a different ordered result, not a replay.
	before := p.identity()
	p.set.entries = []rsArtistAlbumEntry{{1, 2024, "a"}}
	if before == p.identity() {
		t.Fatal("ordering key absent from provenance")
	}
	before = p.identity()
	p.set.entries = []rsArtistAlbumEntry{{1, 2024, "A"}}
	if before == p.identity() {
		t.Fatal("title bytes absent from provenance")
	}
}

func TestRSArtistAlbumReaderLifecycle(t *testing.T) {
	f, r := artistAlbumFixture(t)
	v := f.input
	v.Kind = 5
	in := RSArtistAlbumInput{View: v, Artist: 2006, Limit: 2}
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

func TestRSArtistAlbumReaderFenceAndCursorControls(t *testing.T) {
	for _, mode := range []string{"source", "authority", "limit", "artist", "order", "LIVE", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			f, r := artistAlbumFixture(t)
			v := f.input
			v.Kind = 5
			p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 2})
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
				f.exec(t, `UPDATE catalog_entities SET metadata_revision=metadata_revision+1 WHERE id=2006`)
			case "authority":
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
			case "limit", "artist", "order":
				p.phase = 2
				p.set.ids = []int64{2001}
				p.set.entries = []rsArtistAlbumEntry{{2001, 0, "deliberately reversed display"}}
				p.input.After = &RSArtistAlbumCursor{identity: p.identity(), artist: 2006, library: v.Library, kind: 6, limit: 2, order: rsArtistAlbumOrderVersion, year: 0, title: "deliberately reversed display", after: 2001}
				switch mode {
				case "limit":
					p.input.After.limit = 3
				case "artist":
					p.input.After.artist = 1998
				case "order":
					p.input.After.order = 99
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
func TestRSArtistAlbumReaderPublicationDrift(t *testing.T) {
	for _, mode := range []string{"authority", "source"} {
		t.Run(mode, func(t *testing.T) {
			f, r := artistAlbumFixture(t)
			v := f.input
			v.Kind = 5
			p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 2})
			if e != nil || ref != nil {
				t.Fatal(e, ref)
			}
			defer p.Close()
			r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() {
				if mode == "authority" {
					f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
				} else {
					f.exec(t, `UPDATE catalog_entities SET metadata_revision=metadata_revision+1 WHERE id=2006`)
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

// Revocation after the selected page has been hydrated and encoded but before
// the final delivery fence must still refuse publication. This models an
// encoder's completed payload, not a fresh lookup.
func TestRSArtistAlbumReaderPostEncodeRevocation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := artistAlbumFixture(t)
		v := f.input
		v.Kind = 5
		p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 1})
		if e != nil || ref != nil {
			t.Fatal(e, ref)
		}
		defer p.Close()
		p.phase = 3
		p.selected = []rsArtistAlbumEntry{{1999, 0, "é\x00album"}}
		p.cards = []RSArtistAlbumCard{{Facts: TitleFacts{Album: &AlbumFacts{Artist: 2006}}, PrimaryArtist: BrowseItem{ID: 2006}}}
		p.hydrate = 1
		f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
		page, _, e := r.poll(t.Context(), foregroundRequest(), f.output, p, v.Now)
		if e == nil || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal("encoded revoked page escaped", page, e)
		}
	})
}

func TestRSArtistAlbumReaderAbsoluteExpiry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	f, r := artistAlbumFixture(t, func() time.Time { return time.Unix(0, clock.Load()) })
	v := f.input
	v.Kind = 5
	p, ref, e := r.Begin(t.Context(), 1, time.Unix(0, clock.Load()).Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 2})
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

// This oracle checks exact IDs across opaque public continuations, independently
// of the production enumeration statements. Each control changes current facts
// before Begin; no cached acceptance or own-age surrogate admits an album.
func TestRSArtistAlbumReaderCurrentPages(t *testing.T) {
	rsArtistAlbumCurrentPages(t)
}

func rsArtistAlbumCurrentPages(t *testing.T) {
	for _, mode := range []string{"union", "retired-album", "retired-feature", "primary-context", "hidden-album", "hidden-feature", "wrong-library-album", "wrong-library-feature"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := artistAlbumFixture(t)
				// Keep a small direct-only plus duplicate plus featured-only population.
				f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=6 AND id NOT IN(1999,2001,9001)`)
				if mode == "hidden-album" || mode == "hidden-feature" {
					f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
					f.exec(t, `UPDATE catalog_entities SET admission_own_age=0`)
					id := int64(1999)
					if mode == "hidden-feature" {
						id = 9902
					}
					f.exec(t, `UPDATE catalog_entities SET admission_own_age=30,admission_member_count=1,admission_unrated_members=0,admission_min_known_age=30 WHERE id=?`, id)
				}
				if mode == "wrong-library-album" || mode == "wrong-library-feature" {
					// Canonical composite FKs require album, primary artist and song
					// to move together; the reverse featured edge may cross libraries.
					f.exec(t, `BEGIN`)
					f.exec(t, `PRAGMA defer_foreign_keys=ON`)
					f.exec(t, `UPDATE catalog_subjects SET library_id=2 WHERE id IN(1998,1999,9902)`)
					f.exec(t, `UPDATE catalog_entities SET library_id=2 WHERE id IN(1998,1999,9902)`)
					f.exec(t, `COMMIT`)
				}
				switch mode {
				case "retired-album":
					f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1999`)
				case "retired-feature":
					f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=9902`)
				case "primary-context":
					f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1998`)
				}
				maximumAge, allowUnrated := int64(-1), int64(1)
				if mode == "hidden-album" || mode == "hidden-feature" {
					maximumAge, allowUnrated = 12, 0
				}
				want := artistAlbumSQLOracle(t, f, 2006, 1, maximumAge, allowUnrated)
				if mode == "hidden-album" {
					// Subject42 hides the album's accepted container band even
					// though its member item remains visible.
					want = slices.DeleteFunc(want, func(id int64) bool { return id == 1999 })
				}
				var got []int64
				var after *RSArtistAlbumCursor
				for pageNo := 0; pageNo < 10; pageNo++ {
					v := f.input
					v.Kind = 5
					p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 1, After: after})
					if e != nil || ref != nil {
						t.Fatal(e, ref)
					}
					var page RSArtistAlbumPage
					for poll := 0; poll < 100; poll++ {
						q, req, out := liveRequests("artist-current-pages")
						_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
							var err error
							page, _, err = r.Page(ctx, req, time.Second, o, p)
							return err
						})
						if e != nil {
							p.Close()
							t.Fatal(e)
						}
						if page.Complete {
							break
						}
						if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
							t.Fatal("pending leaked", page)
						}
					}
					p.Close()
					if !page.Complete || page.Total != len(want) {
						t.Fatal("complete total", page, len(want))
					}
					for _, c := range page.Cards {
						got = append(got, c.Facts.Item.ID)
						if (c.PrimaryArtist.ID != 2006) != (c.Role == RSArtistAlbumAppearance) {
							t.Fatal("artist relative role", c.Role, c.PrimaryArtist.ID)
						}
						if c.PrimaryArtist.ID != c.Facts.Album.Artist {
							t.Fatal("primary context", c)
						}
						// No accepted album provider tuple exists in this source
						// contract, so the card must name the gap rather than invent
						// a provider match or role.
						if len(c.MissingCanonicalFacts) == 0 {
							t.Fatal("provider facts invented", c)
						}
						if c.Facts.Item.ID == 1999 && c.PrimaryArtist.Title != "é\x00album" {
							t.Fatal("actual bytes", c.PrimaryArtist)
						}
					}
					after = page.Next
					if after == nil {
						break
					}
				}
				if !slices.Equal(got, want) {
					t.Fatal("exact membership/order", got, want)
				}
			})
		})
	}
}

// artistAlbumSQLOracle is the independent legacy-order oracle. It computes the
// expected complete union, membership and order in SQLite over the fixture:
// unknown year last, year DESC, title NOCASE, entity id. It never calls the
// reducer or the production enumeration statements.
func artistAlbumSQLOracle(t *testing.T, f *browseFixture, artist, library, maximumAge, allowUnrated int64) []int64 {
	t.Helper()
	rows, e := f.conn.QueryContext(t.Context(), `SELECT a.id
FROM catalog_entities a
JOIN catalog_entities ar ON ar.id=a.artist_id AND ar.kind=5 AND ar.retired=0
WHERE a.kind=6 AND a.retired=0 AND a.library_id=?
AND (a.artist_id=? OR EXISTS(SELECT 1 FROM catalog_entities s JOIN catalog_song_artists sa ON sa.song_id=s.id WHERE s.album_id=a.id AND s.kind=7 AND s.retired=0 AND s.library_id=? AND sa.artist_id=?))
AND EXISTS(SELECT 1 FROM catalog_entities m WHERE m.album_id=a.id AND m.library_id=? AND m.kind=7 AND m.retired=0
 AND ((m.admission_own_age IS NULL AND ?<>0) OR (m.admission_own_age IS NOT NULL AND (?<0 OR m.admission_own_age<=?))))
ORDER BY CASE WHEN a.year=0 THEN 1 ELSE 0 END,a.year DESC,a.title COLLATE NOCASE,a.id`, library, artist, library, artist, library, allowUnrated, maximumAge, maximumAge)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var want []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		want = append(want, id)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return want
}

func artistAlbumPage(t *testing.T, f *browseFixture, r *RSArtistAlbumReader, limit int, after *RSArtistAlbumCursor) ([]int64, RSArtistAlbumPage) {
	t.Helper()
	v := f.input
	v.Kind = 5
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: limit, After: after})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	var page RSArtistAlbumPage
	for poll := 0; poll < 300; poll++ {
		q, req, out := liveRequests("artist-album-order")
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, out, func(ctx context.Context, o BrowseOutput) error {
			var err error
			page, _, err = r.Page(ctx, req, time.Second, o, p)
			return err
		})
		if e != nil {
			t.Fatal(e)
		}
		if page.Complete {
			var ids []int64
			for _, c := range page.Cards {
				ids = append(ids, c.Facts.Item.ID)
			}
			return ids, page
		}
		if !page.Pending || len(page.Cards) != 0 || page.Total != 0 || page.Next != nil {
			t.Fatal("pending leaked", page)
		}
	}
	t.Fatal("page did not finish")
	return nil, RSArtistAlbumPage{}
}

func artistAlbumAllPages(t *testing.T, f *browseFixture, r *RSArtistAlbumReader, limit int) ([]int64, int) {
	t.Helper()
	var got []int64
	var after *RSArtistAlbumCursor
	total := -1
	for pageNo := 0; pageNo < 40; pageNo++ {
		ids, page := artistAlbumPage(t, f, r, limit, after)
		if page.Total != len(artistAlbumSQLOracle(t, f, 2006, 1, -1, 1)) {
			t.Fatal("total disagrees with independent oracle", page.Total)
		}
		if total < 0 {
			total = page.Total
		} else if total != page.Total {
			t.Fatal("total changed across pages", total, page.Total)
		}
		got = append(got, ids...)
		after = page.Next
		if after == nil {
			return got, total
		}
	}
	t.Fatal("cursor did not terminate")
	return nil, 0
}

func artistAlbumEntity(t *testing.T, f *browseFixture, id, kind int64, title string, artist, album, age any) {
	t.Helper()
	public := make([]byte, 16)
	binary.LittleEndian.PutUint64(public, uint64(id))
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, public, kind)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id,album_id,admission_own_age,admission_member_count) VALUES(?,?,1,?,?,'k','k','k','k',0,?,?,?,?,?,0)`, id, public, kind, title, public, public, artist, album, age)
}

// artistAlbumOrderFixture retires the inherited album population and builds a
// small controlled set: unknown year, equal years with case-only titles, equal
// year+title id ties, UTF-8 NUL and non-ASCII titles. Every album has a current
// visible member so legacy membership semantics keep it in the union.
func artistAlbumOrderFixture(t *testing.T) (*browseFixture, *RSArtistAlbumReader) {
	t.Helper()
	f, r := artistAlbumFixture(t)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=6`)
	for _, a := range []struct {
		id, year int64
		title    string
		song     int64
	}{
		{9001, 0, "Unknown Year", 7101},
		{9002, 2024, "Zebra", 7102},
		{9003, 2024, "zebra", 7103},
		{9004, 2024, "Tie", 7104},
		{9005, 2024, "Tie", 7105},
		{9006, 2020, "é\x00Z", 7106},
		{9007, 2020, "é\x00a", 7107},
		{9008, 2020, "école", 7108},
		{9009, 2020, "École", 7109},
	} {
		f.exec(t, `UPDATE catalog_entities SET retired=0,year=?,title=? WHERE id=? AND kind=6`, a.year, a.title, a.id)
		artistAlbumEntity(t, f, a.song, 7, "member", nil, a.id, nil)
	}
	return f, r
}

func TestRSArtistAlbumReaderLegacyOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := artistAlbumOrderFixture(t)
		want := artistAlbumSQLOracle(t, f, 2006, 1, -1, 1)
		if !slices.Equal(want, []int64{9004, 9005, 9002, 9003, 9009, 9006, 9007, 9008, 9001}) {
			t.Fatal("SQLite legacy order changed", want)
		}
		one, page := artistAlbumPage(t, f, r, len(want), nil)
		if page.Next != nil || page.Total != len(want) || !slices.Equal(one, want) {
			t.Fatal("one-shot order", one, page, want)
		}
		got, total := artistAlbumAllPages(t, f, r, 2)
		if total != len(want) || !slices.Equal(got, want) {
			t.Fatal("multi-page order differs from one-shot", got, want)
		}
	})
}

func TestRSArtistAlbumReaderRequiresVisibleMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := artistAlbumFixture(t)
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=6`)
		f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
		// 9201 has no member at all; 9202's only member is retired.
		artistAlbumEntity(t, f, 9201, 6, "Memberless", int64(2006), nil, nil)
		artistAlbumEntity(t, f, 9202, 6, "Retired member", int64(2006), nil, nil)
		artistAlbumEntity(t, f, 6202, 7, "member", nil, int64(9202), int64(0))
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=6202`)
		// 9204's member is visible; 9205's only member is over the age band;
		// 9206 has a hidden first member but another visible member.
		artistAlbumEntity(t, f, 9204, 6, "Visible", int64(2006), nil, nil)
		artistAlbumEntity(t, f, 6204, 7, "member", nil, int64(9204), int64(0))
		artistAlbumEntity(t, f, 9205, 6, "Hidden member", int64(2006), nil, nil)
		artistAlbumEntity(t, f, 6205, 7, "member", nil, int64(9205), int64(30))
		artistAlbumEntity(t, f, 9206, 6, "Any visible member", int64(2006), nil, nil)
		artistAlbumEntity(t, f, 6206, 7, "member", nil, int64(9206), int64(30))
		artistAlbumEntity(t, f, 6207, 7, "member", nil, int64(9206), int64(5))
		want := artistAlbumSQLOracle(t, f, 2006, 1, 12, 0)
		if !slices.Equal(want, []int64{9206, 9204}) {
			t.Fatal("visible-member oracle", want)
		}
		one, page := artistAlbumPage(t, f, r, 10, nil)
		if page.Total != 2 || !slices.Equal(one, want) {
			t.Fatal("memberless/hidden album escaped or visible member lost", one, page)
		}
	})
}

// The member admission band is a COALESCE sentinel, so every (maximum_age,
// allow_unrated) fence must agree with the independent SQL oracle for a NULL,
// 0, 12 and 18 member. In particular, a BlockUnrated profile whose maximum is
// the fence's -1 must still hide an unrated member; previously it fell through
// the maximumAge>=0 branch and published the album. Each case seeds the same
// member age through BOTH discovery paths: a primary album (artist_id=2006)
// and a featured-only album (another live primary artist plus a
// catalog_song_artists credit), so the two paths cannot disagree.
func TestRSArtistAlbumReaderUnratedMemberTruthTable(t *testing.T) {
	members := []struct {
		name string
		age  any
	}{
		{"unrated", nil},
		{"age0", int64(0)},
		{"age12", int64(12)},
		{"age18", int64(18)},
	}
	cases := []struct {
		name         string
		maximumAge   int64
		allowUnrated int64
	}{
		{"no-max-allow-unrated", -1, 1},
		{"no-max-block-unrated", -1, 0},
		{"max12-allow-unrated", 12, 1},
		{"max12-block-unrated", 12, 0},
		{"max0-block-unrated", 0, 0},
		{"max0-allow-unrated", 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := artistAlbumFixture(t)
				f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=6`)
				f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',?,?)`, c.maximumAge, c.allowUnrated)
				for i, m := range members {
					primary, member := int64(9300+i), int64(6300+i)
					artistAlbumEntity(t, f, primary, 6, "primary "+m.name, int64(2006), nil, nil)
					artistAlbumEntity(t, f, member, 7, "member", nil, primary, m.age)

					featured, song := int64(9400+i), int64(6400+i)
					artistAlbumEntity(t, f, featured, 6, "featured "+m.name, int64(1998), nil, nil)
					artistAlbumEntity(t, f, song, 7, "member", nil, featured, m.age)
					f.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(?,2006)`, song)
				}
				want := artistAlbumSQLOracle(t, f, 2006, 1, c.maximumAge, c.allowUnrated)
				got, page := artistAlbumPage(t, f, r, 20, nil)
				if page.Total != len(want) || !slices.Equal(got, want) {
					t.Fatal("member band differs from oracle", c.name, got, want)
				}
				unratedPrimary, unratedFeatured := int64(9300), int64(9400)
				if c.allowUnrated == 0 {
					if slices.Contains(got, unratedPrimary) || slices.Contains(got, unratedFeatured) {
						t.Fatal("block-unrated published an unrated member", c.name, got)
					}
				} else if !slices.Contains(got, unratedPrimary) || !slices.Contains(got, unratedFeatured) {
					t.Fatal("allow-unrated lost an unrated member", c.name, got)
				}
				// A rated member inside the current band keeps the album through
				// both paths. With no maximum every rated age qualifies; otherwise
				// the member at exactly the maximum proves the inclusive bound.
				rated := 3 // age18, visible only when no maximum applies
				if c.maximumAge == 12 {
					rated = 1 // age12
				} else if c.maximumAge == 0 {
					rated = 1 // age0
				}
				for _, album := range []int64{9300 + int64(rated), 9400 + int64(rated)} {
					if !slices.Contains(got, album) {
						t.Fatal("rated visible member lost the album", c.name, album, got)
					}
				}
			})
		})
	}
}

// The art helper reads the album's own accepted poster. Legacy listening pages
// fall back to the first visible member song's poster; that cross-family
// fallback is not wired here (see the fixture handoff note), so this pins the
// current choice: own art wins and a member's art is never substituted.
func TestRSArtistAlbumReaderArtParityPinsOwnArt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := artistAlbumOrderFixture(t)
		rsArtSelect(t, f, 9004, "poster", "matched", 20)
		rsArtSelect(t, f, 7104, "poster", "matched", 21)
		rsArtSelect(t, f, 7105, "poster", "matched", 22)
		_, page := artistAlbumPage(t, f, r, 9, nil)
		var own, memberless *RSArtistAlbumCard
		for i := range page.Cards {
			switch page.Cards[i].Facts.Item.ID {
			case 9004:
				own = &page.Cards[i]
			case 9005:
				memberless = &page.Cards[i]
			}
		}
		if own == nil || !own.Artwork.Present || own.Artwork.Owner != 9004 {
			t.Fatal("album own art not selected", own)
		}
		if memberless == nil || memberless.Artwork.Present {
			t.Fatal("member art substituted without the legacy fallback", memberless)
		}
	})
}

func TestRSArtistAlbumReaderArtworkCapacity(t *testing.T) {
	synctest.Test(t, rsArtistAlbumArtworkCapacity)
}

func rsArtistAlbumArtworkCapacity(t *testing.T) {
	f, r := artistAlbumFixture(t)
	v := f.input
	v.Kind = 5
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 1})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	p.selected = []rsArtistAlbumEntry{{1999, 0, "é\x00album"}}
	p.cards = []RSArtistAlbumCard{{Facts: TitleFacts{Album: &AlbumFacts{}}, PrimaryArtist: BrowseItem{ID: 1998}}}
	p.used = p.lease.PayloadBytes() - 1
	var cost BrowseReadResult
	e = r.hydrate(t.Context(), foregroundRequest(), f.output, p, v.Now, &cost)
	if !errors.Is(e, governor.ErrSnapshotBudget) || p.hydrate != 0 || len(p.cards[0].MissingCanonicalFacts) != 0 {
		t.Fatal("uncharged artwork retained", e, p.hydrate, p.cards[0])
	}
}
