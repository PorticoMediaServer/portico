package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// importAlbum adds an artist with one album of one track. The file need not exist.
func (rig *downloadRig) importAlbum() string {
	rig.Helper()
	ctx := context.Background()
	lib, err := rig.s.db.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{rig.TempDir()})
	if err != nil {
		rig.Fatal(err)
	}
	one := 1
	artist := db.ItemKey{Kind: db.KeyFolder, Key: "artist"}
	album := db.ItemKey{Kind: db.KeyName, Key: "artist/album"}
	if _, err := rig.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: db.FileRef{Name: "t1.flac"}, Role: db.FileMedia, Size: 1000, Container: "flac", DurationMS: 60_000,
			Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "flac", Channels: 2, SampleRate: 44100}}}},
		Items: []db.ImportItem{
			{Kind: db.KindArtist, Keys: []db.ItemKey{artist}, Title: "The Band"},
			{Kind: db.KindAlbum, Keys: []db.ItemKey{album}, Parent: &artist, Title: "First Album"},
			{Kind: db.KindTrack, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "t1.flac"}}, Parent: &album, Title: "Opening", IndexNumber: &one,
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "t1.flac"}}}}}}}}); err != nil {
		rig.Fatal(err)
	}
	return get[ItemPage](rig.T, rig.s, rig.owner, "/v1/items?library="+lib.PublicID+"&kind=album").Items[0].ID
}

// The Downloads page reads every request with its titles at once, each title with its show,
// season and episode, or its album and artist; a title the profile may no longer see keeps
// its row but says nothing more about where it sits.
func TestTheDownloadsListCarriesEveryTitleAndWhereItSits(t *testing.T) {
	rig := newDownloadRig(t)
	show := rig.importShow(1, 2)
	album := rig.importAlbum()
	for _, item := range []string{show, album, rig.film} {
		if code, _ := rig.ask(rig.owner, DownloadAsk{ItemID: item, Quality: "original"}); code != 201 {
			t.Fatalf("asking for %s answered %d", item, code)
		}
	}
	list := get[Page[Download]](t, rig.s, rig.owner, "/v1/downloads").Items
	byKind := map[string]Download{}
	for _, dl := range list {
		byKind[dl.Target.Kind] = dl
	}
	episodes := byKind["show"].Items
	if len(list) != 3 || len(episodes) != 2 {
		t.Fatalf("the list is %+v", list)
	}
	for n, e := range episodes {
		if e.ParentTitle != "Show" || e.SeasonNumber == nil || *e.SeasonNumber != 1 || e.EpisodeNumber == nil || *e.EpisodeNumber != n+1 {
			t.Errorf("episode %d is listed as %+v", n+1, e)
		}
	}
	if tracks := byKind["album"].Items; len(tracks) != 1 || tracks[0].ParentTitle != "First Album" || tracks[0].Artist != "The Band" {
		t.Errorf("the album's track is listed as %+v", tracks)
	}
	if films := byKind["movie"].Items; len(films) != 1 || films[0].ParentTitle != "" || films[0].SeasonNumber != nil {
		t.Errorf("the film is listed as %+v", films)
	}
	// The detail read gives the same titles.
	if detail := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+byKind["show"].ID); len(detail.Items) != 2 || detail.Items[1].ItemID != episodes[1].ItemID ||
		detail.Items[1].EpisodeNumber == nil || *detail.Items[1].EpisodeNumber != 2 || detail.Items[1].ParentTitle != "Show" {
		t.Errorf("the detail read gives %+v", detail.Items)
	}

	// A profile limited to the films library after asking for the show keeps the rows, with
	// no show, season or episode named.
	var partner Profile
	call(t, rig.s, "POST", "/v1/profiles", rig.owner, ProfileCreate{Name: "Partner"}, &partner)
	var onPartner Session
	call(t, rig.s, "POST", "/v1/profiles/"+partner.ID+"/select", signIn(t, rig.s, 2).Session.AccessToken, ProfileSelect{}, &onPartner)
	if code, _ := rig.ask(onPartner.AccessToken, DownloadAsk{ItemID: show, Quality: "original"}); code != 201 {
		t.Fatalf("the partner asking answered %d", code)
	}
	limited := Restrictions{Libraries: []string{rig.films.PublicID}}
	if w := call(t, rig.s, "PUT", "/v1/profiles/"+partner.ID+"/restrictions", rig.owner, limited, nil); w.Code != 200 {
		t.Fatalf("limiting the partner answered %d: %s", w.Code, w.Body)
	}
	hidden := get[Page[Download]](t, rig.s, onPartner.AccessToken, "/v1/downloads").Items
	if len(hidden) != 1 || len(hidden[0].Items) != 2 {
		t.Fatalf("the partner's list is %+v", hidden)
	}
	for _, e := range hidden[0].Items {
		if e.ParentTitle != "" || e.SeasonNumber != nil || e.EpisodeNumber != nil {
			t.Errorf("a title the partner may not see says where it sits: %+v", e)
		}
	}
}

