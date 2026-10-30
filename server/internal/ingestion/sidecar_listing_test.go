package ingestion

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"sync"
	"testing"
	"time"
)

// The scan lists every directory before it analyses a file in it. What it saw
// there settles whether a sidecar can exist, for as long as the directory is
// as it was listed.
func TestScanRemembersWhichSidecarsADirectoryHolds(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Show/Season 1", "Bare"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Show/Poster.JPG", "Show/clearlogo.tbn", "Show/tvshow.nfo", "Show/Season 1/notes.txt", "Show/Season 1/episode.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	lib, err := cat.Create("TV", "tv", root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, cat, assets.Probe{})
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := cat.LibrarySource(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Each call stands for the analysis of one file.
	listed := func(job, relative string) (bool, bool) {
		return s.scanListed(context.Background(), job, source)(filepath.Join(source.ResolvedPath, relative))
	}
	if _, known := listed(job.ID, "Show/poster.jpg"); known {
		t.Fatal("a directory was called known before the scan listed it")
	}
	// Up to the point where every folder has been listed and analysis is next.
	for n := 0; ; n++ {
		var phase string
		if err = db.QueryRow(`SELECT phase FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		if phase == "basic_pending" {
			break
		}
		if n == 64 {
			t.Fatal("scan never reached analysis:", phase)
		}
		s.processQuantum(context.Background(), job.ID, lib.ID)
	}
	for _, c := range []struct {
		path           string
		present, known bool
	}{
		// Seen, whatever the letter case on disk.
		{"Show/poster.jpg", true, true},
		{"Show/clearlogo.tbn", true, true},
		{"Show/tvshow.nfo", true, true},
		// Listed completely and not there.
		{"Show/fanart.jpg", false, true},
		{"Show/Season 1/poster.jpg", false, true},
		{"Show/Season 1/episode.nfo", false, true},
		{"Bare/logo.png", false, true},
		// Not a kind of file the scan keeps track of: storage decides.
		{"Show/Season 1/notes.txt", false, false},
		{"Show/Season 1/episode.srt", false, false},
		// Not a directory this scan listed.
		{"Elsewhere/poster.jpg", false, false},
	} {
		present, known := listed(job.ID, c.path)
		if present != c.present || known != c.known {
			t.Errorf("%s: present=%v known=%v, want %v %v", c.path, present, known, c.present, c.known)
		}
	}
	// Another scan knows nothing of this one's listing.
	if _, known := listed("another-job", "Show/fanart.jpg"); known {
		t.Fatal("one scan's listing answered for another")
	}

	// A folder that has changed since it was listed is no longer known, so a
	// sidecar that arrived late is asked for; its unchanged neighbour still is.
	time.Sleep(50 * time.Millisecond)
	if err = os.WriteFile(filepath.Join(root, "Show/Season 1/poster.jpg"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, known := listed(job.ID, "Show/Season 1/poster.jpg"); known {
		t.Fatal("a folder that changed after it was listed was still trusted")
	}
	if _, known := listed(job.ID, "Show/Season 1/episode.nfo"); known {
		t.Fatal("a changed folder's old listing answered for another name")
	}
	if present, known := listed(job.ID, "Bare/logo.png"); present || !known {
		t.Fatal("an unchanged folder was forgotten with the changed one", present, known)
	}

	// When the scan ends, so does what it remembered.
	if err = s.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	if _, known := listed(job.ID, "Bare/logo.png"); known {
		t.Fatal("an ended scan's listing was kept")
	}
}

// A directory whose listing was not seen from its first page to its last is
// never treated as known.
func TestPartialDirectoryListingIsNotTrusted(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Big", "Resumed"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	s := New(nil, nil, assets.Probe{})
	source := catalog.LibrarySource{ResolvedPath: root}
	page := func(directory string, complete bool, names ...string) storage.InventoryPage {
		v, err := storage.LocalInventoryDirectory(storage.InventoryRequest{Root: root, RelativePath: directory})
		if err != nil {
			t.Fatal(err)
		}
		p := storage.InventoryPage{Complete: complete, DirectoryIdentity: v.ObjectIdentity, DirectoryRevision: storage.InventoryRevision(v)}
		for _, name := range names {
			p.Entries = append(p.Entries, storage.Snapshot{Name: name})
		}
		return p
	}
	listed := func(relative string) (bool, bool) {
		return s.scanListed(context.Background(), "job", source)(filepath.Join(root, relative))
	}
	s.noteDirectoryPage("job", "Big", "", page("Big", false, "a.mkv"))
	if _, known := listed("Big/poster.jpg"); known {
		t.Fatal("a directory still being listed was called known")
	}
	s.noteDirectoryPage("job", "Big", "cursor-1", page("Big", true, "poster.jpg"))
	if present, known := listed("Big/poster.jpg"); !present || !known {
		t.Fatal("a directory listed over two pages lost a name", present, known)
	}
	// A continuation whose first page this process never saw (a restart).
	s.noteDirectoryPage("job", "Resumed", "cursor-9", page("Resumed", true, "b.mkv"))
	if _, known := listed("Resumed/poster.jpg"); known {
		t.Fatal("a listing joined part-way was called complete")
	}
	// Listing a directory again replaces what was known about it.
	s.noteDirectoryPage("job", "Big", "", page("Big", true))
	if present, known := listed("Big/poster.jpg"); present || !known {
		t.Fatal("a fresh listing did not replace the old one", present, known)
	}
	// A listing that does not say what it was a listing of is not kept.
	s.noteDirectoryPage("job", "Big", "", storage.InventoryPage{Complete: true})
	if _, known := listed("Big/poster.jpg"); known {
		t.Fatal("a listing with no directory revision was trusted")
	}
}

// One analysis observes each distinct directory once, whichever projections
// ask about it, and every analysis observes again. A listing that changed,
// was never complete or cannot be read now is unknown to all of them and is
// forgotten; the listing itself is never a grant for image bytes.
func TestAnalysisSharesOneDirectoryObservationAcrossProjections(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Show/Season 01", "Film", "Empty"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var poster bytes.Buffer
	if err := png.Encode(&poster, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct {
		relative string
		body     []byte
	}{
		{"Show/Poster.JPG", []byte("x")},
		{"Show/Season 01/Season01.png", []byte("x")},
		{"Show/Season 01/episode.mkv", []byte("x")},
		{"Film/poster.png", poster.Bytes()},
	} {
		if err := os.WriteFile(filepath.Join(root, file.relative), file.body, 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Every directory observation goes through the storage client, so counting
	// its guard counts the helper calls a scan really makes.
	var mu sync.Mutex
	attempts := map[string]int{}
	observed := map[string]int{}
	deny := ""
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	store.Guard = func(path string) error {
		mu.Lock()
		attempts[path]++
		refused := deny != "" && filepath.Clean(path) == filepath.Clean(deny)
		if !refused {
			observed[path]++
		}
		mu.Unlock()
		if refused {
			return errors.New("directory observation refused")
		}
		return nil
	}
	scanner := &Service{storage: store}
	rootPage, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RelativePath: ".", Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	source := catalog.LibrarySource{ID: "source", ResolvedPath: root, RootIdentity: rootPage.RootIdentity}
	capture := func(job, relative string, complete bool) {
		t.Helper()
		page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RootIdentity: source.RootIdentity, RelativePath: relative, Limit: 128})
		if err != nil {
			t.Fatal(err)
		}
		page.Complete = complete
		scanner.noteDirectoryPage(job, relative, "", page)
	}
	for _, dir := range []string{"Show", "Show/Season 01", "Film"} {
		capture("job", dir, true)
	}
	count := func(counts map[string]int, path string) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[path]
	}
	analysis := func(job string) (func(string) (bool, bool), func(string) (localmetadata.ArtworkDirectoryListing, bool), localmetadata.MusicTrackListing) {
		return scanner.analysisListings(context.Background(), job, source)
	}
	showDir := filepath.Join(root, "Show")
	seasonDir := filepath.Join(root, "Show", "Season 01")
	known := func(when string, wantSeason, wantShow bool) {
		t.Helper()
		listed, artwork, music := analysis("job")
		if present, known := listed(filepath.Join(seasonDir, "episode.nfo")); known != wantSeason || present {
			t.Fatalf("%s: season sidecars present=%v known=%v, want known=%v", when, present, known, wantSeason)
		}
		if _, known := artwork(seasonDir); known != wantSeason {
			t.Fatalf("%s: season artwork known=%v, want %v", when, known, wantSeason)
		}
		if _, known := music(seasonDir); known != wantSeason {
			t.Fatalf("%s: season cohort known=%v, want %v", when, known, wantSeason)
		}
		if _, known := artwork(showDir); known != wantShow {
			t.Fatalf("%s: show artwork known=%v, want %v", when, known, wantShow)
		}
		if present, known := listed(filepath.Join(showDir, "tvshow.nfo")); known != wantShow || present {
			t.Fatalf("%s: show sidecars present=%v known=%v, want known=%v", when, present, known, wantShow)
		}
	}

	// One analysis of a season episode: the folder it sits in is observed once
	// for sidecars, artwork and cohort, and its distinct parent once for the
	// show images.
	known("first analysis", true, true)
	if got := count(observed, seasonDir); got != 1 {
		t.Fatal("one analysis observed the season folder", got, "times across its projections")
	}
	if got := count(observed, showDir); got != 1 {
		t.Fatal("one analysis observed the show folder", got, "times across its projections")
	}

	// The next analysed file looks again: the memo belongs to the analysis, not
	// to the scan.
	known("second analysis", true, true)
	if got := count(observed, seasonDir); got != 2 {
		t.Fatal("a new analysis did not observe the season folder again", got)
	}
	if got := count(observed, showDir); got != 2 {
		t.Fatal("a new analysis did not observe the show folder again", got)
	}

	// A folder that changed since it was listed is unknown to every projection
	// from the one observation they share, and the stale listing is dropped.
	if err := os.WriteFile(filepath.Join(seasonDir, "Season01-fanart.jpg"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	before, beforeShow := count(attempts, seasonDir), count(attempts, showDir)
	known("changed folder", false, true)
	if got := count(attempts, seasonDir) - before; got != 1 {
		t.Fatal("a changed folder was re-observed", got, "times across its projections")
	}
	if got := count(attempts, showDir) - beforeShow; got != 1 {
		t.Fatal("the unchanged parent was not observed exactly once", got)
	}
	scanner.listingMu.Lock()
	_, stale := scanner.listings["job"].done["Show/Season 01"]
	scanner.listingMu.Unlock()
	if stale {
		t.Fatal("a changed folder's listing was kept")
	}
	// Listing it again is what makes it known again.
	capture("job", "Show/Season 01", true)
	known("re-listed folder", true, true)

	// A listing this process never saw from its first page to its last is not
	// known to any projection, and costs no observation.
	before = count(attempts, filepath.Join(root, "Empty"))
	capture("job", "Empty", false)
	listed, artwork, music := analysis("job")
	if _, known := listed(filepath.Join(root, "Empty", "poster.jpg")); known {
		t.Fatal("a partly listed folder answered sidecars")
	}
	if _, known := artwork(filepath.Join(root, "Empty")); known {
		t.Fatal("a partly listed folder answered artwork")
	}
	if _, known := music(filepath.Join(root, "Empty")); known {
		t.Fatal("a partly listed folder answered the cohort")
	}
	if got := count(attempts, filepath.Join(root, "Empty")) - before; got != 0 {
		t.Fatal("a partly listed folder was observed", got, "times")
	}

	// A folder that cannot be read now is unknown to every projection after one
	// shared attempt, and is forgotten. Its unchanged neighbour is not.
	mu.Lock()
	deny = showDir
	mu.Unlock()
	beforeShow = count(attempts, showDir)
	known("unreadable folder", true, false)
	if got := count(attempts, showDir) - beforeShow; got != 1 {
		t.Fatal("an unreadable folder was re-observed", got, "times across its projections")
	}
	scanner.listingMu.Lock()
	_, stale = scanner.listings["job"].done["Show"]
	scanner.listingMu.Unlock()
	if stale {
		t.Fatal("an unreadable folder's listing was kept")
	}
	mu.Lock()
	deny = ""
	mu.Unlock()
	capture("job", "Show", true)
	known("re-listed show folder", true, true)

	// The shared artwork listing is discovery only: the actual image read still
	// passes the current per-file scan read guard.
	metadata, err := localmetadata.New(t.TempDir(), "", store)
	if err != nil {
		t.Fatal(err)
	}
	readArtwork := func(refusal error) ([]assets.LocalArtworkCandidate, bool, int) {
		t.Helper()
		guardCalls := 0
		ctx := storage.WithScanInput(context.Background(), store, source.ID, storage.InventoryRequest{Root: root, RootIdentity: source.RootIdentity})
		ctx = storage.WithScanReadGuard(ctx, func(context.Context, string) error { guardCalls++; return refusal })
		_, art, _ := scanner.analysisListings(ctx, "job", source)
		ctx = localmetadata.WithArtworkListing(ctx, art)
		got, complete := metadata.ReadArtworkCandidates(ctx, "library", "movie", root, filepath.Join(root, "Film", "Movie.mkv"), -1)
		return got, complete, guardCalls
	}
	if got, complete, calls := readArtwork(nil); !complete || len(got) != 1 || calls == 0 {
		t.Fatal("a listed image was not admitted by its current guard", got, complete, calls)
	}
	if got, complete, calls := readArtwork(errors.New("current authority revoked")); complete || len(got) != 0 || calls == 0 {
		t.Fatal("the shared listing bypassed the current per-file admission", got, complete, calls)
	}
}
