package api

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

type scrubTestEngine struct {
	fakeEngine
	supervisor *processpool.Supervisor
	ffmpeg     string
}

func (e *scrubTestEngine) AnalysisTools(context.Context) (*processpool.Supervisor, string, error) {
	return e.supervisor, e.ffmpeg, nil
}

// A tiny generated clip follows the actual background job, confined decoder, picture store
// and HTTP path. A new profile's rating/library limits apply to both the manifest and sheets;
// deleting the library removes its references and makes the sheets eligible for normal cleanup.
func TestScrubPreviewsAreMadeInBackgroundAndRestricted(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "clip.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=2", "-t", "21", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated clip: %v %s", err, out)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	rating := 17
	ref := db.FileRef{Name: "clip.mkv"}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: ref, Role: db.FileMedia, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "matroska", DurationMS: 21000,
			Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Width: 160, Height: 90}}}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Title: "Generated clip", ContentRating: "R", RatingAge: &rating, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "clip"}},
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: ref}}}}}}}); err != nil {
		t.Fatal(err)
	}
	item := get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID).Items[0]
	route := "/v1/items/" + item.ID + "/scrub-previews"
	s.playback.SetEngine(&fakeEngine{}, &scrubTestEngine{supervisor: &processpool.Supervisor{Limit: 1}, ffmpeg: ffmpeg})
	if err := s.queueScrubPreviews(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.JobFor(ctx, "scrub_previews", lib.PublicID); err == nil {
		t.Fatal("default library started decoding previews")
	}
	if got := get[ScrubPreviews](t, s, owner, route); got.Available {
		t.Fatal("a request made previews")
	}
	for _, depth := range []string{db.DepthFiles, db.DepthBasic} {
		if err := s.db.SetLibraryOptions(ctx, lib.ID, db.LibraryOptions{Depth: depth, ScrubPreviews: true}); err != nil {
			t.Fatal(err)
		}
		if err := s.queueScrubPreviews(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.JobFor(ctx, "scrub_previews", lib.PublicID); err == nil {
			t.Fatalf("depth %s decoded", depth)
		}
	}
	if err := s.db.SetLibraryOptions(ctx, lib.ID, db.LibraryOptions{Depth: db.DepthComplete, ScrubPreviews: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.queueScrubPreviews(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := s.db.JobFor(ctx, "scrub_previews", lib.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if job := waitForJob(t, s, owner, job.PublicID); job.State != "done" {
		t.Fatalf("preview job: %+v", job)
	}
	made := get[ScrubPreviews](t, s, owner, route)
	if !made.Available || made.FrameCount < 2 || made.IntervalMS != 10000 || made.FrameWidth != 320 || made.FrameHeight != 180 || len(made.Sheets) != 1 {
		t.Fatalf("preview descriptor: %+v", made)
	}
	sheet := call(t, s, "GET", made.Sheets[0], owner, nil, nil)
	shape, err := jpeg.DecodeConfig(bytes.NewReader(sheet.Body.Bytes()))
	if sheet.Code != 200 || err != nil || shape.Width != 3200 || shape.Height != 1800 {
		t.Fatalf("sheet: %d, %+v, %v", sheet.Code, shape, err)
	}
	if w := call(t, s, "GET", route+"?version=missing", owner, nil, nil); w.Code != 404 {
		t.Fatalf("wrong version: %d", w.Code)
	}
	if w := call(t, s, "GET", made.Sheets[0], "", nil, nil); w.Code != 401 {
		t.Fatalf("public sheet: %d", w.Code)
	}
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kids"}, &profile)
	maxAge := 13
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &maxAge}, nil)
	var kid Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &kid)
	for _, p := range []string{route, made.Sheets[0]} {
		if w := call(t, s, "GET", p, kid.AccessToken, nil, nil); w.Code != 404 {
			t.Fatalf("rating-hidden preview: %d", w.Code)
		}
	}
	if w := call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: false}, nil); w.Code != 200 {
		t.Fatalf("library limits: %d %s", w.Code, w.Body)
	}
	for _, p := range []string{route, made.Sheets[0]} {
		if w := call(t, s, "GET", p, kid.AccessToken, nil, nil); w.Code != 404 {
			t.Fatalf("library-hidden preview: %d", w.Code)
		}
	}
	// The ordinary store sweep keeps a referenced sheet, then takes it after its title goes.
	s.setClock(func() time.Time { return time.Now().Add(48 * time.Hour) })
	if n, err := s.SweepPictures(ctx); err != nil || n != 0 {
		t.Fatalf("referenced sheet swept: %d %v", n, err)
	}
	s.setClock(time.Now)
	if err := s.db.DeleteLibrary(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if w := call(t, s, "GET", made.Sheets[0], owner, nil, nil); w.Code != 404 {
		t.Fatalf("removed title's sheet: %d", w.Code)
	}
	s.setClock(func() time.Time { return time.Now().Add(48 * time.Hour) })
	if n, err := s.SweepPictures(ctx); err != nil || n != 1 {
		t.Fatalf("unused sheet not swept: %d %v", n, err)
	}
}
