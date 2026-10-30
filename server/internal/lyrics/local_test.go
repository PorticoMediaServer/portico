package lyrics

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

type localReaderStub struct{}

func (localReaderStub) ReadLyrics(context.Context, string, string, int64, int64, string, string) (storage.LocalLyrics, error) {
	return storage.LocalLyrics{Candidates: []storage.LocalLyric{{Format: "text", Language: "und", Origin: "embedded", Label: "Embedded", Data: []byte("Plain text")}, {Format: "lrc", Language: "und", Origin: "sidecar", Label: "song.lrc", Data: []byte("odd credit\n[00:01]Local words")}}}, nil
}
func TestLocalLyricsAppearAndSurviveRetagging(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("library", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	album := c.Album(artist, "Album", 2020)
	song := c.Song(album, 1, "/music/song.flac", "Song")
	c.Drain()
	s := &Service{DB: c.DB, Local: localReaderStub{}}
	a := Access{Actor: Actor{Authority: "local", AccountID: "viewer", ProfileID: "profile"}, LibraryID: "library", ItemID: song.Public, Authorize: func(*sql.Tx) error { return nil }}
	view, e := s.View(context.Background(), a, Target{})
	if e != nil || len(view.Resources) != 1 || view.Resources[0].Provenance.Origin != "sidecar" {
		t.Fatalf("%+v %v", view, e)
	}
	old := view.Source.Version
	c.Exec(`UPDATE catalog_assets SET size=size+100,modified_ns=modified_ns+1 WHERE token=?`, song.Token)
	view, e = s.View(context.Background(), a, Target{})
	if e != nil || len(view.Resources) != 1 || view.Source.Version != old {
		t.Fatalf("retag %+v %v", view, e)
	}
}
func TestProviderIdentityMustMatch(t *testing.T) {
	d, _ := Parse([]byte("Words"), "text")
	v := acquired{doc: d, title: "Home", artist: "Artist", duration: 180}
	if !sameRecording(v, "Home", "Artist", 181) || sameRecording(v, "Home", "Other", 180) || sameRecording(v, "Intro", "Artist", 180) || sameRecording(v, "Home", "Artist", 195) {
		t.Fatal("wrong lyric identity accepted")
	}
}
