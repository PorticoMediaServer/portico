package scan_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

var ctx = context.Background()

// prober stands in for the real one: a file's length is its size in seconds.
type prober struct{ calls atomic.Int64 }

func (p *prober) Probe(_ context.Context, path string) (scan.Facts, error) {
	p.calls.Add(1)
	info, err := os.Stat(path)
	if err != nil {
		return scan.Facts{}, err
	}
	if strings.Contains(path, "damaged") {
		return scan.Facts{}, scan.ErrDamaged
	}
	return scan.Facts{Container: "mkv", DurationMS: info.Size() * 1000, Streams: []db.Stream{
		{Kind: db.StreamVideo, Codec: "h264", Width: 1920, Height: 1080}, {Kind: db.StreamAudio, Codec: "aac", Channels: 2}},
		Chapters: []db.Marker{{StartMS: 0, EndMS: 1000, Title: "Opening"}}}, nil
}

// fixture is one library over a folder of tiny real files.
type fixture struct {
	t      *testing.T
	d      *db.DB
	s      *scan.Scanner
	probes *prober
	root   string
	lib    db.Library
	viewer db.Viewer
	sizes  int
}

func newFixture(t *testing.T, kind string) *fixture {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f := &fixture{t: t, d: d, probes: &prober{}, root: filepath.Join(t.TempDir(), "library")}
	f.s = &scan.Scanner{DB: d, Prober: f.probes}
	if err := os.MkdirAll(f.root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = d.CreateLibrary(ctx, "Library", kind, "en", []string{f.root}); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.viewer = viewers[0]
	return f
}

// write makes a file. Every file gets its own size, so a moved file is never mistaken.
func (f *fixture) write(rel string) {
	f.t.Helper()
	f.sizes++
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, make([]byte, 100+f.sizes), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) move(from, to string) {
	f.t.Helper()
	target := filepath.Join(f.root, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.root, filepath.FromSlash(from)), target); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) remove(rel string) {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Join(f.root, filepath.FromSlash(rel))); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) scan() scan.Stats {
	f.t.Helper()
	libraries, err := f.d.AllLibraries(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	f.lib = libraries[0]
	stats, err := f.s.Scan(ctx, f.lib)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.d.Check(ctx); err != nil {
		f.t.Fatal(err)
	}
	return stats
}

// titles are the library's items of one kind, by title. A title listed twice fails the test.
func (f *fixture) titles(kind db.ItemKind) map[string]db.Item {
	f.t.Helper()
	out := map[string]db.Item{}
	q := db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 200}
	for {
		page, err := f.d.Items(ctx, f.viewer, q)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, twice := out[item.Title]; twice {
				f.t.Fatalf("%s %q is in the library twice", kind, item.Title)
			}
			out[item.Title] = item
		}
		if q.Cursor = page.NextCursor; q.Cursor == "" {
			return out
		}
	}
}

func (f *fixture) detail(id string) db.ItemDetail {
	f.t.Helper()
	detail, err := f.d.Item(ctx, f.viewer, id)
	if err != nil {
		f.t.Fatalf("item %s: %v", id, err)
	}
	return detail
}

