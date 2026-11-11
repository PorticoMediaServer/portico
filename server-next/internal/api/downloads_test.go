package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

// The sandbox that makes download copies starts this test binary again as its helper.
func TestMain(m *testing.M) {
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// downloadRig is a server with a real two-second film (rated 16) and a subtitle file beside
// it, the owner on the main profile, and a kid's profile limited to 12 on another device.
type downloadRig struct {
	*testing.T
	s          *Server
	owner, kid string
	film       string
	clip       []byte
	shows      *db.Library
	films      db.Library
	folder     string
}

// makeClip makes a two-second 640x360 film with sound and an English text subtitle inside
// it that says what it is given.
func makeClip(t *testing.T, path, subtitle string) {
	t.Helper()
	srt := filepath.Join(t.TempDir(), "inside.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,500\n"+subtitle+"\n"), 0o600)
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-i", srt, "-map", "0", "-map", "1", "-map", "2",
		"-c:v", "libx264", "-b:v", "3M", "-c:a", "aac", "-c:s", "srt", "-metadata:s:s:0", "language=eng", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("making the clip failed: %v %s", err, out)
	}
}

func newDownloadRig(t *testing.T) *downloadRig {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	folder := t.TempDir()
	clip := filepath.Join(folder, "film.mkv")
	makeClip(t, clip, "Inside")
	os.WriteFile(filepath.Join(folder, "film.en.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0o600)
	s := newTestServer(t)
	rig := &downloadRig{T: t, s: s, owner: onMain(t, s, setup(t, s)).AccessToken, folder: folder}
	rig.clip, _ = os.ReadFile(clip)
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	rig.films = lib
	stat := func(name string) (int64, int64) {
		info, _ := os.Stat(filepath.Join(folder, name))
		return info.Size(), info.ModTime().UnixNano()
	}
	filmSize, filmTime := stat("film.mkv")
	srtSize, srtTime := stat("film.en.srt")
	age := 16
	_, err = s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{
			{FileRef: db.FileRef{Name: "film.mkv"}, Role: db.FileMedia, Size: filmSize, ModifiedNS: filmTime, Container: "matroska",
				DurationMS: 2000, Bitrate: 3_000_000, Streams: []db.Stream{
					{Kind: db.StreamVideo, Codec: "h264", Width: 640, Height: 360, BitDepth: 8},
					{Kind: db.StreamAudio, Codec: "aac", Channels: 1, SampleRate: 44100, Default: true},
					{Kind: db.StreamSubtitle, Codec: "subrip", Language: "eng"}}},
			{FileRef: db.FileRef{Name: "film.en.srt"}, Role: db.FileSubtitle, Size: srtSize, ModifiedNS: srtTime,
				Streams: []db.Stream{{Kind: db.StreamSubtitle, Codec: "subrip", Language: "eng"}}}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "film"}}, Title: "Film", RatingAge: &age,
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "film.mkv"}}},
				Sidecars: []db.FileRef{{Name: "film.en.srt"}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	page := get[ItemPage](t, s, rig.owner, "/v1/items?library="+lib.PublicID+"&kind=movie")
	if len(page.Items) != 1 {
		t.Fatal("the film was not imported")
	}
	rig.film = page.Items[0].ID
	var kid Profile
	call(t, s, "POST", "/v1/profiles", rig.owner, ProfileCreate{Name: "Kid"}, &kid)
	limit := 12
	if w := call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", rig.owner, Restrictions{MaxRatingAge: &limit, AllLibraries: true}, nil); w.Code != 200 {
		t.Fatalf("limiting the kid answered %d: %s", w.Code, w.Body)
	}
	var onKid Session
	if w := call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the kid's profile answered %d", w.Code)
	}
	rig.kid = onKid.AccessToken
	return rig
}

func (rig *downloadRig) ask(token string, body DownloadAsk) (int, Download) {
	rig.Helper()
	var out Download
	w := call(rig.T, rig.s, "POST", "/v1/downloads", token, body, &out)
	return w.Code, out
}

