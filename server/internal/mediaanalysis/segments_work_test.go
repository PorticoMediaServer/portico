package mediaanalysis

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect/segmentsynth"
)

func readSegmentCatchupFile(ctx context.Context, db *sql.DB, object, revision string) (segmentFile, error) {
	snapshot, err := dbwork.BeginSnapshot(ctx, db)
	if err != nil {
		return segmentFile{}, err
	}
	defer snapshot.Rollback()
	return segmentFileTx(ctx, snapshot.Tx(), object, revision)
}

func TestSegmentCatchupEmptyServerDoesNotCreateChangingCommits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	s := &Service{db: c.DB}
	ctx := context.Background()
	if more, err := s.segmentCatchup(ctx); err != nil || more {
		t.Fatal("initial empty pass", more, err)
	}
	before, err := readSegmentCatchup(ctx, c.DB)
	if err != nil || !before.Settled {
		t.Fatal("initial completion was not persisted", before, err)
	}
	commits := dbwork.ChangingCommits()
	for i := 0; i < 30; i++ {
		if more, err := s.segmentCatchup(ctx); err != nil || more {
			t.Fatal("unchanged empty pass", more, err)
		}
	}
	after, err := readSegmentCatchup(ctx, c.DB)
	if err != nil || after != before || dbwork.ChangingCommits() != commits {
		t.Fatal("empty catchup rewrote progress/published a changing commit", before, after, dbwork.ChangingCommits()-commits, err)
	}
}

