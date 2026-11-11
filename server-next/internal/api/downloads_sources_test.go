package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// fetchWhole fetches every file of a downloaded title and checks each against what the
// device is told: its size, and its SHA-256 for a copy. It returns the files' bytes.
func (rig *downloadRig) fetchWhole(token, download, item string) (DownloadedItem, [][]byte) {
	rig.Helper()
	var out DownloadedItem
	if w := call(rig.T, rig.s, "GET", "/v1/downloads/"+download+"/items/"+item, token, nil, &out); w.Code != 200 {
		rig.Fatalf("the title's files answered %d: %s", w.Code, w.Body)
	}
	var bodies [][]byte
	for _, file := range out.Files {
		res, body := rig.fetch(file.URL)
		sum := sha256.Sum256(body)
		if res.StatusCode != 200 || int64(len(body)) != file.SizeBytes || file.SHA256 != "" && hex.EncodeToString(sum[:]) != file.SHA256 {
			rig.Fatalf("%s answered %d with %d bytes; described as %+v", file.URL, res.StatusCode, len(body), file)
		}
		bodies = append(bodies, body)
	}
	return out, bodies
}

// probe asks ffprobe one thing about a file's first audio or video stream, or its length.
func probe(t *testing.T, raw []byte, entries string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "copy")
	os.WriteFile(path, raw, 0o600)
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", entries, "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe could not read the copy: %v", err)
	}
	return strings.TrimSpace(strings.Split(string(out), "\n")[0])
}

// A film kept on remote storage (a WebDAV share) downloads like any other: the original is
// read from the share by range, and a smaller copy is made from it through the share, its
// subtitle included.
func TestACopyIsMadeFromAFileOnRemoteStorage(t *testing.T) {
	rig := newDownloadRig(t)
	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	clip := filepath.Join(t.TempDir(), "remote.mkv")
	makeClip(t, clip, "From the share")
	raw, _ := os.ReadFile(clip)
	dav := remotetest.NewDAV(t, map[string]string{"Films/remote.mkv": string(raw)})
	var src RemoteSource
	add := RemoteSourceCreate{Kind: "webdav", Name: "NAS", WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}
	if w := call(t, rig.s, "POST", "/v1/admin/remote-sources", rig.owner, add, &src); w.Code != 200 {
		t.Fatalf("adding the share answered %d: %s", w.Code, w.Body)
	}
	ctx := context.Background()
	row, err := rig.s.db.RemoteSource(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := rig.s.db.CreateLibrary(ctx, "Remote films", db.LibraryMovies, "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := rig.s.db.AddRemoteFolder(ctx, lib.ID, row.ID, "Films")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: folder.ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: db.FileRef{Name: "remote.mkv"}, Role: db.FileMedia, Size: int64(len(raw)), ModifiedNS: time.Unix(1700000000, 0).UnixNano(),
			Container: "matroska", DurationMS: 2000, Bitrate: 3_000_000, Streams: []db.Stream{
				{Kind: db.StreamVideo, Codec: "h264", Width: 640, Height: 360}, {Kind: db.StreamAudio, Codec: "aac", Channels: 1, SampleRate: 44100},
				{Kind: db.StreamSubtitle, Codec: "subrip", Language: "eng"}}}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "remote"}}, Title: "Remote",
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "remote.mkv"}}}}}}}}); err != nil {
		t.Fatal(err)
	}
	film := get[ItemPage](t, rig.s, rig.owner, "/v1/items?library="+lib.PublicID+"&kind=movie").Items[0].ID

	_, original := rig.ask(rig.owner, DownloadAsk{ItemID: film, Quality: "original"})
	if _, bodies := rig.fetchWhole(rig.owner, original.ID, film); string(bodies[0]) != string(raw) {
		t.Fatal("the original read from the share is not the file on it")
	}
	_, smaller := rig.ask(rig.owner, DownloadAsk{ItemID: film, Quality: "240p"})
	rig.waitReady(rig.owner, smaller.ID)
	item, bodies := rig.fetchWhole(rig.owner, smaller.ID, film)
	if height, _ := strconv.Atoi(probe(t, bodies[0], "stream=height")); height < 230 || height > 240 {
		t.Fatalf("the copy from the share is %d lines high", height)
	}
	if len(item.Subtitles) != 1 || item.Subtitles[0].URL == "" {
		t.Fatalf("the copy from the share has subtitles %+v", item.Subtitles)
	}
	if res, body := rig.fetch(item.Subtitles[0].URL); res.StatusCode != 200 || !strings.Contains(string(body), "From the share") {
		t.Fatalf("the subtitle from the share answered %d %q", res.StatusCode, body)
	}
}