// watch marks a title watched; watched then says whether that title still is.
func (f *fixture) watch(id string) {
	f.t.Helper()
	yes := true
	if _, _, err := f.d.SetState(ctx, f.viewer, id, db.StateEdit{Watched: &yes}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) watched(id string) bool { return f.detail(id).State.Watched }

func (f *fixture) film(title string) db.Item {
	f.t.Helper()
	item, ok := f.titles(db.KindMovie)[title]
	if !ok {
		f.t.Fatalf("no film %q; have %v", title, keys(f.titles(db.KindMovie)))
	}
	return item
}

func keys(m map[string]db.Item) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func films(f *fixture) {
	f.write("Heat (1995)/Heat (1995).mkv")
	f.write("Heat (1995)/Heat (1995).en.srt")
	f.write("Heat (1995)/Heat (1995).en.forced.srt")
	f.write("Heat (1995)/Heat (1995) - 4K.mkv")
	f.write("Heat (1995)/poster.jpg")
	f.write("Heat (1995)/Trailers/Teaser.mkv")
	f.write("Alien.1979.1080p.BluRay.x264-GRP.mkv")
	f.write("Kill Bill (2003)/Kill Bill (2003) - pt1.mkv")
	f.write("Kill Bill (2003)/Kill Bill (2003) - pt2.mkv")
	f.write("Blade Runner (1982) {tmdb-78}/Blade Runner (1982) {edition-Final Cut}.mkv")
	f.write("Old Film (1950)/VIDEO_TS/VIDEO_TS.IFO")
	f.write("Old Film (1950)/VIDEO_TS/VTS_01_1.VOB")
	f.write("The Final Cut (2004).mkv")
	f.write("notes.txt")
	f.write(".hidden/Hidden (2000).mkv")
}

func TestFilmsAreReadFromTheirNames(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	f.write("Bad \xff name (2001).mkv")
	f.write("A damaged film (2002).mkv")
	nfo := `<movie><title>Alien</title><year>1979</year><plot>In space.</plot><genre>Horror</genre><uniqueid type="tmdb">348</uniqueid></movie>`
	if err := os.WriteFile(filepath.Join(f.root, "Alien.1979.1080p.BluRay.x264-GRP.nfo"), []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}
	stats := f.scan()
	got := f.titles(db.KindMovie)
	for _, want := range []string{"Heat", "Alien", "Kill Bill", "Blade Runner", "Old Film", "The Final Cut", "A damaged film"} {
		if _, ok := got[want]; !ok {
			t.Errorf("no film %q; have %v", want, keys(got))
		}
	}
	if len(got) != 7 || stats.ItemsNew != 8 {
		t.Errorf("%d films and %d new items; want 7 films and the trailer: %v", len(got), stats.ItemsNew, keys(got))
	}
	heat := f.detail(got["Heat"].PublicID)
	if heat.Year != 1995 || len(heat.Versions) != 2 || !heat.Available {
		t.Fatalf("Heat: year %d, %d versions, available %v", heat.Year, len(heat.Versions), heat.Available)
	}
	first := heat.Versions[0]
	if first.DurationMS == 0 || first.VideoCodec != "h264" || first.Height != 1080 || len(first.Markers) != 1 {
		t.Errorf("Heat's first version was not read: %+v", first)
	}
	if kill := f.detail(got["Kill Bill"].PublicID); len(kill.Versions) != 1 || len(kill.Versions[0].Parts) != 2 ||
		!strings.Contains(kill.Versions[0].Parts[0].Name, "pt1") {
		t.Errorf("Kill Bill should be one version in two parts: %+v", kill.Versions)
	}
	blade := f.detail(got["Blade Runner"].PublicID)
	if len(blade.ExternalIDs) != 1 || blade.ExternalIDs[0].ID != "78" || blade.Versions[0].Edition != "Final Cut" {
		t.Errorf("Blade Runner: ids %v, edition %q", blade.ExternalIDs, blade.Versions[0].Edition)
	}
	if alien := f.detail(got["Alien"].PublicID); alien.Summary != "In space." || len(alien.Tags) != 1 || len(alien.ExternalIDs) != 1 {
		t.Errorf("Alien's NFO file was not read: %q, %v, %v", alien.Summary, alien.Tags, alien.ExternalIDs)
	}
	skipped, total, err := f.d.SkippedFiles(ctx, f.lib.ID, 0, 40)
	if err != nil || total != 2 {
		t.Fatalf("skipped files: %v, %v", skipped, err)
	}
	reasons := map[db.FileProblem]bool{skipped[0].Problem: true, skipped[1].Problem: true}
	if !reasons[db.ProblemUnreadableName] || !reasons[db.ProblemDamaged] {
		t.Errorf("skipped files: %+v", skipped)
	}
}

// A second scan of a library where nothing changed writes no row, announces no change and
// reads no file again.
func TestRescanOfAnUnchangedLibraryChangesNothing(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	first := f.scan()
	head, _ := f.d.ChangesHead(ctx)
	probes := f.probes.calls.Load()
	again := f.scan()
	after, _ := f.d.ChangesHead(ctx)
	if again.RowsWritten != 0 || again.FilesNew+again.FilesChanged+again.FilesMoved+again.FilesGone+again.ItemsNew != 0 || after != head {
		t.Fatalf("an unchanged rescan changed something: %+v, changes %d to %d", again, head, after)
	}
	if again.Files != first.Files || again.Files == 0 || f.probes.calls.Load() != probes {
		t.Fatalf("the rescan looked at %d files (first scan %d) and read %d", again.Files, first.Files, f.probes.calls.Load()-probes)
	}
}

// Renaming a folder, moving a file and replacing a file each leave the title the same title,
// with the same watched mark, and a moved file is not read again.
func TestTitlesKeepTheirStateThroughRenamesMovesAndReplacements(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	f.scan()
	heat, alien, kill := f.film("Heat"), f.film("Alien"), f.film("Kill Bill")
	for _, item := range []db.Item{heat, alien, kill} {
		f.watch(item.PublicID)
	}
	probes := f.probes.calls.Load()

	f.move("Heat (1995)", "Crime/Heat (1995)")
	f.move("Alien.1979.1080p.BluRay.x264-GRP.mkv", "Alien (1979)/Alien (1979).mkv")
	stats := f.scan()
	if stats.FilesMoved != 4 || stats.ItemsNew != 0 || f.probes.calls.Load() != probes {
		t.Fatalf("moved %d files, %d new items, %d files read again; want 4 moves and nothing else", stats.FilesMoved, stats.ItemsNew, f.probes.calls.Load()-probes)
	}
	for _, was := range []db.Item{heat, alien, kill} {
		now := f.film(was.Title)
		if now.PublicID != was.PublicID || !f.watched(now.PublicID) || !f.detail(now.PublicID).Available {
			t.Errorf("%s is not the same watched title after the move", was.Title)
		}
	}
	if len(f.titles(db.KindMovie)) != 6 || len(f.detail(heat.PublicID).Versions) != 2 {
		t.Errorf("titles after the moves: %v", keys(f.titles(db.KindMovie)))
	}

	// A better copy at the same path: the title stays, the version's facts change.
	before := f.detail(alien.PublicID).Versions[0]
	f.write("Alien (1979)/Alien (1979).mkv")
	if stats = f.scan(); stats.FilesChanged != 1 || stats.ItemsNew != 0 {
		t.Fatalf("replacing a file: %+v", stats)
	}
	after := f.detail(alien.PublicID)
	if !f.watched(alien.PublicID) || after.Versions[0].PublicID != before.PublicID || after.Versions[0].DurationMS == before.DurationMS {
		t.Errorf("a replaced file should keep its title and version and get new facts: %+v", after.Versions)
	}
}

// A file that is gone makes its title Missing at once; the title comes back as it was when
// the file does. A whole folder that is gone does the same for everything in it.
func TestALostFileMakesItsTitleMissingUntilItReturns(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	f.scan()
	alien, kill := f.film("Alien"), f.film("Kill Bill")
	f.watch(alien.PublicID)
	f.watch(kill.PublicID)
	f.move("Alien.1979.1080p.BluRay.x264-GRP.mkv", "../alien.keep")
	f.move("Kill Bill (2003)", "../kill.keep")
	f.remove("Heat (1995)/Heat (1995) - 4K.mkv")
	stats := f.scan()
	if stats.FilesGone != 2 || f.detail(alien.PublicID).Available || f.detail(kill.PublicID).Available {
		t.Fatalf("lost files should make their titles missing: %+v", stats)
	}
	if heat := f.detail(f.film("Heat").PublicID); !heat.Available || len(heat.Versions) != 1 {
		t.Errorf("Heat lost one of two versions and should still play: %d versions, available %v", len(heat.Versions), heat.Available)
	}
	missing, total, err := f.d.MissingItems(ctx, f.lib.ID, 50)
	if err != nil || total != 2 || len(missing) != 2 {
		t.Fatalf("missing titles: %+v, %v", missing, err)
	}

	f.move("../alien.keep", "Alien.1979.1080p.BluRay.x264-GRP.mkv")
	f.move("../kill.keep", "Kill Bill (2003)")
	if stats = f.scan(); stats.ItemsNew != 0 {
		t.Fatalf("files coming back made new titles: %+v", stats)
	}
	for _, was := range []db.Item{alien, kill} {
		if now := f.detail(f.film(was.Title).PublicID); now.PublicID != was.PublicID || !now.Available || !now.State.Watched {
			t.Errorf("%s did not come back as it was", was.Title)
		}
	}
}

// A version the owner split off stays its own title through rescans, including one that has
// reason to look at its folder again.
func TestASplitHoldsThroughRescans(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	f.scan()
	heat := f.film("Heat")
	f.watch(heat.PublicID)
	version := f.detail(heat.PublicID).Versions[1]
	split, err := f.d.SplitVersion(ctx, version.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	f.write("Heat (1995)/Heat (1995) - 4K.en.srt")
	f.write("Heat (1995)/Heat (1995).fr.srt")
	f.scan()
	f.move("Heat (1995)", "Heat (1995) [remux]")
	f.scan()
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("a rescan after the split wrote %d rows", stats.RowsWritten)
	}
	original, other := f.detail(heat.PublicID), f.detail(split)
	if len(original.Versions) != 1 || len(other.Versions) != 1 || !original.State.Watched || other.State.Watched {
		t.Fatalf("the split did not hold: %d and %d versions", len(original.Versions), len(other.Versions))
	}
	if want := strings.TrimSuffix(version.Parts[0].Name, ".mkv"); other.Title != want || other.Versions[0].PublicID != version.PublicID {
		t.Errorf("the split title is %q with version %s; want %q with %s", other.Title, other.Versions[0].PublicID, want, version.PublicID)
	}
}

// A scan stopped part way, as by a kill, loses nothing it stored and makes nothing twice
// when it runs again.
func TestAScanStoppedPartWayIsFinishedByTheNext(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	for n := 0; n < 40; n++ {
		f.write("Film " + string(rune('A'+n%26)) + string(rune('a'+n/26)) + " (2001).mkv")
		f.write("Folder " + string(rune('A'+n%26)) + string(rune('a'+n/26)) + " (2002)/film.mkv")
	}
	f.s.BatchFiles = 7
	stopped, stop := context.WithCancel(ctx)
	f.s.Progress = func(stats scan.Stats) {
		if stats.FilesNew >= 30 {
			stop()
		}
	}
	libraries, _ := f.d.AllLibraries(ctx)
	stats, err := f.s.Scan(stopped, libraries[0])
	if err == nil || stats.FilesNew >= 80 {
		t.Fatalf("the scan was not stopped part way: %+v, %v", stats, err)
	}
	f.s.Progress = nil
	rest := f.scan()
	if got := len(f.titles(db.KindMovie)); got != 80 || stats.ItemsNew+rest.ItemsNew != 80 {
		t.Fatalf("%d films after the scan was run again, %d + %d made; want 80", got, stats.ItemsNew, rest.ItemsNew)
	}
	if again := f.scan(); again.RowsWritten != 0 {
		t.Fatalf("a third scan wrote %d rows", again.RowsWritten)
	}
}

// A library folder that cannot be reached, or that is suddenly empty, marks nothing missing.
func TestAnUnreachableOrEmptiedFolderMarksNothingMissing(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	f.scan()
	for _, how := range []string{"unreachable", "emptied"} {
		if err := os.Rename(f.root, f.root+".away"); err != nil {
			t.Fatal(err)
		}
		if how == "emptied" {
			os.Mkdir(f.root, 0o755)
		}
		stats := f.scan()
		_, missing, err := f.d.MissingItems(ctx, f.lib.ID, 10)
		if err != nil || missing != 0 || stats.RowsWritten != 0 || stats.FilesGone != 0 {
			t.Fatalf("%s folder: %d titles missing, %+v, %v", how, missing, stats, err)
		}
		os.Remove(f.root)
		if err := os.Rename(f.root+".away", f.root); err != nil {
			t.Fatal(err)
		}
	}
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("the folder came back and the scan wrote %d rows", stats.RowsWritten)
	}
}

