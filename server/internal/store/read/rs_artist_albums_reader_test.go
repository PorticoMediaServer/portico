package read

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"testing"
	"time"
)

func artistAlbumFixture(t *testing.T) (*browseFixture, *RSArtistAlbumReader) {
	t.Helper()
	cfg := readfixture.LiveConfig(time.Now)
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
	defs := append(browseDefinitions(), RSArtistAlbumDefinitions()...)
	defs = append(defs, TitleFactsDefinitions()...)
	defs = append(defs, ArtworkDefinitions()...)
	// composeReadFixture captures the actual composed registry under both native
	// statistics profiles and issues it from the resulting receipt.
	var _ []stmt.Definition = defs
	f := composeReadFixture(t, audioRelationsFixture(t), defs, cfg, false, fmt.Sprintf("%x", sha256.Sum256([]byte(rsAudioRelationsJournal+rsDetailJournal))))
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
				if c.Facts.Album == nil || c.PrimaryArtist.Item.ID != 2006 || len(c.MissingCanonicalFacts) == 0 {
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
	b.PrimaryArtist.Country = "é\x00"
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