// fetch sends a GET for a media address with optional headers, and no sign-in.
func (rig *downloadRig) fetch(url string, headers ...string) (*http.Response, []byte) {
	r := newRequest("GET", url, "", nil)
	for n := 0; n+1 < len(headers); n += 2 {
		r.Header.Set(headers[n], headers[n+1])
	}
	res := serve(rig.s, r).Result()
	body, _ := io.ReadAll(res.Body)
	return res, body
}

// Someone downloads a film at Original: asking twice is one download, the device fetches the
// file by range from an address that needs no sign-in and carries on where it stopped, and
// the subtitle beside it comes too. A kid limited below the film's rating cannot download
// it or learn it exists, a TV cannot download, and once the download is removed its address
// stops working.
func TestAnOriginalDownloadIsFetchedByRangeAndOnlyByWhoMaySeeIt(t *testing.T) {
	rig := newDownloadRig(t)
	code, first := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "original"})
	if code != 201 || first.Counts.Ready != 1 || first.Counts.Titles != 1 {
		t.Fatalf("asking answered %d %+v", code, first)
	}
	if code, again := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "original"}); code != 200 || again.ID != first.ID {
		t.Fatalf("asking again answered %d %+v", code, again)
	}
	if code, _ := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "original", Selection: "unwatched"}); code != 409 {
		t.Fatalf("asking again with other choices answered %d; want 409", code)
	}
	var item DownloadedItem
	if w := call(t, rig.s, "GET", "/v1/downloads/"+first.ID+"/items/"+rig.film, rig.owner, nil, &item); w.Code != 200 || len(item.Files) != 1 {
		t.Fatalf("the title's files answered %d: %s", w.Code, w.Body)
	}
	if item.Files[0].SizeBytes != int64(len(rig.clip)) || item.Item.Title != "Film" {
		t.Fatalf("the file is described wrongly: %+v", item.Files[0])
	}
	res, body := rig.fetch(item.Files[0].URL, "Range", "bytes=100-199")
	if res.StatusCode != 206 || string(body) != string(rig.clip[100:200]) {
		t.Fatalf("a range answered %d with %d bytes", res.StatusCode, len(body))
	}
	// Carrying on after a lost connection: the same file still, so the rest comes.
	res, body = rig.fetch(item.Files[0].URL, "Range", "bytes=200-", "If-Range", res.Header.Get("ETag"))
	if res.StatusCode != 206 || string(body) != string(rig.clip[200:]) {
		t.Fatalf("carrying on answered %d with %d bytes", res.StatusCode, len(body))
	}
	var subtitle string
	for _, s := range item.Subtitles {
		if s.Source == "sidecar" {
			subtitle = s.URL
		}
	}
	if res, body := rig.fetch(subtitle); res.StatusCode != 200 || !strings.Contains(string(body), "Hello") {
		t.Fatalf("the subtitle beside the film answered %d %q", res.StatusCode, body)
	}

	for _, path := range []string{"/v1/items/" + rig.film + "/download-options", "/v1/downloads/" + first.ID,
		"/v1/downloads/" + first.ID + "/items/" + rig.film} {
		if w := call(t, rig.s, "GET", path, rig.kid, nil, nil); w.Code != 404 {
			t.Fatalf("the kid's %s answered %d", path, w.Code)
		}
	}
	if code, _ := rig.ask(rig.kid, DownloadAsk{ItemID: rig.film, Quality: "original"}); code != 404 {
		t.Fatalf("the kid asking for a film above their limit answered %d", code)
	}
	var tv SignInResponse
	tvDevice := device(7)
	tvDevice.Platform = "tvos"
	call(t, rig.s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "justin", Password: testPassword, Device: tvDevice}, &tv)
	onTV := onMain(t, rig.s, tv).AccessToken
	if code, _ := rig.ask(onTV, DownloadAsk{ItemID: rig.film, Quality: "original"}); code != 403 {
		t.Fatalf("a TV asking to download answered %d", code)
	}
	if w := call(t, rig.s, "GET", "/v1/downloads?scope=profile", onTV, nil, nil); w.Code != 403 {
		t.Fatalf("a TV listing downloads answered %d", w.Code)
	}

	if w := call(t, rig.s, "DELETE", "/v1/downloads/"+first.ID, rig.owner, nil, nil); w.Code != 204 {
		t.Fatalf("removing answered %d", w.Code)
	}
	if res, _ := rig.fetch(item.Files[0].URL); res.StatusCode != 404 {
		t.Fatalf("a removed download's address answered %d", res.StatusCode)
	}
}

