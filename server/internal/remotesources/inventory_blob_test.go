package remotesources

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func inventoryBlobFixture(t *testing.T) (observationFixture, storage.InventoryRequest, storage.InventoryBlobAdmission) {
	t.Helper()
	f := newObservationFixture(t)
	ctx := context.Background()
	identities, owner, _ := observationOwner(t, f)
	profile, _ := SourceReadPreset("ask")
	write, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ApplySourceReadProfileTx(ctx, write.Tx(), identities, owner, SourceReadProfileChange{SourceID: "remote", Generation: "1", Profile: profile}); err != nil {
		write.Rollback()
		t.Fatal(err)
	}
	if err = write.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`UPDATE inventory_runs SET policy_revision=(SELECT revision FROM library_scan_policies WHERE library_id=?)`, f.library); err != nil {
		t.Fatal(err)
	}
	if _, err = f.record(t, scanFacts("media"), 0); err != nil {
		t.Fatal(err)
	}
	sidecar := f
	sidecar.file = filepath.Join(filepath.Dir(f.file), "film.nfo")
	facts := scanFacts("sidecar")
	facts.Facts.Object = "film.nfo"
	if _, err = sidecar.record(t, facts, 0); err != nil {
		t.Fatal(err)
	}
	view, err := dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	source, err := catalog.InventoryFenceTx(ctx, view.Tx(), f.job)
	if err != nil {
		view.Rollback()
		t.Fatal(err)
	}
	_, media, err := f.s.ScanSnapshotTx(ctx, view.Tx(), f.file)
	view.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	revision, _, err := catalog.ObservationRevision(source, media)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('media-asset',?,16,23,'mkv','h264','aac',1920,1080,60)`, []any{f.file}},
		{`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('media',?,'media-asset',?, 'film.mkv',?,'{}',16,23,'available')`, []any{source.ID, source.Incarnation, revision}},
		{`INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) SELECT ?, 'media',?,revision,'pending' FROM library_scan_policies WHERE library_id=?`, []any{f.job, revision, f.library}},
	} {
		if _, err = f.s.db.Exec(command.query, command.args...); err != nil {
			t.Fatal(err)
		}
	}
	var policy int64
	if err = f.s.db.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id=?`, f.library).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	request := storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, RelativePath: "film.nfo", FollowSymlinks: source.FollowSymlinks}
	a := storage.InventoryBlobAdmission{Purpose: storage.InventoryBlobAnalysisSidecar, JobID: f.job, LibraryID: f.library, LibrarySourceID: source.ID, RootIncarnation: source.Incarnation, RootIdentity: source.RootIdentity, SourceGeneration: source.Generation, PolicyRevision: policy, ObjectID: "media", SourceRevision: revision, Operation: "local_metadata"}
	return f, request, a
}

func TestInventoryBlobFencesActualJobMediaTargetAndSeparateCandidate(t *testing.T) {
	f, request, admission := inventoryBlobFixture(t)
	ctx := context.Background()
	for _, mutation := range []struct{ name, sql string }{
		{"cancelled-job", `UPDATE jobs SET status='cancelled' WHERE id='scan'`},
		{"job-root", `UPDATE inventory_runs SET root_incarnation='replacement'`},
		{"source-root", `UPDATE library_sources SET root_identity='replacement'`},
		{"source-disabled", `UPDATE library_sources SET enabled=0`},
		{"policy-revision", `UPDATE library_scan_policies SET revision=revision+1`},
		{"operation-excluded", `UPDATE library_scan_policies SET tier='custom',operations_json='["probe"]'`},
		{"media-revision", `UPDATE inventory_objects SET revision='new-media'`},
		{"media-retired", `UPDATE inventory_objects SET retired=1`},
		{"queue-revision", `UPDATE inventory_analysis_queue SET revision='new-media'`},
		{"queue-complete", `UPDATE inventory_analysis_queue SET state='complete'`},
		{"candidate-aba", `UPDATE remote_scan_observations SET revision=revision+2 WHERE relative_path='film.nfo'`},
		{"media-observation-aba", `UPDATE remote_scan_observations SET revision=revision+2 WHERE relative_path='film.mkv'`},
		{"media-observation-facts", `UPDATE remote_scan_observations SET modified_ns=modified_ns+1 WHERE relative_path='film.mkv'`},
		{"profile-revision", `UPDATE remote_source_read_profiles SET revision=revision+1`},
		{"source-generation", `UPDATE remote_sources SET generation=generation+1`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			write, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err != nil {
				t.Fatal(err)
			}
			defer write.Rollback()
			capture, err := f.s.captureInventoryBlobTx(ctx, write.Tx(), f.library, request, admission, 1024)
			if err != nil {
				t.Fatal("positive stored facts", err)
			}
			if capture.binding.Observation.Object != "film.nfo" || capture.admission.ObjectID != "media" || capture.binding.ScanRevision == admission.SourceRevision {
				t.Fatal("candidate transport evidence aliased media catalogue revision")
			}
			if _, err = write.Tx().ExecContext(ctx, mutation.sql); err != nil {
				t.Fatal(err)
			}
			if err = capture.checkTx(ctx, write.Tx()); err == nil {
				t.Fatal("changed stored facts retained blob authority")
			}
		})
	}
}