func shows(f *fixture) {
	f.write("The Wire/Season 1/The Wire - S01E01.mkv")
	f.write("The Wire/Season 1/The Wire - S01E01.en.srt")
	f.write("The Wire/Season 1/The Wire - S01E02-E03.mkv")
	f.write("The Wire/Season 2/E05.mkv")
	f.write("The Wire/Season 2/06.mkv")
	f.write("The Wire/Specials/The Wire S00E01.mkv")
	f.write("The Wire/Extras/Making of.mkv")
	f.write("Dark/Staffel 1/Dark 1x02.mkv")
	f.write("Fargo.S02E05.mkv")
	f.write("The Daily Show 2024-03-15.mkv")
	f.write("Home video from the lake.mkv")
}

func (f *fixture) children(parent string) map[int]db.Item {
	f.t.Helper()
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Parent: parent, Limit: 200})
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[int]db.Item{}
	for _, item := range page.Items {
		if item.IndexNumber == nil || out[*item.IndexNumber].PublicID != "" {
			f.t.Fatalf("under %s: %q has no number or a number used twice", parent, item.Title)
		}
		out[*item.IndexNumber] = item
	}
	return out
}

func TestShowsAreReadFromTheirNames(t *testing.T) {
	f := newFixture(t, db.LibraryShows)
	shows(f)
	f.scan()
	got := f.titles(db.KindShow)
	if len(got) != 4 {
		t.Fatalf("shows: %v", keys(got))
	}
	wire, ok := got["The Wire"]
	if !ok || got["Dark"].PublicID == "" || got["Fargo"].PublicID == "" || got["The Daily Show"].PublicID == "" {
		t.Fatalf("shows: %v", keys(got))
	}
	seasons := f.children(wire.PublicID)
	if len(seasons) != 3 || seasons[0].Title != "Specials" {
		t.Fatalf("The Wire's seasons: %v", seasons)
	}
	one, two := f.children(seasons[1].PublicID), f.children(seasons[2].PublicID)
	if len(one) != 3 || len(two) != 2 || two[5].PublicID == "" || two[6].PublicID == "" {
		t.Fatalf("The Wire has %d and %d episodes in seasons 1 and 2", len(one), len(two))
	}
	// Two episodes in one file: both are listed and both play the file.
	second, third := f.detail(one[2].PublicID), f.detail(one[3].PublicID)
	if second.Versions[0].Parts[0].FileID != third.Versions[0].Parts[0].FileID {
		t.Errorf("episodes 2 and 3 should play the same file")
	}
	if detail := f.detail(wire.PublicID); detail.Leaves != 6 {
		t.Errorf("The Wire counts %d episodes; want 6", detail.Leaves)
	}
	if daily := f.children(f.children(got["The Daily Show"].PublicID)[2024].PublicID); len(daily) != 1 {
		t.Errorf("the dated episode was not placed by its date")
	}
	// A file that cannot be placed is still a title, under its cleaned name.
	if _, ok := f.titles(db.KindMovie)["Home video from the lake"]; !ok {
		t.Errorf("loose titles: %v", keys(f.titles(db.KindMovie)))
	}
	if again := f.scan(); again.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", again.RowsWritten)
	}
}