// Two profiles ask for the same film at 240p: the server prepares it once, both are ready
// with the same copy counted once, and the file fetched is the one whose hash the device
// is told. A browser saves it under the title's name.
func TestASmallerCopyIsPreparedOnceForEveryoneWhoAsks(t *testing.T) {
	rig := newDownloadRig(t)
	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	var options DownloadOptions
	call(t, rig.s, "GET", "/v1/items/"+rig.film+"/download-options", rig.owner, nil, &options)
	if len(options.Items) != 2 || options.Items[1].Quality != "240p" || !options.Items[1].Prepared || !options.Items[1].Available {
		t.Fatalf("a 360-line film offers %+v", options.Items)
	}
	code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	if code != 201 {
		t.Fatalf("asking answered %d", code)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for dl.Counts.Ready == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		call(t, rig.s, "GET", "/v1/downloads/"+dl.ID, rig.owner, nil, &dl)
		if dl.Counts.Failed > 0 {
			t.Fatalf("the copy failed: %+v", dl.Items)
		}
	}
	if dl.Counts.Ready != 1 || dl.Items[0].State != "ready" || dl.Items[0].KeptUntil == nil {
		t.Fatalf("the copy was not made: %+v", dl)
	}

	var partner Profile
	call(t, rig.s, "POST", "/v1/profiles", rig.owner, ProfileCreate{Name: "Partner"}, &partner)
	var onPartner Session
	call(t, rig.s, "POST", "/v1/profiles/"+partner.ID+"/select", signIn(t, rig.s, 2).Session.AccessToken, ProfileSelect{}, &onPartner)
	code, theirs := rig.ask(onPartner.AccessToken, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	if code != 201 || theirs.Counts.Ready != 1 {
		t.Fatalf("a second person waited for a copy already made: %d %+v", code, theirs)
	}
	used, _ := rig.s.db.PreparedSpace(context.Background())
	if used != dl.Items[0].SizeBytes {
		t.Fatalf("the server's space counts %d for one copy of %d", used, dl.Items[0].SizeBytes)
	}
	var item DownloadedItem
	call(t, rig.s, "GET", "/v1/downloads/"+theirs.ID+"/items/"+rig.film, onPartner.AccessToken, nil, &item)
	if len(item.Files) != 1 || item.Files[0].SHA256 == "" {
		t.Fatalf("the copy's file is not described: %+v", item.Files)
	}
	res, body := rig.fetch(item.Files[0].URL + "?save=1")
	sum := sha256.Sum256(body)
	if res.StatusCode != 200 || hex.EncodeToString(sum[:]) != item.Files[0].SHA256 || int64(len(body)) != item.Files[0].SizeBytes {
		t.Fatalf("the copy fetched is not the one described: %d, %d bytes", res.StatusCode, len(body))
	}
	if disposition := res.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "Film.mp4") {
		t.Fatalf("a browser is not told to save it as Film.mp4: %q", disposition)
	}
	// The copy has no subtitle track: the one inside the film comes beside it, as WebVTT,
	// with the subtitle file that was already beside the film.
	var inside, beside string
	for _, sub := range item.Subtitles {
		if sub.Source == "embedded" {
			inside = sub.URL
		} else {
			beside = sub.URL
		}
	}
	if inside == "" || beside == "" {
		t.Fatalf("the copy's subtitles are %+v", item.Subtitles)
	}
	if res, body := rig.fetch(inside); res.StatusCode != 200 || !strings.HasPrefix(string(body), "WEBVTT") || !strings.Contains(string(body), "Inside") {
		t.Fatalf("the subtitle from inside the film answered %d %q", res.StatusCode, body)
	}
}