func TestInventoryBlobRejectsUnknownOrCrossPurposeFactsBeforeIO(t *testing.T) {
	f, request, a := inventoryBlobFixture(t)
	ctx := context.Background()
	for _, mutate := range []func(*storage.InventoryRequest, *storage.InventoryBlobAdmission){
		func(_ *storage.InventoryRequest, a *storage.InventoryBlobAdmission) {
			a.Operation = "episode_thumbnail"
		},
		func(_ *storage.InventoryRequest, a *storage.InventoryBlobAdmission) { a.JobID = "unknown" },
		func(_ *storage.InventoryRequest, a *storage.InventoryBlobAdmission) { a.LibraryID = "other" },
		func(_ *storage.InventoryRequest, a *storage.InventoryBlobAdmission) { a.ListingID = "borrowed" },
		func(r *storage.InventoryRequest, _ *storage.InventoryBlobAdmission) { r.RelativePath = "../escape.nfo" },
		func(r *storage.InventoryRequest, _ *storage.InventoryBlobAdmission) { r.RelativePath = "film.mkv" },
		func(r *storage.InventoryRequest, _ *storage.InventoryBlobAdmission) { r.RelativePath = "other.mkv" },
		func(_ *storage.InventoryRequest, a *storage.InventoryBlobAdmission) {
			a.Purpose = storage.InventoryBlobIgnore
		},
		func(_ *storage.InventoryRequest, a *storage.InventoryBlobAdmission) { a.Purpose = "analysis" },
	} {
		r, a := request, a
		mutate(&r, &a)
		view, err := dbwork.BeginSnapshot(ctx, f.s.db)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.s.captureInventoryBlobTx(ctx, view.Tx(), f.library, r, a, 1024)
		view.Rollback()
		if err == nil {
			t.Fatal("unknown or forged facts admitted", r, a)
		}
	}
	if _, err := f.s.db.Exec(`DELETE FROM remote_source_read_profiles`); err != nil {
		t.Fatal(err)
	}
	view, err := dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	if _, err = f.s.captureInventoryBlobTx(ctx, view.Tx(), f.library, request, a, 1024); !errors.Is(err, ErrInventoryBlobSourceTier) {
		t.Fatal("missing profile granted work or invented a provider/STRM failure", err)
	}
}

func TestInventorySidecarFormatsDoNotGrantOtherMediaReads(t *testing.T) {
	for operation, files := range map[string][]string{
		"probe":          {"poster.tbn"},
		"local_metadata": {"poster.TBN", "movie.nfo", "metadata.opf", "book.cue", "cover.JPG", "folder.webp", "book.mp3.portico.json", "metadata.json", "desc.txt", "reader.txt"},
		"subtitles":      {"movie.en.srt", "movie.sup", "movie.idx", "movie.sub", "movie.ttml"},
	} {
		for _, file := range files {
			if !inventorySidecarCandidate(operation, file) {
				t.Fatal("existing metadata input refused", operation, file)
			}
		}
	}
	for _, operation := range []string{"probe", "local_metadata", "subtitles", "decode", ""} {
		for _, file := range []string{"other.mkv", "other.mp3", "book.epub", "document.pdf", "comic.cbz", "config.json", "private.txt"} {
			if inventorySidecarCandidate(operation, file) {
				t.Fatal("metadata operation granted a different content input", operation, file)
			}
		}
	}
}

func TestInventoryThumbnailAliasDoesNotGrantOtherOperations(t *testing.T) {
	for _, operation := range []string{"subtitles", "decode", "episode_thumbnail", "reading", "playback", ""} {
		if inventorySidecarCandidate(operation, "poster.tbn") {
			t.Fatal("image alias granted another operation", operation)
		}
	}
}