// Renaming a show's folder keeps the show, its seasons and its episodes the same titles, with
// what each person had on them.
func TestARenamedShowFolderIsTheSameShow(t *testing.T) {
	f := newFixture(t, db.LibraryShows)
	shows(f)
	f.scan()
	wire := f.titles(db.KindShow)["The Wire"]
	episode := f.children(f.children(wire.PublicID)[2].PublicID)[5]
	f.watch(episode.PublicID)
	favourite := true
	if _, _, err := f.d.SetState(ctx, f.viewer, wire.PublicID, db.StateEdit{Favorite: &favourite}); err != nil {
		t.Fatal(err)
	}
	f.move("The Wire", "The Wire (2002)")
	stats := f.scan()
	got := f.titles(db.KindShow)
	if len(got) != 4 || got["The Wire"].PublicID != wire.PublicID || stats.ItemsNew != 0 {
		t.Fatalf("after the rename: %v, %d new items", keys(got), stats.ItemsNew)
	}
	now := f.children(f.children(wire.PublicID)[2].PublicID)[5]
	if now.PublicID != episode.PublicID || !f.watched(now.PublicID) || !f.detail(wire.PublicID).State.Favorite {
		t.Errorf("the renamed show lost an episode's watched mark or its own favourite")
	}
	if detail := f.detail(wire.PublicID); detail.Year != 2002 || detail.Leaves != 6 {
		t.Errorf("the renamed show: year %d, %d episodes", detail.Year, detail.Leaves)
	}
}