// A song kept as FLAC and an audiobook kept as two MP3 files can be downloaded at 96 kbps:
// each becomes AAC of the same length, the audiobook one copy per file, in order.
func TestSongsAndAudiobooksAreMadeSmaller(t *testing.T) {
	rig := newDownloadRig(t)
	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	ctx := context.Background()
	folder := t.TempDir()
	audio := func(name string, seconds, frequency int, codec ...string) (int64, int64) {
		path := filepath.Join(folder, name)
		args := append([]string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=" + strconv.Itoa(frequency) + ":duration=" + strconv.Itoa(seconds),
			"-ac", "2", "-ar", "44100"}, append(codec, path)...)
		if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("making %s failed: %v %s", name, err, out)
		}
		info, _ := os.Stat(path)
		return info.Size(), info.ModTime().UnixNano()
	}
	file := func(name, container string, bitrate int64, codec string, size, modified int64) db.ImportFile {
		return db.ImportFile{FileRef: db.FileRef{Name: name}, Role: db.FileMedia, Size: size, ModifiedNS: modified, Container: container,
			DurationMS: 3000, Bitrate: bitrate, Streams: []db.Stream{{Kind: db.StreamAudio, Codec: codec, Channels: 2, SampleRate: 44100,
				Bitrate: bitrate, Default: true}}}
	}
	songSize, songTime := audio("song.flac", 3, 440)
	music, _ := rig.s.db.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{folder})
	if _, err := rig.s.db.Import(ctx, db.ImportBatch{LibraryID: music.ID, PathID: music.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{file("song.flac", "flac", 900_000, "flac", songSize, songTime)},
		Items: []db.ImportItem{{Kind: db.KindTrack, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "song"}}, Title: "Song",
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "song.flac"}}}}}}}}); err != nil {
		t.Fatal(err)
	}
	oneSize, oneTime := audio("book-1.mp3", 3, 300, "-c:a", "libmp3lame", "-b:a", "128k")
	twoSize, twoTime := audio("book-2.mp3", 3, 500, "-c:a", "libmp3lame", "-b:a", "128k")
	books, _ := rig.s.db.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{folder})
	if _, err := rig.s.db.Import(ctx, db.ImportBatch{LibraryID: books.ID, PathID: books.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{file("book-1.mp3", "mp3", 128_000, "mp3", oneSize, oneTime), file("book-2.mp3", "mp3", 128_000, "mp3", twoSize, twoTime)},
		Items: []db.ImportItem{{Kind: db.KindAudiobook, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "book"}}, Title: "Book",
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "book-1.mp3"}}, {File: db.FileRef{Name: "book-2.mp3"}}}}}}}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		library, kind string
		parts         int
	}{{music.PublicID, "track", 1}, {books.PublicID, "audiobook", 2}} {
		title := get[ItemPage](t, rig.s, rig.owner, "/v1/items?library="+c.library+"&kind="+c.kind).Items[0].ID
		code, dl := rig.ask(rig.owner, DownloadAsk{ItemID: title, Quality: "96"})
		if code != 201 || dl.Counts.Titles != 1 || dl.Counts.Failed != 0 {
			t.Fatalf("asking for the %s at 96 answered %d %+v", c.kind, code, dl)
		}
		rig.waitReady(rig.owner, dl.ID)
		_, bodies := rig.fetchWhole(rig.owner, dl.ID, title)
		if len(bodies) != c.parts {
			t.Fatalf("the %s's copy has %d files; want %d", c.kind, len(bodies), c.parts)
		}
		for _, body := range bodies {
			if codec := probe(t, body, "stream=codec_name"); codec != "aac" {
				t.Fatalf("the %s's copy is %s, not AAC", c.kind, codec)
			}
			if seconds, _ := strconv.ParseFloat(probe(t, body, "format=duration"), 64); seconds < 2.8 || seconds > 3.2 {
				t.Fatalf("the %s's copy lasts %.2f s; the original lasts 3", c.kind, seconds)
			}
		}
	}
}
