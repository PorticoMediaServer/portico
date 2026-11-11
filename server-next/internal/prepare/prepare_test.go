package prepare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// The encoder's sandbox starts this test binary again as its helper.
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

// A film in a library, as a scan would have recorded it: a real two-second clip made with
// ffmpeg, 640 by 360. wanting stores a request for a copy of it, which is what the queue
// makes copies for.
func filmLibrary(t *testing.T) (*db.DB, db.Version, func(db.Prepared)) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	folder := t.TempDir()
	clip := filepath.Join(folder, "film.mkv")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:v", "libx264", "-b:v", "3M", "-c:a", "aac", "-shortest", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("making the clip failed: %v %s", err, out)
	}
	info, _ := os.Stat(clip)
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	libraryfile.ServerData = t.TempDir()
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: db.FileRef{Name: "film.mkv"}, Role: db.FileMedia, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(),
			Container: "matroska", DurationMS: 2000, Bitrate: 3_000_000, Streams: []db.Stream{
				{Kind: db.StreamVideo, Codec: "h264", Width: 640, Height: 360, BitDepth: 8},
				{Kind: db.StreamAudio, Codec: "aac", Channels: 1, SampleRate: 44100, Default: true}}}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "film"}}, Title: "Film",
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "film.mkv"}}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := d.CreateOwner(ctx, db.NewOwner{Username: "owner", ProfileName: "Owner", Now: time.Now(),
		Session: db.NewSession{RefreshHash: "refresh", ExpiresAt: time.Now().Add(time.Hour), Device: db.DeviceInfo{InstallationID: "phone", Name: "Phone"}}})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := d.Profiles(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	viewer := db.Viewer{UserID: owner.UserID, ProfileID: profiles[0].ID}
	phone, err := d.SessionDevice(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Items(ctx, viewer, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindMovie}, Sort: db.SortTitle})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("the film was not imported", err)
	}
	item, err := d.PlaybackItem(ctx, viewer, page.Items[0].PublicID)
	if err != nil {
		t.Fatal(err)
	}
	wanting := func(p db.Prepared) {
		t.Helper()
		if _, _, err := d.CreateDownload(ctx, db.NewDownload{Viewer: viewer, DeviceID: phone.ID, ItemPublicID: item.PublicID, Quality: p.Recipe,
			Selection: "all", Titles: []db.DownloadTitle{{ItemID: item.ID, MediaID: p.MediaID, Prepared: &p}}}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return d, item.Versions[0].Version, wanting
}

func service(t *testing.T, d *db.DB, folder string, now func() time.Time) *Service {
	t.Helper()
	store, _, err := settings.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{DB: d, Folder: folder, Settings: store, Now: now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func waitReady(t *testing.T, d *db.DB, id int64) db.Prepared {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		p, err := d.PreparedByID(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if p.State == db.PreparedReady || p.State == db.PreparedFailed {
			return p
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the copy was not made in two minutes")
	return db.Prepared{}
}

// Someone asks for a smaller copy of a film: the server makes it once, as a real playable
// file whose hash is what a device checks, and a second person asking gets the same copy.
// Thirty days after anyone last asked for or fetched it, the copy and its file are gone,
// along with any file in the folder that no copy names.
func TestACopyIsMadeOnceSharedAndRemovedWhenUnused(t *testing.T) {
	d, film, wanting := filmLibrary(t)
	clock := time.Now()
	folder := t.TempDir()
	s := service(t, d, folder, func() time.Time { return clock })
	if !s.Converts() {
		t.Skip("this machine cannot run the sandboxed encoder")
	}
	if got := Recipes(film, db.KindMovie, "original"); len(got) != 1 || got[0] != "240p" {
		t.Fatalf("a 360-line film offers %v; want only 240p", got)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go s.Run(ctx)

	first, err := s.Want(ctx, film, db.KindMovie, "240p")
	if err != nil {
		t.Fatal(err)
	}
	wanting(first)
	s.Wake()
	ready := waitReady(t, d, first.ID)
	if ready.State != db.PreparedReady || ready.Container != "mp4" || ready.Size == 0 {
		t.Fatalf("the copy was not made: %+v", ready)
	}
	files := s.Files(ready)
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != ready.Size || hex.EncodeToString(sum[:]) != ready.ContentHash || !bytes.Contains(raw[:64], []byte("ftyp")) {
		t.Fatalf("the file is not the MP4 the row describes: %d bytes, row %+v", len(raw), ready)
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=height", "-of", "csv=p=0", files[0]).Output()
	if height, _ := strconv.Atoi(string(bytes.TrimSpace(probe))); err != nil || height < 230 || height > 240 {
		t.Fatalf("the copy is not about 240 lines high: %q %v", probe, err)
	}

	clock = clock.Add(10 * 24 * time.Hour)
	again, err := s.Want(ctx, film, db.KindMovie, "240p")
	if err != nil || again.ID != first.ID {
		t.Fatalf("a second request made another copy: %+v %v", again, err)
	}
	if used, _ := d.PreparedSpace(ctx); used != ready.Size {
		t.Fatalf("the space used is %d; the one copy is %d", used, ready.Size)
	}

	stray := filepath.Join(folder, "prp_nothing.mp4")
	os.WriteFile(stray, []byte("x"), 0o600)
	old := clock.Add(-2 * time.Hour)
	os.Chtimes(stray, old, old)
	clock = clock.Add(29 * 24 * time.Hour)
	if err := s.Tidy(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PreparedByID(ctx, first.ID); err != nil {
		t.Fatal("a copy asked for 29 days ago was removed")
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatal("a file no copy names was kept")
	}
	clock = clock.Add(2 * 24 * time.Hour)
	if err := s.Tidy(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PreparedByID(ctx, first.ID); err != db.ErrNotFound {
		t.Fatal("a copy nobody asked for in 31 days was kept")
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatal("the removed copy's file was kept")
	}
}
