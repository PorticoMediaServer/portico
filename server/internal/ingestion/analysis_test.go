package ingestion

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// A TV file whose name yields a naming issue (an over-wide episode range) and carries an embedded text subtitle must surface the
// naming issue without recording subtitle_text_import_failed. Before the fix,
// the item lookup returned sql.ErrNoRows and fell through to the subtitle
// warning, hiding the real issue.
func TestSubtitleTextImportSkippedWhenFileHasNoItem(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(root, "media")
	if err := os.MkdirAll(filepath.Join(media, "Show", "Season 01"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "subs.srt"), []byte("1\n00:00:00,000 --> 00:00:02,000\nHello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(media, "Show", "Season 01", "Show - S01E01-E09.mkv")
	if output, err := exec.Command(binary, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:size=64x64:rate=1", "-i", filepath.Join(root, "subs.srt"), "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:s", "srt", out).CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, output)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	helper, _ := os.Executable()
	store := storage.New(helper)
	cat.SetStorage(store)
	meta, err := localmetadata.New(filepath.Join(root, "art"), binary, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "subobjects"), 0700); err != nil {
		t.Fatal(err)
	}
	subSvc, err := subtitles.New(subtitles.Options{
		DB:           db,
		Directory:    filepath.Join(root, "subobjects"),
		Storage:      store,
		HelperBinary: helper,
		Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
			return p, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db, cat, assets.Probe{Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	scanner.LocalMetadata = meta
	scanner.Subtitles = subSvc
	lib, err := cat.Create("TV", "tv", media)
	if err != nil {
		t.Fatal(err)
	}
	job, err := scanner.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := false
	for n := 0; n < 20; n++ {
		scanner.process(context.Background(), job.ID, lib.ID)
		state, err := scanner.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			done = true
			t.Logf("scan finished with status %q warnings=%d", state.Status, state.Warnings)
			break
		}
		if state.Status == "failed" || state.Status == "cancelled" {
			t.Fatalf("scan did not finish: %+v", state)
		}
	}
	if !done {
		t.Fatal("scan unfinished")
	}
	issues, _, err := cat.EpisodeIssues(lib.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	foundNaming := false
	for _, issue := range issues {
		if issue.Code == "ambiguous_episode_range" {
			foundNaming = true
		}
		if issue.Code == "subtitle_text_import_failed" {
			t.Fatalf("naming issue hidden behind subtitle warning: %+v", issues)
		}
	}
	if !foundNaming {
		t.Fatalf("naming issue missing from scan warnings: %+v", issues)
	}
	var objectWarnings, changeWarnings int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE analysis_error='subtitle_text_import_failed'`).Scan(&objectWarnings); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM inventory_changes WHERE kind='subtitle_text_import_failed'`).Scan(&changeWarnings); err != nil {
		t.Fatal(err)
	}
	if objectWarnings != 0 || changeWarnings != 0 {
		t.Fatalf("subtitle_text_import_failed recorded (objects=%d changes=%d)", objectWarnings, changeWarnings)
	}
}