// A download address belongs to one sign-in. Once that sign-in ends (the person signs out on
// the device, or the owner signs the device out), the address stops at its next range, as it
// does when the download is removed.
func TestADownloadAddressStopsWhenItsSignInEnds(t *testing.T) {
	rig := newDownloadRig(t)
	code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "original"})
	if code != 201 {
		t.Fatalf("asking answered %d", code)
	}
	var item DownloadedItem
	if w := call(t, rig.s, "GET", "/v1/downloads/"+dl.ID+"/items/"+rig.film, rig.owner, nil, &item); w.Code != 200 || len(item.Files) != 1 {
		t.Fatalf("the title's files answered %d: %s", w.Code, w.Body)
	}
	if res, _ := rig.fetch(item.Files[0].URL, "Range", "bytes=0-99"); res.StatusCode != 206 {
		t.Fatalf("the address answered %d before signing out", res.StatusCode)
	}
	if code := status(t, rig.s, "DELETE", "/v1/auth/session", rig.owner, nil); code != 204 {
		t.Fatalf("signing out answered %d", code)
	}
	if res, _ := rig.fetch(item.Files[0].URL, "Range", "bytes=100-199"); res.StatusCode != 404 {
		t.Fatalf("after signing out the download address answered %d; want 404", res.StatusCode)
	}
}

// Using a download address keeps it working from day to day, but not for ever: a week after
// it was given it stops, and the device asks for the title's addresses again.
func TestADownloadAddressDoesNotLiveForEver(t *testing.T) {
	rig := newDownloadRig(t)
	_, dl := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "original"})
	var item DownloadedItem
	call(t, rig.s, "GET", "/v1/downloads/"+dl.ID+"/items/"+rig.film, rig.owner, nil, &item)
	start := time.Now()
	defer func() { rig.s.now = time.Now }()
	// Used every 20 hours, it never sits idle for its day, so only its week can end it.
	for hours := 20; hours <= 160; hours += 20 {
		rig.s.now = func() time.Time { return start.Add(time.Duration(hours) * time.Hour) }
		if res, _ := rig.fetch(item.Files[0].URL, "Range", "bytes=0-9"); res.StatusCode != 206 {
			t.Fatalf("after %d hours of steady use the address answered %d", hours, res.StatusCode)
		}
	}
	rig.s.now = func() time.Time { return start.Add(180 * time.Hour) }
	if res, _ := rig.fetch(item.Files[0].URL, "Range", "bytes=0-9"); res.StatusCode != 404 {
		t.Fatalf("after a week of steady use the address answered %d; want 404", res.StatusCode)
	}
}

