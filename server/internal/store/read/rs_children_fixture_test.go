package read

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_children_fixture_test.go
var rsChildrenJournal string

func rsChildrenFixtureHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.BrowseHash(t)+rsChildrenJournal)))
}

func rsChildrenSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, "BEGIN")
	for _, k := range []struct {
		id        int
		name      string
		container int
	}{
		{3, "season", 1}, {4, "episode", 0}, {5, "artist", 1}, {6, "album", 1}, {7, "song", 0}, {8, "book", 1}, {9, "audiobook_file", 0},
	} {
		f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(?,?,0,?,1,1,0)`, k.id, k.name, k.container)
	}
	insert := func(id, kind int64, show, season, artist, album, book, disc, track, part, seasonNumber, episodeNumber any, retired int64) {
		var authorKey any
		if kind == 8 {
			authorKey = "author:"
		}
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, public, kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,
show_id,season_id,artist_id,album_id,book_id,disc_number,track_number,part_number,season_number,episode_number,numbering,retired,author,book_author_identity_key)
VALUES(?,?,1,?,'deliberately reversed display','z','z','z','z',0,?,?, ?,?,?,?,?,?,?,?,?,?,'seasonal',?,'',?)`, id, public, kind, public, public, show, season, artist, album, book, disc, track, part, seasonNumber, episodeNumber, retired, authorKey)
	}
	for _, p := range []struct {
		id, kind             int64
		show, artist, season any
	}{
		{2006, 5, nil, nil, nil}, {2003, 2, nil, nil, nil}, {2004, 2, nil, nil, nil},
		{2005, 3, int64(2003), nil, int64(-10)}, {2001, 6, nil, int64(2006), nil}, {2002, 8, nil, nil, nil},
	} {
		insert(p.id, p.kind, p.show, nil, p.artist, nil, nil, nil, nil, nil, p.season, nil, 0)
	}
	for family := RSAlbumTracks; family <= RSArtistAlbums; family++ {
		for n := int64(1); n <= 90; n++ {
			id := int64(3000) + int64(family)*1000 + n
			var disc, number any
			switch n % 5 {
			case 0:
				disc = nil
			case 1:
				disc = int64(-1)
			case 2:
				disc = int64(0)
			case 3:
				disc = int64(1)
			case 4:
				disc = int64(2)
			}
			switch n % 4 {
			case 0:
				number = nil
			case 1:
				number = int64(0)
			case 2:
				number = int64(1)
			case 3:
				number = int64(3)
			}
			retired := int64(0)
			if n%9 == 0 {
				retired = 1
			}
			switch family {
			case RSAlbumTracks:
				insert(id, 7, nil, nil, nil, int64(2001), nil, disc, number, nil, nil, nil, retired)
			case RSAudiobookFiles:
				insert(id, 9, nil, nil, nil, nil, int64(2002), disc, nil, number, nil, nil, retired)
			case RSShowSeasons:
				insert(id, 3, int64(2003), nil, nil, nil, nil, nil, nil, nil, (n*37)%91, nil, retired)
			case RSShowEpisodes:
				insert(id, 4, int64(2004), nil, nil, nil, nil, nil, nil, nil, nil, n, retired)
			case RSSeasonEpisodes:
				insert(id, 4, int64(2003), int64(2005), nil, nil, nil, nil, nil, nil, nil, n, retired)
			case RSArtistAlbums:
				insert(id, 6, nil, nil, int64(2006), nil, nil, nil, nil, nil, nil, nil, retired)
			}
		}
	}
	// Wrong-kind NULL keys must consume the raw budget before type rejection;
	// continuation from them must reach negative/zero/positive real keys.
	insert(11001, 1, int64(2004), nil, nil, nil, nil, nil, nil, nil, nil, nil, 0)
	insert(11002, 1, int64(2003), int64(2005), nil, nil, nil, nil, nil, nil, nil, nil, 0)
	f.exec(t, "COMMIT")
	return f
}

func rsChildrenFixture(t *testing.T) (*browseFixture, *RSChildrenReader) {
	t.Helper()
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundRequestBytes}
	f := composeReadFixture(t, rsChildrenSource(t), append(browseDefinitions(), RSChildrenDefinitions()...), gc, true, rsChildrenFixtureHash(t))
	r, err := NewRSChildrenReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rsChildrenInput(f *browseFixture, family RSChildrenFamily) RSChildrenInput {
	r, _ := rsChildrenRecipeFor(family)
	in := RSChildrenInput{View: f.input, Parent: 2000 + int64(family), Family: family}
	in.View.Kind = r.parentKind
	return in
}
