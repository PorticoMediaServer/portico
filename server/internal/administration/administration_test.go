package administration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"runtime"
)

func newService(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := New(db)
	// The service derives its state directory from the open database file. On a
	// host where the temporary directory is itself a symlink the two spellings
	// differ, so they are compared after resolution and the resolved spelling is
	// what the rest of the test uses.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if service.StateDirectory() != resolved {
		t.Fatalf("state directory %q, want %q", service.StateDirectory(), resolved)
	}
	return service, db, service.StateDirectory()
}

// The composition root builds this service while other work may hold the single
// database connection, so construction must not read anything.
func TestConstructionNeverTouchesTheDatabase(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// One connection, held open for the duration: a constructor that queried the
	// database here would block until this transaction ended.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	done := make(chan *Service, 1)
	go func() { done <- New(db) }()
	select {
	case service := <-done:
		if service == nil {
			t.Fatal("no service")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("constructing the administration service blocked on the database")
	}
}

// seedLibrary installs one library with one item backed by two files, which is
// what the deletion tests operate on. The item is created through the write
// API; the returned id is its public id.
func seedLibrary(t *testing.T, db *sql.DB, root string) (string, string, []string) {
	t.Helper()
	library := "lib-1"
	media := filepath.Join(root, "media")
	if err := os.MkdirAll(media, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(media, "part1.mkv"), filepath.Join(media, "part2.mkv")}
	for index, path := range paths {
		if err := os.WriteFile(path, []byte(strconv.Itoa(index)+"0123456789"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,'Films','movie',?)`, library, media); err != nil {
		t.Fatal(err)
	}
	var entity int64
	err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		handle, e := compactcatalog.LibraryTx(ctx, tx, library)
		if e != nil {
			return e
		}
		if entity, _, e = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
			Library: handle, Kind: compactcatalog.Movie,
			Key: compactcatalog.ItemKey(media, paths[0], 0), Title: "The Lighthouse",
		}); e != nil {
			return e
		}
		for index, path := range paths {
			var asset int64
			if asset, _, e = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{
				Path: path, Size: 11, ModifiedNS: 0,
				Container: "mkv", VideoCodec: "h264", AudioCodec: "aac",
				Width: 1920, Height: 1080, Duration: 60,
			}); e != nil {
				return e
			}
			if e = compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{Part: index}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var item string
	if err = db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, entity).Scan(&item); err != nil {
		t.Fatal(err)
	}
	return library, item, paths
}

// entityID resolves a test item's public id to the integer the tables hold.
func entityID(t *testing.T, db *sql.DB, public string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, public).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func addCatalogTestEntity(t *testing.T, db *sql.DB, library, key string, kind compactcatalog.Kind, title string) string {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		handle, err := compactcatalog.LibraryTx(ctx, tx, library)
		if err != nil {
			return err
		}
		id, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
			Library: handle, Kind: kind, Key: key, Title: title,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var public string
	if err := db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, id).Scan(&public); err != nil {
		t.Fatal(err)
	}
	return public
}

func allow(context.Context, *sql.Tx) error { return nil }

func TestLibraryContinueWatchingSettingsRoundTrip(t *testing.T) {
	s, db, root := newService(t)
	library, _, _ := seedLibrary(t, db, root)
	ctx := context.Background()
	doc, err := s.LibrarySettingsFor(ctx, allow, library)
	if err != nil || doc.Settings.ContinueWatching.Weeks != 16 || doc.Settings.ContinueWatching.MaximumItems != 40 || doc.Settings.ContinueWatching.IncludeSeasonPremieres == nil || !*doc.Settings.ContinueWatching.IncludeSeasonPremieres || doc.Settings.ContinueWatching.VideoPlayedThreshold != 95 || doc.Settings.ContinueWatching.VideoCompletion != "earliest" {
		t.Fatalf("Continue Watching defaults: %+v %v", doc.Settings.ContinueWatching, err)
	}
	settings := doc.Settings
	settings.ContinueWatching.Weeks = 4
	settings.ContinueWatching.MaximumItems = 12
	settings.ContinueWatching.VideoPlayedThreshold = 85
	settings.ContinueWatching.VideoCompletion = "credits"
	no := false
	settings.ContinueWatching.IncludeSeasonPremieres = &no
	saved, err := s.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: settings, OperationID: "test-policy-key-1"})
	if err != nil || saved.Settings.ContinueWatching.Weeks != 4 || saved.Settings.ContinueWatching.MaximumItems != 12 || saved.Settings.ContinueWatching.IncludeSeasonPremieres == nil || *saved.Settings.ContinueWatching.IncludeSeasonPremieres || saved.Settings.ContinueWatching.VideoPlayedThreshold != 85 || saved.Settings.ContinueWatching.VideoCompletion != "credits" {
		t.Fatalf("Continue Watching save: %+v %v", saved.Settings.ContinueWatching, err)
	}
	settings.ContinueWatching.Weeks = 105
	if _, err = s.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: saved.Revision, Settings: settings, OperationID: "test-policy-key-2"}); err == nil {
		t.Fatal("unbounded Continue Watching window accepted")
	}
}

func TestLibrarySettingsWritesEffectivePolicyAndRejectsUnwiredKeys(t *testing.T) {
	service, db, root := newService(t)
	library, _, _ := seedLibrary(t, db, root)
	ctx := context.Background()
	// Online lookups are on by default (0079); an owner who withdrew the
	// server-wide consent must not get it back through a library save.
	if _, err := db.Exec(`UPDATE screen_metadata_consent SET confirmed=0,confirmed_at='2026-09-01T00:00:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	doc, err := service.LibrarySettingsFor(ctx, allow, library)
	if err != nil {
		t.Fatal(err)
	}
	settings := doc.Settings
	settings.AllowMediaDeletion = true
	settings.Providers = []ProviderSelection{{MediaKind: "movie", Provider: "tmdb", APIKey: "secret-key", Language: "en", Region: "GB"}}
	if _, err = service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: settings, OperationID: "test-policy-key-3"}); err == nil {
		t.Fatal("unwired credentials accepted")
	}
	settings.Providers[0].APIKey = ""
	settings.Analysis = []string{"probe", "trickplay"}
	settings.Navigation.TrickplayTileWidth = 480
	saved, err := service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: settings, OperationID: "test-policy-key-4"})
	if err != nil {
		t.Fatal(err)
	}
	var width int
	var operations, stored string
	var confirmed bool
	if err = db.QueryRow(`SELECT trickplay_tile_width,operations_json FROM library_scan_policies WHERE library_id=?`, library).Scan(&width, &operations); err != nil || width != 480 || !contains(operations, "trickplay") {
		t.Fatal(width, operations, err)
	}
	if err = db.QueryRow(`SELECT confirmed FROM screen_metadata_consent WHERE singleton=1`).Scan(&confirmed); err != nil || confirmed {
		t.Fatal("save granted remote consent", confirmed, err)
	}
	if _, err = service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: settings, OperationID: "test-policy-key-5"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale save", err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET revision=revision+1,operations_json='["probe"]' WHERE library_id=?`, library); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: saved.Revision, Settings: settings, OperationID: "test-policy-key-6"}); !errors.Is(err, ErrConflict) {
		t.Fatal("domain change did not invalidate admin revision", err)
	}
	read, err := service.LibrarySettingsFor(ctx, allow, library)
	if err != nil || contains(digestOf(read.Settings.Analysis), "trickplay") {
		t.Fatal(read, err)
	}
	for _, op := range read.Settings.Analysis {
		if op == "trickplay" {
			t.Fatal("stale admin JSON won")
		}
	}
	if err = db.QueryRow(`SELECT body FROM admin_documents WHERE scope=?`, libraryScope(library)).Scan(&stored); err != nil || contains(stored, "secret-key") {
		t.Fatal("credential persisted", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestLibrarySettingsRefusesUnsatisfiedAnalysisDependency(t *testing.T) {
	service, db, root := newService(t)
	library, _, _ := seedLibrary(t, db, root)
	settings := DefaultLibrarySettings()
	// Trickplay needs probe; asking for trickplay alone would never run.
	settings.Analysis = []string{"trickplay"}
	_, err := service.SaveLibrarySettings(context.Background(), allow, library, Change[LibrarySettings]{ExpectedRevision: 1, Settings: settings, OperationID: "test-policy-key-7"})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Fields[0] != "settings.analysis" {
		t.Fatalf("error %v, want a validation failure naming settings.analysis", err)
	}
}

func TestBrowseRootsDirectoryPagingAndRefusals(t *testing.T) {
	service, _, root := newService(t)
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "browse")
	media, err := filepath.EvalSymlinks(filepath.Dir(media))
	if err != nil {
		t.Fatal(err)
	}
	media = filepath.Join(media, "browse")
	service.PickerRoots = []string{media}
	for _, name := range []string{"Alpha", "Beta", "Gamma", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(media, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	roots, err := service.Browse(ctx, "", "", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots.Roots) == 0 || roots.Platform == "" || roots.Separator == "" {
		t.Fatalf("every platform must publish at least one root: %+v", roots)
	}
	if len(roots.Roots) != 1 || roots.Roots[0].Path != media {
		t.Fatalf("picker exposed unconfigured roots: %+v", roots.Roots)
	}
	if err := os.WriteFile(filepath.Join(media, "notes.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := service.Browse(ctx, media, "", 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Name != "Alpha" || page.NextCursor == "" || page.Writable {
		t.Fatalf("first page %+v", page)
	}
	second, err := service.Browse(ctx, media, page.NextCursor, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.Entries[0].Name != "Gamma" || second.NextCursor != "" {
		t.Fatalf("second page %+v", second)
	}
	// Files appear only when asked for; dotfiles never do.
	withFiles, err := service.Browse(ctx, media, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range withFiles.Entries {
		names[entry.Name] = true
	}
	if !names["notes.txt"] || names[".hidden"] {
		t.Fatalf("entries %+v", withFiles.Entries)
	}
	if _, err = service.Browse(ctx, "relative/path", "", 0, false); !errors.Is(err, ErrInput) {
		t.Fatalf("error %v, want a refusal for a relative path", err)
	}
	if _, err = service.Browse(ctx, filepath.Join(media, "missing"), "", 0, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error %v, want not found", err)
	}
	if _, err = service.Browse(ctx, root, "", 0, true); !errors.Is(err, ErrDenied) {
		t.Fatalf("state directory was browsable: %v", err)
	}
	if err := os.Symlink(root, filepath.Join(media, "state-link")); err == nil {
		if _, err = service.Browse(ctx, filepath.Join(media, "state-link"), "", 0, true); !errors.Is(err, ErrDenied) {
			t.Fatalf("symlink into state was browsable: %v", err)
		}
	}
	if entries, err := os.ReadDir(media); err != nil {
		t.Fatal(err)
	} else {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".portico-write-probe-") {
				t.Fatal("browse created a write probe")
			}
		}
	}
}

func TestDVRTemplatesAndTunerConflicts(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()
	document, err := service.DVRSettings(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.FolderTokens) != 0 || len(document.Enumerations["keepMode"]) == 0 {
		t.Fatalf("the page must publish its vocabularies: %+v", document)
	}
	settings := document.Settings
	for _, bad := range []string{"{series}/{unknown}", "../{title}", "/{title}", "Season {season}", "{title:02}"} {
		settings.FolderTemplate = bad
		if _, err = service.SaveDVRSettings(ctx, allow, Change[DVRDefaults]{ExpectedRevision: 1, Settings: settings, OperationID: "test-policy-key-16"}); err == nil {
			t.Fatalf("template %q should have been refused", bad)
		}
	}
	settings.FolderTemplate = ""
	// Live viewing keeps its tuner unless the owner says otherwise (15 Oct).
	if settings.LiveTunerConflict != "keep-watching" || len(document.Enumerations["liveTunerConflict"]) != 2 {
		t.Fatalf("live tuner conflict default %q %v", settings.LiveTunerConflict, document.Enumerations["liveTunerConflict"])
	}
	settings.LiveTunerConflict = "take-it"
	if _, err = service.SaveDVRSettings(ctx, allow, Change[DVRDefaults]{ExpectedRevision: 1, Settings: settings, OperationID: "test-policy-key-18"}); err == nil {
		t.Fatal("an unknown live tuner policy should have been refused")
	}
	settings.LiveTunerConflict = "stop-watching"
	settings.PrePaddingMs = 120000
	saved, err := service.SaveDVRSettings(ctx, allow, Change[DVRDefaults]{ExpectedRevision: 1, Settings: settings, OperationID: "test-policy-key-17"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 2 || saved.Settings.LiveTunerConflict != "stop-watching" {
		t.Fatalf("revision %d policy %q", saved.Revision, saved.Settings.LiveTunerConflict)
	}
	// Three overlapping recordings against two tuners is one conflict.
	list := []TunerAssignment{
		{ChannelID: "one", StartsAt: "2026-09-16T20:00:00Z", EndsAt: "2026-09-16T21:00:00Z"},
		{ChannelID: "two", StartsAt: "2026-09-16T20:30:00Z", EndsAt: "2026-09-16T21:30:00Z"},
		{ChannelID: "three", StartsAt: "2026-09-16T20:45:00Z", EndsAt: "2026-09-16T21:15:00Z"},
	}
	if excess := overlapExcess(list, 2); excess != 1 {
		t.Fatalf("overlap excess %d, want 1", excess)
	}
	if excess := overlapExcess(list, 3); excess != 0 {
		t.Fatalf("overlap excess %d, want 0", excess)
	}
}

func TestMaintenanceRetentionMaximaAndUpdateReport(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()
	document, err := service.MaintenanceSettings(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Cadences) == 0 || document.Maxima["logs"] != 365 {
		t.Fatalf("maintenance vocabularies %+v", document)
	}
	settings := document.Settings
	settings.Retention["logs"] = 4000
	if _, err = service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: 1, Settings: settings, OperationID: "test-policy-key-18"}); err == nil {
		t.Fatal("a retention beyond the published maximum must be refused")
	}
	settings.Retention["logs"] = 365
	settings.Windows[0].Timezone = "Not/AZone"
	if _, err = service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: 1, Settings: settings, OperationID: "test-policy-key-19"}); err == nil {
		t.Fatal("an unloadable timezone must be refused")
	}
	settings.Windows[0].Timezone = "Europe/London"
	settings.Windows[0].Cadence = "weekends"
	settings.Windows[0].Days = []string{"saturday", "sunday"}
	saved, err := service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: 1, Settings: settings, OperationID: "maintenance-1"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 2 || saved.Settings.Windows[0].Cadence != "weekends" {
		t.Fatalf("saved %+v", saved.Settings.Windows)
	}
	// With no feed the report says so and never claims an update.
	current := UpdateBuild{Version: "1.2.0", BuildID: "b1"}
	report := service.Updates(ctx, current, "stable", "")
	if report.Status != "no-feed" || report.State != "unconfigured" || report.UpdateAvailable || report.AutoInstall {
		t.Fatalf("report %+v", report)
	}
	service.Fetch = func(context.Context, string) ([]byte, error) {
		return []byte(`{"channels":{"stable":[{"version":"1.1.0"},{"version":"1.3.0","notes":"Faster scans","url":"https://releases.example/notes/1.3.0"}],"beta":[{"version":"2.0.0-rc1"}]}}`), nil
	}
	report = service.Updates(ctx, current, "stable", "https://releases.example/portico.json")
	if report.Status != "update-available" || report.State != "available" || report.Latest == nil || report.Latest.Version != "1.3.0" || report.NotesURL != "https://releases.example/notes/1.3.0" || report.AutoInstall {
		t.Fatalf("report %+v", report)
	}
	// A pre-release sorts below the same numbers without one.
	report = service.Updates(ctx, UpdateBuild{Version: "2.0.0"}, "beta", "https://releases.example/portico.json")
	if report.UpdateAvailable {
		t.Fatalf("2.0.0-rc1 must not read as newer than 2.0.0: %+v", report.Latest)
	}
	if report.State != "current" {
		t.Fatalf("a non-newer release changed the state: %+v", report)
	}
	if safeUpdateURL("javascript:alert(1)") != "" || safeUpdateURL("https://user:secret@releases.example/notes") != "" || safeUpdateURL("http://releases.example/notes") != "" {
		t.Fatal("unsafe release notes URL accepted")
	}
}

func TestMaintenanceSavePreservesBackgroundSpeed(t *testing.T) {
	service, _, _ := newService(t)
	ctx := context.Background()
	current, err := service.MaintenanceSettings(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	settings := current.Settings
	settings.BackgroundSpeed = "fast"
	saved, err := service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: current.Revision, Settings: settings, OperationID: "priority-normal"})
	if err != nil {
		t.Fatal(err)
	}
	settings = saved.Settings
	// An unrelated edit keeps the authored speed.
	saved, err = service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: saved.Revision, Settings: settings, OperationID: "retention-only"})
	if err != nil || saved.Settings.BackgroundSpeed != "fast" {
		t.Fatalf("omitted priority reset: %+v, %v", saved.Settings, err)
	}
	loaded, err := service.MaintenanceSettings(ctx, allow)
	if err != nil || loaded.Settings.BackgroundSpeed != "fast" {
		t.Fatalf("stored priority: %+v, %v", loaded.Settings, err)
	}
}

