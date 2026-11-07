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
	}{{1998, 5, 2006, nil, nil}, {1999, 6, 2001, int64(1998), nil}, {9902, 7, 4001, nil, int64(1999)}} {
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
			err := f.conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM (SELECT a.id FROM catalog_entities a JOIN catalog_entities ar ON ar.id=a.artist_id WHERE a.kind=6 AND a.retired=0 AND a.library_id=1 AND ar.retired=0 AND (a.artist_id=2006 OR EXISTS(SELECT 1 FROM catalog_entities s JOIN catalog_song_artists sa ON sa.song_id=s.id WHERE s.album_id=a.id AND s.kind=7 AND s.retired=0 AND s.library_id=1 AND sa.artist_id=2006)))`).Scan(&oracle)
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
	if a == p.identity() {
		t.Fatal("union absent from provenance")
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
	for _, mode := range []string{"source", "authority", "limit", "artist", "LIVE", "capacity"} {
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
			case "limit", "artist":
				p.phase = 2
				p.set.ids = []int64{2001}
				p.input.After = &RSArtistAlbumCursor{identity: p.identity(), artist: 2006, library: v.Library, kind: 6, limit: 2}
				if mode == "limit" {
					p.input.After.limit = 3
				} else {
					p.input.After.artist = 1998
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
	for _, mode := range []string{"union", "retired-album", "retired-feature", "primary-context"} {
		t.Run(mode, func(t *testing.T) {
			f, r := artistAlbumFixture(t)
			// Keep a small direct-only plus duplicate plus featured-only population.
			f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=6 AND id NOT IN(1999,2001,9001)`)
			switch mode {
			case "retired-album":
				f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1999`)
			case "retired-feature":
				f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=9902`)
			case "primary-context":
				f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1998`)
			}
			rows, e := f.conn.QueryContext(t.Context(), `SELECT a.id FROM catalog_entities a JOIN catalog_entities ar ON ar.id=a.artist_id AND ar.kind=5 AND ar.retired=0 WHERE a.kind=6 AND a.retired=0 AND a.library_id=1 AND (a.artist_id=2006 OR EXISTS(SELECT 1 FROM catalog_entities s JOIN catalog_song_artists sa ON sa.song_id=s.id WHERE s.album_id=a.id AND s.kind=7 AND s.retired=0 AND s.library_id=1 AND sa.artist_id=2006)) ORDER BY a.id`)
			if e != nil {
				t.Fatal(e)
			}
			var want []int64
			for rows.Next() {
				var id int64
				if e = rows.Scan(&id); e != nil {
					t.Fatal(e)
				}
				want = append(want, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				t.Fatal(e)
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
	}
}

func TestRSArtistAlbumReaderArtworkCapacity(t *testing.T) {
	f, r := artistAlbumFixture(t)
	v := f.input
	v.Kind = 5
	p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(time.Minute), 4<<20, RSArtistAlbumInput{View: v, Artist: 2006, Limit: 1})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	p.selected = []int64{1999}
	p.cards = []RSArtistAlbumCard{{Facts: TitleFacts{Album: &AlbumFacts{}}, PrimaryArtist: BrowseItem{ID: 1998}}}
	p.used = p.lease.PayloadBytes() - 1
	var cost BrowseReadResult
	e = r.hydrate(t.Context(), foregroundRequest(), f.output, p, v.Now, &cost)
	if !errors.Is(e, governor.ErrSnapshotBudget) || p.hydrate != 0 || len(p.cards[0].MissingCanonicalFacts) != 0 {
		t.Fatal("uncharged artwork retained", e, p.hydrate, p.cards[0])
	}
}