// A device reaching the server learns what to fetch and that its downloads play for 30 more
// days. When its profile may no longer see a title, the device is told to remove that one;
// when Downloads is turned off for the profile, everything, and it may not ask again.
func TestADeviceIsToldToRemoveWhatItsProfileLost(t *testing.T) {
	rig := newDownloadRig(t)
	var partner Profile
	call(t, rig.s, "POST", "/v1/profiles", rig.owner, ProfileCreate{Name: "Partner"}, &partner)
	var onPartner Session
	call(t, rig.s, "POST", "/v1/profiles/"+partner.ID+"/select", signIn(t, rig.s, 2).Session.AccessToken, ProfileSelect{}, &onPartner)
	token := onPartner.AccessToken
	code, dl := rig.ask(token, DownloadAsk{ItemID: rig.film, Quality: "original"})
	if code != 201 {
		t.Fatalf("asking answered %d", code)
	}
	sync := func(holds ...HeldTitle) DownloadSyncReply {
		t.Helper()
		var out DownloadSyncReply
		if w := call(t, rig.s, "POST", "/v1/downloads/sync", token, DownloadSync{Holds: holds}, &out); w.Code != 200 {
			t.Fatalf("syncing answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	held := HeldTitle{DownloadID: dl.ID, ItemID: rig.film}
	first := sync()
	if len(first.Fetch) != 1 || first.Fetch[0] != held || first.PlayableForMs != 30*24*3_600_000 ||
		first.PlayableUntil.Before(time.Now().Add(29*24*time.Hour)) {
		t.Fatalf("a device holding nothing was told %+v", first)
	}
	if again := sync(held); len(again.Fetch) != 0 || len(again.Remove) != 0 {
		t.Fatalf("a device holding its download was told %+v", again)
	}
	// A title that has left the server keeps playing: nothing says to remove it.
	if gone := sync(held, HeldTitle{DownloadID: dl.ID, ItemID: "itm_gone"}); len(gone.Remove) != 0 {
		t.Fatalf("a title no longer on the server was removed: %+v", gone.Remove)
	}

	limit := 12
	restrictions := "/v1/profiles/" + partner.ID + "/restrictions"
	call(t, rig.s, "PUT", restrictions, rig.owner, Restrictions{MaxRatingAge: &limit, AllLibraries: true}, nil)
	if lost := sync(held); len(lost.Remove) != 1 || lost.Remove[0].Reason != "no_access" || len(lost.Fetch) != 0 {
		t.Fatalf("a device whose profile lost the film was told %+v", lost)
	}

	off := false
	var saved Restrictions
	call(t, rig.s, "PUT", restrictions, rig.owner, Restrictions{AllLibraries: true, AllowDownloads: &off}, &saved)
	if saved.AllowDownloads == nil || *saved.AllowDownloads || !saved.AccountAllowDownloads {
		t.Fatalf("turning Downloads off for the profile saved %+v", saved)
	}
	if offNow := sync(held); len(offNow.Remove) != 1 || offNow.Remove[0].Reason != "downloads_off" {
		t.Fatalf("a device whose profile may no longer download was told %+v", offNow)
	}
	if code, _ := rig.ask(token, DownloadAsk{ItemID: rig.film, Quality: "original"}); code != 403 {
		t.Fatalf("asking with Downloads off answered %d", code)
	}

	// The owner sees the request by person and removes it; no device is reached.
	byPerson := get[DownloadsByPerson](t, rig.s, rig.owner, "/v1/admin/downloads")
	if len(byPerson.Items) != 1 || byPerson.Items[0].Titles != 1 {
		t.Fatalf("the owner's page lists %+v", byPerson.Items)
	}
	if w := call(t, rig.s, "DELETE", "/v1/admin/members/"+byPerson.Items[0].UserID+"/downloads", rig.owner, nil, nil); w.Code != 204 {
		t.Fatalf("removing their downloads answered %d", w.Code)
	}
	if after := get[DownloadsByPerson](t, rig.s, rig.owner, "/v1/admin/downloads"); len(after.Items) != 0 {
		t.Fatalf("their downloads were kept: %+v", after.Items)
	}
}

// importShow adds a show with episodes 1 to n of season 1 (or, called again, more of them).
// The files need not exist: nothing here fetches them.
func (rig *downloadRig) importShow(from, to int) string {
	rig.Helper()
	ctx := context.Background()
	if rig.shows == nil {
		lib, err := rig.s.db.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{rig.TempDir()})
		if err != nil {
			rig.Fatal(err)
		}
		rig.shows = &lib
	}
	lib := *rig.shows
	one := 1
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: int64(to), Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindShow, Keys: []db.ItemKey{{Kind: db.KeyFolder, Key: "show"}}, Title: "Show"},
		{Kind: db.KindSeason, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "show/1"}}, Parent: &db.ItemKey{Kind: db.KeyFolder, Key: "show"},
			Title: "Season 1", IndexNumber: &one}}}
	for n := from; n <= to; n++ {
		name := fmt.Sprintf("e%d.mkv", n)
		number := n
		batch.Files = append(batch.Files, db.ImportFile{FileRef: db.FileRef{Name: name}, Role: db.FileMedia, Size: 1000, Container: "matroska",
			DurationMS: 60_000, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Width: 640, Height: 360}}})
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: name}},
			Parent: &db.ItemKey{Kind: db.KeyName, Key: "show/1"}, Title: fmt.Sprintf("Episode %d", n), IndexNumber: &number, ParentIndex: &one,
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: name}}}}}})
	}
	if _, err := rig.s.db.Import(ctx, batch); err != nil {
		rig.Fatal(err)
	}
	shows := get[ItemPage](rig.T, rig.s, rig.owner, "/v1/items?library="+lib.PublicID+"&kind=show")
	return shows.Items[0].ID
}