// A title removed from the trash and added again gets back what each person had on it, by
// its name and year.
func TestStateReturnsToATitleThatWasRemovedAndAddedAgain(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	f.scan()
	if err := f.d.SetLibraryOptions(ctx, f.lib.ID, db.LibraryOptions{Trash: db.TrashRule{Mode: db.TrashAfterScan}}); err != nil {
		t.Fatal(err)
	}
	alien := f.film("Alien")
	f.watch(alien.PublicID)
	f.move("Alien.1979.1080p.BluRay.x264-GRP.mkv", "../alien.keep")
	if stats := f.scan(); stats.Removed != 1 {
		t.Fatalf("the trash rule removed %d titles; want 1", stats.Removed)
	}
	if _, still := f.titles(db.KindMovie)["Alien"]; still {
		t.Fatal("Alien is still in the library after its trash was emptied")
	}
	f.write("Alien (1979)/Alien (1979) remastered.mkv")
	f.scan()
	back := f.film("Alien")
	if back.PublicID == alien.PublicID || !f.watched(back.PublicID) {
		t.Fatalf("Alien came back without its watched mark")
	}
}

// What a person had on a removed title returns when the title is added to another library,
// and where they already have something on it, the later edit wins field by field.
func TestKeptStateReturnsInAnotherLibrary(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	films(f)
	f.scan()
	alien := f.film("Alien")
	f.watch(alien.PublicID)
	if err := f.d.SetLibraryOptions(ctx, f.lib.ID, db.LibraryOptions{Trash: db.TrashRule{Mode: db.TrashAfterScan}}); err != nil {
		t.Fatal(err)
	}
	f.remove("Alien.1979.1080p.BluRay.x264-GRP.mkv")
	f.scan()
	// The same film turns up in a second library.
	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "Alien (1979).mkv"), make([]byte, 999), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := f.d.CreateLibrary(ctx, "Second", db.LibraryMovies, "en", []string{other})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Scan(ctx, second); err != nil {
		t.Fatal(err)
	}
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: second.PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	if err != nil || len(page.Items) != 1 || !f.watched(page.Items[0].PublicID) || f.watched(f.film("Heat").PublicID) {
		t.Fatalf("Alien should be watched again in the second library, and Heat should not be: %+v, %v", page.Items, err)
	}
}