func TestSegmentCatchupKeepsNewRevisionDurableAndCancellationSilent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSegmentFixture(t)
	f.episode("one", 1, 1, 0)
	ctx := context.Background()
	if more, err := f.service.segmentCatchup(ctx); err != nil || more {
		t.Fatal(more, err)
	}
	before, err := readSegmentCatchup(ctx, f.c.DB)
	if err != nil {
		t.Fatal(err)
	}
	object := f.objects["one"]
	old, err := readSegmentCatchupFile(ctx, f.c.DB, object, "r1")
	if err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE library_id='tv'`)
	f.c.Exec(`UPDATE inventory_objects SET revision='r2',analysis_revision='r2' WHERE id=?`, object)
	f.c.Exec(`INSERT INTO inventory_changes(source_id,job_id,object_id,asset_id,revision,kind,created_at) SELECT source_id,'job',id,asset_id,'r2','analysis_complete','2026-10-24T00:00:00Z' FROM inventory_objects WHERE id=?`, object)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	commits := dbwork.ChangingCommits()
	if _, err = f.service.segmentCatchup(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled catchup did not refuse", err)
	}
	if state, err := readSegmentCatchup(ctx, f.c.DB); err != nil || state != before || dbwork.ChangingCommits() != commits {
		t.Fatal("cancellation consumed durable progress", state, err)
	}
	if _, err := readSegmentCatchupFile(ctx, f.c.DB, object, "r1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old generation/revision remained eligible", err)
	}
	// A replacement worker must resume the committed high-water mark and admit
	// the new source revision, rather than inheriting the canceled pass.
	restarted := &Service{db: f.c.DB}
	if more, err := restarted.segmentCatchup(ctx); err != nil || more {
		t.Fatal(more, err)
	}
	current, err := readSegmentCatchupFile(ctx, f.c.DB, object, "r2")
	if err != nil || current.Generation <= old.Generation || current.binding() == old.binding() {
		t.Fatal("new current source tuple was not admitted", current, old, err)
	}
	progress, err := readSegmentCatchup(ctx, f.c.DB)
	if err != nil || progress.ChangeSequence <= before.ChangeSequence {
		t.Fatal("new high-water mark was not persisted", before, progress, err)
	}
	commits = dbwork.ChangingCommits()
	for i := 0; i < 30; i++ {
		if _, err := restarted.segmentCatchup(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if state, err := readSegmentCatchup(ctx, f.c.DB); err != nil || state != progress || dbwork.ChangingCommits() != commits {
		t.Fatal("unchanged admitted work rewrote progress", progress, state, err)
	}
}

func TestSegmentCatchupRestartsAtDurableBoundedPage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSegmentFixture(t)
	for i := 0; i <= segmentCatchupPage; i++ {
		f.episode(fmt.Sprintf("page-%03d", i), 1, i+1, 0)
	}
	ctx := context.Background()
	if more, err := f.service.segmentCatchup(ctx); err != nil || !more {
		t.Fatal("first bounded page", more, err)
	}
	first, err := readSegmentCatchup(ctx, f.c.DB)
	if err != nil || first.Cursor == "" || first.Settled {
		t.Fatal("first page not durable", first, err)
	}
	restarted := &Service{db: f.c.DB}
	if more, err := restarted.segmentCatchup(ctx); err != nil || more {
		t.Fatal("remaining page", more, err)
	}
	last, err := readSegmentCatchup(ctx, f.c.DB)
	if err != nil || !last.Settled || last.Cursor != "" || last.ChangeSequence != first.ChangeSequence {
		t.Fatal("restart lost its page/high-water boundary", first, last, err)
	}
	var queued int
	if err := f.c.DB.QueryRow(`SELECT COUNT(*) FROM segment_files WHERE source_revision='r1' AND state='pending'`).Scan(&queued); err != nil || queued != segmentCatchupPage+1 {
		t.Fatal("newly admitted catchup work lost", queued, err)
	}
}

// fakeInput stands in for a bridged media file; the fake decoder never reads it.
type fakeInput struct{ evidence string }

func (f fakeInput) Evidence() string                                       { return f.evidence }
func (fakeInput) Size() int64                                              { return 1 << 20 }
func (fakeInput) ReadExtent(context.Context, int64, int64) ([]byte, error) { return nil, io.EOF }
func (fakeInput) Validate(context.Context) error                           { return nil }
func (fakeInput) Close() error                                             { return nil }

type segmentFixture struct {
	t        *testing.T
	c        *catalogtest.Catalog
	service  *Service
	show     catalogtest.Item
	source   catalog.LibrarySource
	seasons  map[int]catalogtest.Item
	programs map[string]segmentsynth.Programme // by asset token
	objects  map[string]string                 // episode name → object id
	items    map[string]catalogtest.Item
	decodes  []decoder.AnalysisSpec
}

func newSegmentFixture(t *testing.T) *segmentFixture {
	t.Helper()
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("tv", "TV", "tv", root)
	f := &segmentFixture{t: t, c: c, seasons: map[int]catalogtest.Item{}, programs: map[string]segmentsynth.Programme{}, objects: map[string]string{}, items: map[string]catalogtest.Item{}}
	f.show = c.Show(library, "Show", 2020)
	// Admit this same original source through the real physical-root API;
	// raw Catalog.Library intentionally does not confirm source identity.
	ctx := context.Background()
	catalogService := catalog.New(c.DB)
	original, err := catalogService.LibrarySource(ctx, "tv")
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = c.DB.QueryRow(`SELECT revision FROM library_configuration WHERE library_id='tv'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	authorize := func(tx *sql.Tx) error {
		var originalRoot string
		if err := tx.QueryRowContext(ctx, `SELECT root FROM libraries WHERE id='tv'`).Scan(&originalRoot); err != nil {
			return err
		}
		if originalRoot != root {
			return errors.New("fixture library root changed before source admission")
		}
		return nil
	}
	f.source, err = catalogService.SaveSource(ctx, "tv", original.ID, catalog.SourceSettings{Name: original.Name, Path: root, Classification: "local", ExpectedRevision: revision}, authorize)
	if err != nil {
		t.Fatal(err)
	}
	if f.source.ID != original.ID || !f.source.IdentityConfirmed || f.source.Incarnation == "" || f.source.RootIdentity == "" || f.source.Generation <= original.Generation {
		t.Fatal("original source was not physically admitted", f.source)
	}
	tool := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(tool, []byte("decoder"), 0o700); err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{DB: c.DB, Directory: canonicalFixtureDir(t), FFmpeg: tool,
		Open: func(_ context.Context, _, asset string) (Input, error) { return fakeInput{"observed:" + asset}, nil },
		Decoder: func(input Input) Decode {
			asset := input.Evidence()[len("observed:"):]
			return func(_ context.Context, _ string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
				f.decodes = append(f.decodes, spec)
				if spec.Kind != "segment_audio" {
					return ErrUnsupported
				}
				program := f.programs[asset]
				return consume(bytes.NewReader(program.RenderRange(segmentdetect.SampleRate, float64(spec.StartUS)/1e6, float64(spec.RegionUS)/1e6)))
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	f.service = service
	c.Drain()
	return f
}

// episode adds one episode file: cold open, the show's intro, its own story,
// the show's credits.
func (f *segmentFixture) episode(name string, season, number int, coldOpen float64) {
	f.t.Helper()
	s, ok := f.seasons[season]
	if !ok {
		s = f.c.Season(f.show, season)
		f.seasons[season] = s
	}
	seed := uint64(1000 + season*100 + number)
	program := segmentsynth.Programme{
		{Signal: segmentsynth.Speech(seed), Length: coldOpen},
		{Signal: segmentsynth.Theme(7), Length: 50},
		{Signal: segmentsynth.Silence(), Length: 1},
		{Signal: segmentsynth.Speech(seed + 1), Length: 300},
		{Signal: segmentsynth.Theme(8), Length: 40},
		{Signal: segmentsynth.Silence(), Length: 2},
	}
	item := f.c.Episode(f.show, s, number, filepath.Join(f.source.ResolvedPath, "Show", name+".mkv"))
	f.c.Exec(`UPDATE catalog_assets SET duration=? WHERE token=?`, program.Duration(), item.Token)
	object := "object-" + name
	f.c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,analysis_revision) VALUES(?,?,?,?,?,'r1','{}',1000,1,'available','r1')`, object, f.source.ID, item.Token, f.source.Incarnation, filepath.Join("Show", name+".mkv"))
	f.c.Exec(`INSERT INTO inventory_changes(source_id,job_id,object_id,asset_id,revision,kind,created_at) VALUES(?,'job',?,?,'r1','analysis_complete','2026-10-18T00:00:00Z')`, f.source.ID, object, item.Token)
	f.programs[item.Token] = program
	f.objects[name] = object
	f.items[name] = item
	f.c.Drain()
}

// settle runs the worker until it has nothing to do.
func (f *segmentFixture) settle() {
	f.t.Helper()
	for i := 0; i < 400; i++ {
		wait, err := f.service.SegmentStep(context.Background())
		if err != nil {
			f.t.Fatal(err)
		}
		if wait != time.Millisecond {
			return
		}
	}
	f.t.Fatal("the segment worker did not settle")
}

type fixtureMarker struct {
	kind, provenance       string
	start, end             int64
	confidence             float64
	approved, edited, gone bool
}

func (f *segmentFixture) markers(name string) []fixtureMarker {
	f.t.Helper()
	rows, err := f.c.DB.Query(`SELECT kind,provenance,start_us,end_us,confidence,approved,edited,deleted FROM analysis_markers WHERE object_id=? ORDER BY start_us`, f.objects[name])
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []fixtureMarker
	for rows.Next() {
		var m fixtureMarker
		if err = rows.Scan(&m.kind, &m.provenance, &m.start, &m.end, &m.confidence, &m.approved, &m.edited, &m.gone); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func (f *segmentFixture) live(name, kind string) []fixtureMarker {
	var out []fixtureMarker
	for _, m := range f.markers(name) {
		if m.kind == kind && !m.gone {
			out = append(out, m)
		}
	}
	return out
}

func TestSegmentWorkerFindsASeasonsIntroAndCreditsAndRespectsTheOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSegmentFixture(t)
	for i, cold := range []float64{0, 40, 90, 15} {
		f.episode(fmt.Sprintf("s1e%d", i+1), 1, i+1, cold)
	}
	f.settle()
	for i, cold := range []float64{0, 40, 90, 15} {
		name := fmt.Sprintf("s1e%d", i+1)
		intro := f.live(name, "intro")
		if len(intro) != 1 || intro[0].provenance != segmentdetect.IntroProvenance || intro[0].confidence < 0.85 {
			t.Fatalf("%s intro markers %+v", name, f.markers(name))
		}
		want := int64(cold * 1e6)
		if want <= 5e6 {
			want = 0
		}
		if d := intro[0].start - want; d > 500_000 || d < -500_000 {
			t.Fatalf("%s intro starts at %d, want %d", name, intro[0].start, want)
		}
		credits := f.live(name, "credits")
		if len(credits) != 1 || credits[0].confidence < 0.85 {
			t.Fatalf("%s credits markers %+v", name, f.markers(name))
		}
	}
	// The viewer sees the season's intro as safe to skip without asking.
	set, err := ViewerMarkers(context.Background(), f.c.DB, Access{LibraryID: "tv", ItemID: f.items["s1e2"].Public, AccountID: "a", ProfileID: "p", Authority: "local", Authorize: func(*sql.Tx) error { return nil }}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Markers) != 2 || set.Markers[0].Kind != "intro" || !set.Markers[0].AutomaticSafe || set.Markers[1].Kind != "credits" || !set.Markers[1].AutomaticSafe {
		t.Fatalf("viewer markers %+v", set.Markers)
	}
	// Only the audio of the two regions was decoded, never the whole file.
	for _, spec := range f.decodes {
		if spec.Kind != "segment_audio" || spec.RegionUS > 600_000_000 {
			t.Fatalf("decoded %+v", spec)
		}
	}
	// The owner dismisses one episode's intro. A new episode arrives and the
	// season is compared again: the dismissal stands.
	f.c.Exec(`UPDATE analysis_markers SET deleted=1,edited=1 WHERE object_id=? AND kind='intro'`, f.objects["s1e1"])
	before := len(f.decodes)
	f.episode("s1e5", 1, 5, 60)
	f.settle()
	if len(f.live("s1e1", "intro")) != 0 {
		t.Fatalf("re-detection resurrected a dismissed intro: %+v", f.markers("s1e1"))
	}
	if intro := f.live("s1e5", "intro"); len(intro) != 1 || intro[0].confidence < 0.85 {
		t.Fatalf("the new episode's intro: %+v", f.markers("s1e5"))
	}
	// Incremental: only the new episode's file was decoded.
	if got := len(f.decodes) - before; got != 2 {
		t.Fatalf("adding one episode decoded %d regions, want its own two", got)
	}
	// The owner turns detection off for the server: nothing new is queued
	// and viewers keep only what the owner made or approved.
	f.c.Exec(`INSERT INTO server_settings(singleton,"library.segmentDetection") VALUES(1,'off') ON CONFLICT(singleton) DO UPDATE SET "library.segmentDetection"='off'`)
	f.episode("s1e6", 1, 6, 20)
	f.settle()
	var queued int
	if err = f.c.DB.QueryRow(`SELECT COUNT(*) FROM segment_files WHERE object_id=?`, f.objects["s1e6"]).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("a file was queued with detection off: %d %v", queued, err)
	}
	set, err = ViewerMarkers(context.Background(), f.c.DB, Access{LibraryID: "tv", ItemID: f.items["s1e2"].Public, AccountID: "a", ProfileID: "p", Authority: "local", Authorize: func(*sql.Tx) error { return nil }}, "")
	if err != nil || len(set.Markers) != 0 {
		t.Fatalf("detections stayed visible with detection off: %+v %v", set.Markers, err)
	}
}

func TestSegmentWorkerConfirmsASingleEpisodeSeasonFromTheNextSeason(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSegmentFixture(t)
	f.episode("s1e1", 1, 1, 30)
	for i := range 3 {
		f.episode(fmt.Sprintf("s2e%d", i+1), 2, i+1, float64(10+i*30))
	}
	f.settle()
	if intro := f.live("s1e1", "intro"); len(intro) != 1 || intro[0].confidence < 0.85 {
		t.Fatalf("the single-episode season's intro was not confirmed from its neighbours: %+v", f.markers("s1e1"))
	}
}

func TestSegmentReviewApproveDismissAndRedetectKeepOwnerDecisions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSegmentFixture(t)
	for i, cold := range []float64{0, 30, 60} {
		f.episode(fmt.Sprintf("s1e%d", i+1), 1, i+1, cold)
	}
	f.settle()
	ctx := context.Background()
	review, err := f.service.SegmentTitle(ctx, nil, f.show.Public)
	if err != nil {
		t.Fatal(err)
	}
	if review.Kind != "show" || review.Mode != SegmentModeStandard || len(review.Seasons) != 1 || len(review.Seasons[0].Entries) != 3 {
		t.Fatalf("review %+v", review)
	}
	first := review.Seasons[0].Entries[0]
	if first.SourceRevision == "" || first.MappingRevision == "" || len(first.Markers) != 2 || first.Markers[0].Basis != "season" || !first.Markers[0].AutomaticSafe {
		t.Fatalf("entry %+v", first)
	}
	// The review's target is exactly what the marker edit takes.
	owner := Access{LibraryID: "tv", ItemID: first.ItemID, AccountID: "a", ProfileID: "p", Authority: "local", Owner: true, Authorize: func(*sql.Tx) error { return nil }}
	if _, err = f.service.Mutate(ctx, owner, Mutation{Target: Target{SourceID: first.SourceID, SourceRevision: first.SourceRevision, MappingRevision: first.MappingRevision}, ExpectedRevision: first.MarkerRevision, RequestID: "request-dismiss-0001", Action: "dismiss", MarkerID: first.Markers[0].ID}); err != nil {
		t.Fatal(err)
	}
	approved, err := f.service.SegmentApprove(ctx, nil, Actor("local", "a", "p"), f.show.Public, 1, []string{"intro", "credits"})
	if err != nil || approved != 5 {
		t.Fatalf("approved %d markers (want the five not dismissed): %v", approved, err)
	}
	if n, err := f.service.SegmentRedetect(ctx, nil, f.show.Public); err != nil || n != 3 {
		t.Fatalf("redetect %d %v", n, err)
	}
	f.settle()
	if len(f.live("s1e1", "intro")) != 0 {
		t.Fatalf("re-running detection resurrected a dismissed intro: %+v", f.markers("s1e1"))
	}
	for _, name := range []string{"s1e2", "s1e3"} {
		intro := f.live(name, "intro")
		if len(intro) != 1 || !intro[0].approved {
			t.Fatalf("%s: an approved intro was replaced by re-detection: %+v", name, f.markers(name))
		}
	}
	status, err := f.service.SegmentStatus(ctx, nil)
	if err != nil || status.Mode != SegmentModeStandard || len(status.Libraries) != 1 || status.Libraries[0].Files.Complete != 3 || !status.CatchUpComplete {
		t.Fatalf("status %+v %v", status, err)
	}
	page, err := f.service.SegmentTitles(ctx, nil, "tv", "", 10)
	if err != nil || len(page.Titles) != 1 || page.Titles[0].Files.Complete != 3 || page.Titles[0].Markers.Approved != 5 || page.Titles[0].Markers.Dismissed != 1 {
		t.Fatalf("titles %+v %v", page, err)
	}
}

// A server upgraded from before intro and credits detection has settings and
// library documents that never mention it: the server runs standard, its video
// libraries follow it, and a library's own later choice wins.
func TestSegmentModesOfDocumentsSavedBeforeDetectionExisted(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("tv", "TV", "tv", "/tv")
	c.Library("films", "Films", "movie", "/films")
	c.Library("music", "Music", "music", "/music")
	c.Exec(`INSERT INTO console_documents(scope,revision,body,updated_ms) VALUES('server',4,'{"name":"Den","analysisStorageGiB":64}',1)`)
	c.Exec(`INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('library:tv',2,'{"allowMediaDeletion":true}',1)`)
	ctx := context.Background()
	libraries, server, err := segmentLibrariesTx(ctx, c.DB)
	if err != nil || server != SegmentModeStandard {
		t.Fatalf("server mode %q (%v)", server, err)
	}
	got := map[string]string{}
	for _, l := range libraries {
		got[l.ID] = l.Choice + ":" + l.Mode
	}
	if len(got) != 2 || got["tv"] != "server:standard" || got["films"] != "server:standard" {
		t.Fatalf("%v", got)
	}
	c.Exec(`UPDATE admin_documents SET body='{"allowMediaDeletion":true,"segmentDetection":"off"}' WHERE scope='library:tv'`)
	if libraries, _, err = segmentLibrariesTx(ctx, c.DB); err != nil || libraries[1].ID != "tv" || libraries[1].Mode != SegmentModeOff {
		t.Fatalf("%+v %v", libraries, err)
	}
}

// The scan-stage detector this replaced left single-file guesses on existing
// installs. Measuring the file replaces them; one the owner approved stays,
// and no detection of its kind is laid over it.
func TestSegmentWorkerReplacesTheRetiredDetectorsGuesses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSegmentFixture(t)
	for i, cold := range []float64{0, 30, 60} {
		f.episode(fmt.Sprintf("s1e%d", i+1), 1, i+1, cold)
	}
	original, err := readSegmentCatchupFile(context.Background(), f.c.DB, f.objects["s1e1"], "r1")
	if err != nil {
		t.Fatal(err)
	}
	binding := original.binding()
	f.c.Exec(`INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,approved) VALUES
	 ('old-guess',?,'r1',?,'intro',0,30000000,.3,'luminance_opening_boundary:v1','old-result',0),
	 ('old-approved',?,'r1',?,'credits',380000000,391000000,.35,'sustained_end_title_luminance:v1','old-result',1)`,
		f.objects["s1e1"], binding, f.objects["s1e1"], binding)
	f.settle()
	var guess, approved bool
	for _, m := range f.markers("s1e1") {
		guess = guess || m.provenance == "luminance_opening_boundary:v1" && !m.gone
		approved = approved || m.provenance == "sustained_end_title_luminance:v1" && m.approved && !m.gone
	}
	if guess || !approved {
		t.Fatalf("old guess live %v, approved old marker live %v: %+v", guess, approved, f.markers("s1e1"))
	}
	if intro := f.live("s1e1", "intro"); len(intro) != 1 || intro[0].provenance != segmentdetect.IntroProvenance {
		t.Fatalf("%+v", f.markers("s1e1"))
	}
	if credits := f.live("s1e1", "credits"); len(credits) != 1 || !credits[0].approved {
		t.Fatalf("a detection was laid over the owner's credits: %+v", f.markers("s1e1"))
	}
}