// "Keep the next 3" on a show: the device is told to fetch the first three episodes; once
// one is watched it is told to remove it and fetch the fourth; an episode added later joins
// when its turn comes. With "Delete watched downloads" on, a watched film is removed too.
func TestKeepTheNextFollowsWhatIsWatchedAndWhatArrives(t *testing.T) {
	rig := newDownloadRig(t)
	show := rig.importShow(1, 4)
	code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: show, Quality: "original", Selection: "next", KeepNext: 3})
	if code != 201 || dl.Counts.Titles != 3 || dl.KeepNext != 3 {
		t.Fatalf("asking answered %d %+v", code, dl)
	}
	episodes := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID).Items
	sync := func(holds []HeldTitle) DownloadSyncReply {
		t.Helper()
		var out DownloadSyncReply
		if w := call(t, rig.s, "POST", "/v1/downloads/sync", rig.owner, DownloadSync{Holds: holds}, &out); w.Code != 200 {
			t.Fatalf("syncing answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	holds := sync(nil).Fetch
	if len(holds) != 3 || holds[0].ItemID != episodes[0].ItemID {
		t.Fatalf("a new device was told to fetch %+v", holds)
	}
	watched := true
	if w := call(t, rig.s, "PUT", "/v1/items/"+episodes[0].ItemID+"/state", rig.owner, map[string]any{"watched": watched}, nil); w.Code != 200 {
		t.Fatalf("marking watched answered %d: %s", w.Code, w.Body)
	}
	next := sync(holds)
	if len(next.Remove) != 1 || next.Remove[0].ItemID != episodes[0].ItemID || next.Remove[0].Reason != "watched" || len(next.Fetch) != 1 {
		t.Fatalf("after episode 1 was watched the device was told %+v", next)
	}
	holds = append(holds[1:], next.Fetch...)
	if titles := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID).Items; len(titles) != 3 || titles[2].Title != "Episode 4" {
		t.Fatalf("the request now holds %+v", titles)
	}

	rig.importShow(5, 5)
	call(t, rig.s, "PUT", "/v1/items/"+episodes[1].ItemID+"/state", rig.owner, map[string]any{"watched": watched}, nil)
	arrived := sync(holds)
	titles := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID).Items
	if len(arrived.Fetch) != 1 || len(titles) != 3 || titles[2].Title != "Episode 5" || arrived.Fetch[0].ItemID != titles[2].ItemID {
		t.Fatalf("a new episode did not join: told %+v, holding %+v", arrived, titles)
	}

	code, film := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "original"})
	held := HeldTitle{DownloadID: film.ID, ItemID: rig.film}
	call(t, rig.s, "PUT", "/v1/items/"+rig.film+"/state", rig.owner, map[string]any{"watched": watched}, nil)
	if kept := sync([]HeldTitle{held}); code != 201 || len(kept.Remove) != 0 {
		t.Fatalf("a watched film was removed with Delete watched downloads off: %+v", kept)
	}
	edit := PreferencesEdit{Scope: "profile-device", DeviceClass: "mobile", Values: map[string]any{"downloads.deleteWatched": true}}
	if w := call(t, rig.s, "PATCH", "/v1/preferences", rig.owner, edit, nil); w.Code != 200 {
		t.Fatalf("turning on Delete watched downloads answered %d: %s", w.Code, w.Body)
	}
	if gone := sync([]HeldTitle{held}); len(gone.Remove) != 1 || gone.Remove[0].Reason != "watched" {
		t.Fatalf("a watched film was kept with Delete watched downloads on: %+v", gone)
	}
}