func TestStorageReportAndCleanupPolicy(t *testing.T) {
	service, _, root := newService(t)
	ctx := context.Background()
	analysis := filepath.Join(root, "analysis", "tiles")
	if err := os.MkdirAll(analysis, 0700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(analysis, "old.jpg")
	if err := os.WriteFile(old, make([]byte, 2048), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().AddDate(0, 0, -400)
	if err := os.Chtimes(old, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(analysis, "fresh.jpg")
	if err := os.WriteFile(fresh, make([]byte, 1024), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := service.Storage(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, category := range report.Categories {
		if category.ID == "trickplay" {
			found = category.Bytes == 3072 && category.FileCount == 2 && category.Present
		}
		if category.ID == "recordings" && category.Cleanable {
			t.Fatal("recordings are content and must never be cleanable")
		}
	}
	if !found || report.TotalBytes == 0 {
		t.Fatalf("storage report %+v", report.Categories)
	}
	if _, err = service.Cleanup(ctx, allow, "recordings", CleanupRequest{Confirmation: "recordings", OperationID: "test-policy-key-20"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("error %v, want denied", err)
	}
	if _, err = service.Cleanup(ctx, allow, "logs", CleanupRequest{Confirmation: "wrong", OperationID: "test-policy-key-21"}); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("error %v, want a confirmation mismatch", err)
	}
	// Generated assets are managed by their domain owner, never a raw tree sweep.
	if _, err = service.Cleanup(ctx, allow, "trickplay", CleanupRequest{Confirmation: "trickplay", OperationID: "cleanup-1"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected owner-managed cleanup refusal, got %v", err)
	}
	if _, err = os.Stat(old); err != nil {
		t.Fatal("old referenced artifacts must survive")
	}
	if _, err = os.Stat(fresh); err != nil {
		t.Fatal("a file inside the retention window must survive")
	}
}

// A link inside a permitted folder that points outside it is not a way to list
// what is outside: the folder that would actually be listed is checked, not
// only the name that was asked for.
func TestFolderPickerDoesNotFollowALinkOutOfThePermittedFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege on Windows")
	}
	ctx := context.Background()
	service, _, _ := newService(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(base, "media")
	private := filepath.Join(base, "private")
	for _, dir := range []string{filepath.Join(media, "Films"), filepath.Join(private, "Secrets")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(private, filepath.Join(media, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(media, "Films"), filepath.Join(media, "inside")); err != nil {
		t.Fatal(err)
	}
	service.PickerRoots = []string{media}
	if _, err := service.Browse(ctx, media, "", 0, false); err != nil {
		t.Fatalf("the permitted folder: %v", err)
	}
	// A link that stays inside is followed.
	if page, err := service.Browse(ctx, filepath.Join(media, "inside"), "", 0, false); err != nil || page.Path != filepath.Join(media, "Films") {
		t.Fatalf("a link that stays inside the permitted folder: %+v %v", page.Path, err)
	}
	// A link that leaves is refused, and so is anything under it.
	for _, path := range []string{filepath.Join(media, "escape"), filepath.Join(media, "escape", "Secrets")} {
		if page, err := service.Browse(ctx, path, "", 0, false); !errors.Is(err, ErrDenied) {
			t.Fatalf("%s was listed through a link out of the permitted folder: %+v %v", path, page, err)
		}
	}
	// The outside folder asked for directly is refused as before.
	if _, err := service.Browse(ctx, private, "", 0, false); !errors.Is(err, ErrDenied) {
		t.Fatalf("a folder outside the permitted ones: %v", err)
	}
}
