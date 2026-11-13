package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Albums get their format from the probed streams, including the audio bit depth that
// FromMedia must keep. No list row, path, client aggregation or rescan is involved.
func TestAlbumAudioComesFromVisibleSongsStoredProbeFacts(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	album := db.ItemKey{Kind: db.KeyName, Key: "album|formats"}
	revision := int64(0)
	store := func(codecs [2]string, depths [2]int, bitrates [2]int64) {
		t.Helper()
		revision++
		batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: revision, Dirs: []db.ImportDir{{}},
			Items: []db.ImportItem{{Kind: db.KindAlbum, Keys: []db.ItemKey{album}, Title: "Formats"}}}
		for n, codec := range codecs {
			name := fmt.Sprintf("%d.flac", n+1)
			facts := scan.FromMedia(mediafacts.Facts{Container: "flac", Duration: 60, Streams: []mediafacts.Stream{{
				Type: "audio", Codec: codec, Channels: 2, Detail: mediafacts.StreamDetail{BitsPerSample: depths[n], SampleRate: 96000, BitRate: bitrates[n]},
			}}})
			if codec == "" {
				facts.Streams = nil
			}
			file := db.FileRef{Name: name}
			batch.Files = append(batch.Files, db.ImportFile{FileRef: file, Role: db.FileMedia, Size: 100, ModifiedNS: revision,
				Container: facts.Container, DurationMS: facts.DurationMS, Streams: facts.Streams})
			track := db.ImportItem{Kind: db.KindTrack, Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}}, Parent: &album, Title: name,
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: file}}}}}
			if n == 1 {
				track.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Hidden"}}
			}
			batch.Items = append(batch.Items, track)
		}
		if _, err := s.db.Import(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	store([2]string{"flac", "flac"}, [2]int{24, 24}, [2]int64{800000, 900000})
	page := get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID+"&kind=album")
	if len(page.Items) != 1 || page.Items[0].AlbumAudio != nil {
		t.Fatalf("summary on a list row: %+v", page)
	}
	id := page.Items[0].ID
	read := func(token string) AlbumAudioSummary {
		t.Helper()
		var item Item
		w := call(t, s, "GET", "/v1/items/"+id, token, nil, &item)
		if w.Code != 200 || item.AlbumAudio == nil || strings.Contains(w.Body.String(), `"path"`) {
			t.Fatalf("album item: %d %s", w.Code, w.Body)
		}
		return *item.AlbumAudio
	}
	if got := read(owner); got.State != "common" || got.Codec != "flac" || got.BitDepth != 24 || got.SampleRate != 96000 || got.Bitrate != 0 {
		t.Fatalf("24/96 probe facts were lost or file bitrate was treated as format: %+v", got)
	}
	tracks := get[ItemPage](t, s, owner, "/v1/items?parent="+id+"&kind=track")
	track := get[Item](t, s, owner, "/v1/items/"+tracks.Items[0].ID)
	if track.AlbumAudio != nil || track.Versions[0].Parts[0].Streams[0].BitDepth != 24 {
		t.Fatalf("audio depth not stored on the stream, or summary on a song: %+v", track)
	}
	// Unknown depth must not turn FLAC 96 kHz into a guessed FLAC 24/96.
	store([2]string{"flac", "flac"}, [2]int{24, 0}, [2]int64{})
	if got := read(owner); got.State != "common" || got.Codec != "flac" || got.BitDepth != 0 || got.SampleRate != 96000 {
		t.Fatalf("unknown depth was guessed or known facts were lost: %+v", got)
	}
	store([2]string{"flac", "mp3"}, [2]int{24, 32}, [2]int64{800000, 320000})
	if got := read(owner); got.State != "mixed" || got.Codec != "" {
		t.Fatalf("mixed album: %+v", got)
	}
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %+v %v", labels, err)
	}
	var quiet Profile
	if w := call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Quiet"}, &quiet); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call(t, s, "PUT", "/v1/profiles/"+quiet.ID+"/restrictions", owner,
		Restrictions{AllLibraries: true, BlockedLabels: []string{labels[0].PublicID}}, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var selected Session
	if w := call(t, s, "POST", "/v1/profiles/"+quiet.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &selected); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if got := read(selected.AccessToken); got.State != "common" || got.Codec != "flac" || got.BitDepth != 24 {
		t.Fatalf("a hidden song affected the album format: %+v", got)
	}
	store([2]string{"mp3", "mp3"}, [2]int{32, 32}, [2]int64{320000, 320000})
	if got := read(owner); got.State != "common" || got.Codec != "mp3" || got.BitDepth != 0 || got.Bitrate != 320000 {
		t.Fatalf("lossy audio claimed a depth or lost its known bitrate: %+v", got)
	}
	store([2]string{"mp3", "mp3"}, [2]int{}, [2]int64{192000, 320000})
	if got := read(owner); got.State != "common" || got.Bitrate != 0 {
		t.Fatalf("variable bitrate: %+v", got)
	}
	store([2]string{"", ""}, [2]int{}, [2]int64{})
	if got := read(owner); got != (AlbumAudioSummary{State: "unknown"}) {
		t.Fatalf("unknown audio: %+v", got)
	}
}