// "Keep the next" is a rule for a download of the next unwatched titles; on a download of
// all of them it is refused rather than left to remove the rest. And a kept title whose copy
// the server removed before the device fetched it (a phone away from Wi-Fi past the owner's
// keeping time) is asked for again at the next sync.
func TestKeepTheNextAsksAgainForAnExpiredCopy(t *testing.T) {
	rig := newDownloadRig(t)
	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot convert")
	}
	show := rig.importShow(1, 2)
	_, all := rig.ask(rig.owner, DownloadAsk{ItemID: show, Quality: "original"})
	three := 3
	if w := call(t, rig.s, "PATCH", "/v1/downloads/"+all.ID, rig.owner, DownloadChange{KeepNext: &three}, nil); w.Code != 400 {
		t.Fatalf("keep the next on a download of all the titles answered %d", w.Code)
	}

	code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: show, Quality: "240p", Selection: "next", KeepNext: 1})
	if code != 201 || dl.Counts.Titles != 1 {
		t.Fatalf("asking answered %d %+v", code, dl)
	}
	// The episodes' files are not real, so the copy fails; either way it is not being made
	// when the keeping time runs out.
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if state := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID).Items[0].State; state != "waiting" && state != "preparing" {
			break
		}
	}
	rig.s.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	err := rig.s.prepare.Tidy(context.Background())
	rig.s.now = time.Now
	if err != nil {
		t.Fatal(err)
	}
	if state := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID).Items[0].State; state != "expired" {
		t.Fatalf("the swept copy reads %s", state)
	}
	if w := call(t, rig.s, "POST", "/v1/downloads/sync", rig.owner, DownloadSync{}, nil); w.Code != 200 {
		t.Fatalf("syncing answered %d", w.Code)
	}
	if state := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID).Items[0].State; state == "expired" {
		t.Fatal("a kept title whose copy expired was not asked for again")
	}
}

// waitReady reads a request until every title is ready, failing on a copy that failed.
func (rig *downloadRig) waitReady(token, id string) Download {
	rig.Helper()
	var dl Download
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		call(rig.T, rig.s, "GET", "/v1/downloads/"+id, token, nil, &dl)
		if dl.Counts.Failed > 0 {
			rig.Fatalf("a copy failed: %+v", dl.Items)
		}
		if dl.Counts.Ready == dl.Counts.Titles {
			return dl
		}
	}
	rig.Fatalf("the request was not ready in two minutes: %+v", dl)
	return dl
}