// A title taken out of a download is gone from the list and its count, the device is told to
// remove it, and it does not come back: not under "keep the next", which takes the one after
// instead, and not as something to fetch. Taking it out again, or out of a download that is
// not there, succeeds.
func TestATitleTakenOutOfADownloadIsRemovedAndStaysOut(t *testing.T) {
	rig := newDownloadRig(t)
	show := rig.importShow(1, 4)
	sync := func(holds []HeldTitle) DownloadSyncReply {
		t.Helper()
		var out DownloadSyncReply
		if w := call(t, rig.s, "POST", "/v1/downloads/sync", rig.owner, DownloadSync{Holds: holds}, &out); w.Code != 200 {
			t.Fatalf("syncing answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	drop := func(download, item string) {
		t.Helper()
		for range 2 {
			if w := call(t, rig.s, "DELETE", "/v1/downloads/"+download+"/items/"+item, rig.owner, nil, nil); w.Code != 204 {
				t.Fatalf("taking a title out answered %d: %s", w.Code, w.Body)
			}
		}
	}
	_, next := rig.ask(rig.owner, DownloadAsk{ItemID: show, Quality: "original", Selection: "next", KeepNext: 3})
	holds := sync(nil).Fetch
	episodes := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+next.ID).Items
	if len(holds) != 3 || len(episodes) != 3 {
		t.Fatalf("keep the next 3 holds %+v", episodes)
	}
	drop(next.ID, episodes[1].ItemID)
	if after := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+next.ID); after.Counts.Titles != 2 || len(after.Items) != 2 {
		t.Fatalf("after taking episode 2 out the download is %+v", after)
	}
	told := sync(holds)
	if len(told.Remove) != 1 || told.Remove[0].ItemID != episodes[1].ItemID || told.Remove[0].Reason != "removed" ||
		len(told.Fetch) != 1 || told.Fetch[0].ItemID == episodes[1].ItemID {
		t.Fatalf("the device was told %+v", told)
	}
	holds = append([]HeldTitle{holds[0], holds[2]}, told.Fetch...)
	if again := sync(holds); len(again.Remove) != 0 || len(again.Fetch) != 0 {
		t.Fatalf("the next contact told %+v", again)
	}
	titles := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+next.ID).Items
	if len(titles) != 3 || titles[0].Title != "Episode 1" || titles[1].Title != "Episode 3" || titles[2].Title != "Episode 4" {
		t.Fatalf("keep the next 3 now holds %+v", titles)
	}

	// From a download of everything: not offered to a device that does not hold it yet.
	_, film := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "original"})
	drop(film.ID, rig.film)
	for _, f := range sync(holds).Fetch {
		if f.DownloadID == film.ID {
			t.Fatalf("a title taken out was offered: %+v", f)
		}
	}
	drop("dl_none", rig.film)
}