// A link back to a folder above it does not send the scan round in circles or list a film twice.
func TestALinkBackToAFolderAboveIsReadOnce(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	f.write("Heat (1995)/Heat (1995).mkv")
	for _, link := range []string{"Heat (1995)/again", "Heat (1995)/and again"} {
		if err := os.Symlink(f.root, filepath.Join(f.root, filepath.FromSlash(link))); err != nil {
			t.Skip("links cannot be made here:", err)
		}
	}
	first := f.scan()
	if got := f.titles(db.KindMovie); len(got) != 1 || first.Files != 1 {
		t.Fatalf("%d films from %d files; want one film from one file: %v", len(got), first.Files, keys(got))
	}
	if again := f.scan(); again.RowsWritten != 0 {
		t.Fatalf("a rescan wrote %d rows", again.RowsWritten)
	}
}

// held is a local folder whose listing waits until it is let go.
type held struct {
	scan.Local
	listing chan struct{} // closed when a listing has started
	release chan struct{}
}

func (h held) List(ctx context.Context, rel string) ([]scan.Entry, error) {
	select {
	case <-h.listing:
	default:
		close(h.listing)
	}
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return h.Local.List(ctx, rel)
}

// Cancel scan and then Scan now, before the cancelled scan has wound down, runs a new scan
// to its end: it is not left cancelled.
func TestScanNowAfterCancelRunsANewScan(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	f.write("Heat (1995).mkv")
	libraries, err := f.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	library := libraries[0].PublicID
	h := held{listing: make(chan struct{}), release: make(chan struct{})}
	f.s.Source = func(root string) scan.Source { h.Local = scan.Local{Root: root}; return h }
	m := scan.NewManager(jobs.New(ctx, f.d, slog.New(slog.NewTextHandler(io.Discard, nil))), f.s)
	first, err := m.Scan(ctx, library, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	<-h.listing
	if err := m.Cancel(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Scan(ctx, library, 0, false); err != nil {
		t.Fatal(err)
	}
	close(h.release)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		job, err := f.d.JobFor(ctx, db.JobScan, library)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == db.JobDone {
			break
		}
		if job.State == db.JobCancelled || time.Now().After(deadline) {
			t.Fatalf("the scan asked for after Cancel ended %s", job.State)
		}
	}
	if stats, _ := m.Stats(library); stats.ItemsNew != 1 {
		t.Fatalf("the new scan found %d titles; want the one film", stats.ItemsNew)
	}
}