// A film kept as two files downloads whole: at Original the device is given both files in
// order, and a smaller copy is made of each part, each with its own hash.
func TestAFilmInTwoFilesDownloadsWhole(t *testing.T) {
	rig := newDownloadRig(t)
	ctx := context.Background()
	var files []db.ImportFile
	var parts []db.ImportPart
	var raw [][]byte
	for n, name := range []string{"split-part1.mkv", "split-part2.mkv"} {
		path := filepath.Join(rig.folder, name)
		makeClip(t, path, fmt.Sprintf("Part %d", n+1))
		info, _ := os.Stat(path)
		body, _ := os.ReadFile(path)
		raw = append(raw, body)
		files = append(files, db.ImportFile{FileRef: db.FileRef{Name: name}, Role: db.FileMedia, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(),
			Container: "matroska", DurationMS: 2000, Bitrate: 3_000_000, Streams: []db.Stream{
				{Kind: db.StreamVideo, Codec: "h264", Width: 640, Height: 360}, {Kind: db.StreamAudio, Codec: "aac", Channels: 1, SampleRate: 44100}}})
		parts = append(parts, db.ImportPart{File: db.FileRef{Name: name}})
	}
	if _, err := rig.s.db.Import(ctx, db.ImportBatch{LibraryID: rig.films.ID, PathID: rig.films.Paths[0].ID, Scan: 2, Dirs: []db.ImportDir{{}},
		Files: files, Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "split"}}, Title: "Split",
			Versions: []db.ImportVersion{{Parts: parts}}}}}); err != nil {
		t.Fatal(err)
	}
	var split string
	for _, item := range get[ItemPage](t, rig.s, rig.owner, "/v1/items?library="+rig.films.PublicID+"&kind=movie").Items {
		if item.Title == "Split" {
			split = item.ID
		}
	}

	_, original := rig.ask(rig.owner, DownloadAsk{ItemID: split, Quality: "original"})
	var item DownloadedItem
	call(t, rig.s, "GET", "/v1/downloads/"+original.ID+"/items/"+split, rig.owner, nil, &item)
	if len(item.Files) != 2 || item.Files[1].SizeBytes != int64(len(raw[1])) {
		t.Fatalf("the original's files are %+v", item.Files)
	}
	if res, body := rig.fetch(item.Files[1].URL); res.StatusCode != 200 || string(body) != string(raw[1]) {
		t.Fatalf("the second part answered %d with %d bytes", res.StatusCode, len(body))
	}

	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	_, smaller := rig.ask(rig.owner, DownloadAsk{ItemID: split, Quality: "240p"})
	rig.waitReady(rig.owner, smaller.ID)
	call(t, rig.s, "GET", "/v1/downloads/"+smaller.ID+"/items/"+split, rig.owner, nil, &item)
	if len(item.Files) != 2 || item.Files[1].SHA256 == "" {
		t.Fatalf("the copy's files are %+v", item.Files)
	}
	for _, file := range item.Files {
		res, body := rig.fetch(file.URL)
		sum := sha256.Sum256(body)
		if res.StatusCode != 200 || hex.EncodeToString(sum[:]) != file.SHA256 {
			t.Fatalf("a part of the copy is not the one described: %d", res.StatusCode)
		}
	}
}

// A copy nobody fetched for longer than the owner keeps them is removed and its title reads
// Expired; Try again makes it again and the title is ready to fetch.
func TestAnExpiredCopyIsMadeAgainOnRetry(t *testing.T) {
	rig := newDownloadRig(t)
	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	_, dl := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	rig.waitReady(rig.owner, dl.ID)
	rig.s.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if err := rig.s.prepare.Tidy(context.Background()); err != nil {
		t.Fatal(err)
	}
	rig.s.now = time.Now
	expired := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+dl.ID)
	if expired.Items[0].State != "expired" || expired.Counts.Ready != 0 {
		t.Fatalf("an unused copy past the keeping time reads %+v", expired.Items)
	}
	var item DownloadedItem
	if w := call(t, rig.s, "GET", "/v1/downloads/"+dl.ID+"/items/"+rig.film, rig.owner, nil, &item); w.Code == 200 {
		t.Fatal("an expired title still offered files")
	}
	if w := call(t, rig.s, "PATCH", "/v1/downloads/"+dl.ID, rig.owner, DownloadChange{Retry: true}, nil); w.Code != 200 {
		t.Fatalf("Try again answered %d: %s", w.Code, w.Body)
	}
	rig.waitReady(rig.owner, dl.ID)
	if w := call(t, rig.s, "GET", "/v1/downloads/"+dl.ID+"/items/"+rig.film, rig.owner, nil, &item); w.Code != 200 || len(item.Files) != 1 {
		t.Fatalf("after Try again the title's files answered %d", w.Code)
	}
}
