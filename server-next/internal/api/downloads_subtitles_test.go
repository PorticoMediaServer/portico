package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// The subtitles written beside a copy take room on the disk, so they count against the
// owner's download space like the copy itself.
func TestWrittenSubtitlesCountAgainstTheDownloadSpace(t *testing.T) {
	rig := newDownloadRig(t)
	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	_, dl := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	rig.waitReady(rig.owner, dl.ID)
	var onDisk int64
	entries, err := os.ReadDir(rig.s.prepare.Folder)
	if err != nil {
		t.Fatal(err)
	}
	subtitles := 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(e.Name()) == ".vtt" {
			subtitles++
		}
		onDisk += info.Size()
	}
	if subtitles == 0 {
		t.Fatal("no subtitle was written beside the copy")
	}
	used, err := rig.s.db.PreparedSpace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if used != onDisk {
		t.Fatalf("the download space counts %d bytes; the copy and its %d subtitle file(s) take %d", used, subtitles, onDisk)
	}
}

// A film with several text subtitles inside it, kept on remote storage, is read from the
// source about twice when its copy is made (once to convert, once for all its subtitles),
// not once more for every subtitle track.
func TestACopysSubtitlesAreReadInOnePass(t *testing.T) {
	rig := newDownloadRig(t)
	if !rig.s.prepare.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	folder := t.TempDir()
	clip := filepath.Join(folder, "many.mkv")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "nullsrc=size=480x272:rate=25:duration=3,geq=lum=random(1)*255:cb=128:cr=128",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3"}
	maps := []string{"-map", "0", "-map", "1"}
	for n, lang := range []string{"eng", "fra", "deu"} {
		srt := filepath.Join(folder, lang+".srt")
		os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,500\nIn "+lang+"\n"), 0o600)
		args = append(args, "-i", srt)
		maps = append(maps, "-map", string(rune('2'+n)))
	}
	args = append(args, maps...)
	// Noise, lossless, so the file is several MiB: more than the four 1 MiB blocks the source keeps.
	args = append(args, "-c:v", "libx264", "-qp", "0", "-c:a", "aac", "-c:s", "srt", "-shortest", clip)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("making the clip failed: %v %s", err, out)
	}
	raw, _ := os.ReadFile(clip)
	blocks := int64(len(raw)+(1<<20)-1) >> 20
	if blocks <= 5 {
		t.Fatalf("the clip is only %d bytes; it must be larger than the source's four kept blocks", len(raw))
	}
	dav := remotetest.NewDAV(t, map[string]string{"Films/many.mkv": string(raw)})
	var src RemoteSource
	add := RemoteSourceCreate{Kind: "webdav", Name: "NAS", Limits: &RemoteLimits{Connections: 8, PerSecond: 1000},
		WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}
	if w := call(t, rig.s, "POST", "/v1/admin/remote-sources", rig.owner, add, &src); w.Code != 200 {
		t.Fatalf("adding the share answered %d: %s", w.Code, w.Body)
	}
	ctx := context.Background()
	row, err := rig.s.db.RemoteSource(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Made with its folder, as the owner makes one: a library always has a folder.
	lib, err := rig.s.db.NewLibrary(ctx, db.NewLibrary{Name: "Remote films", Kind: db.LibraryMovies, Language: "en",
		RemoteFolders: []db.RemoteFolder{{SourceID: row.ID, Path: "Films"}}})
	if err != nil {
		t.Fatal(err)
	}
	where := lib.Paths[0]
	streams := []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Width: 480, Height: 272}, {Kind: db.StreamAudio, Codec: "aac", Channels: 1, SampleRate: 44100}}
	for _, lang := range []string{"eng", "fra", "deu"} {
		streams = append(streams, db.Stream{Kind: db.StreamSubtitle, Codec: "subrip", Language: lang})
	}
	if _, err := rig.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: where.ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: db.FileRef{Name: "many.mkv"}, Role: db.FileMedia, Size: int64(len(raw)), ModifiedNS: time.Unix(1700000000, 0).UnixNano(),
			Container: "matroska", DurationMS: 3000, Bitrate: 3_000_000, Streams: streams}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "many"}}, Title: "Many",
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "many.mkv"}}}}}}}}); err != nil {
		t.Fatal(err)
	}
	film := get[ItemPage](t, rig.s, rig.owner, "/v1/items?library="+lib.PublicID+"&kind=movie").Items[0].ID
	before := dav.Reads.Load()
	_, smaller := rig.ask(rig.owner, DownloadAsk{ItemID: film, Quality: "240p"})
	rig.waitReady(rig.owner, smaller.ID)
	reads := dav.Reads.Load() - before
	item, _ := rig.fetchWhole(rig.owner, smaller.ID, film)
	if len(item.Subtitles) != 3 {
		t.Fatalf("the copy has subtitles %+v, want all three", item.Subtitles)
	}
	// One pass to convert and one for the subtitles is about 2*blocks; allow a few more for
	// opening the file and probing its start and end.
	if limit := 2*blocks + 8; reads > limit {
		t.Fatalf("making the copy of a %d-block file read %d blocks from the source; one pass for all subtitles needs at most %d", blocks, reads, limit)
	}
}
