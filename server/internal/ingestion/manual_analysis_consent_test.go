package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Root §64: explicit manual analysis is admitted only when the remote source's
// own recorded consent allows it. These are unrun source vectors against the
// actual QueueItemAnalysis; production, schema and the existing tests are not
// changed.

type manualAnalysisFixture struct {
	db       *sql.DB
	service  *Service
	library  string
	source   string
	asset    string
	revision string
}

func newManualAnalysisFixture(t *testing.T, remote bool, relative string) manualAnalysisFixture {
	t.Helper()
	state := t.TempDir()
	root := filepath.Join(state, "media")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(state, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cat := catalog.New(db)
	lib, err := cat.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	// The source row is its own identity, not the library's. Read the exact row
	// this library was created with and use that id everywhere below.
	var source, incarnation string
	if err = db.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id=?`, lib.ID).Scan(&source, &incarnation); err != nil || source == "" || incarnation == "" {
		t.Fatalf("seeded library source: %q/%q %v", source, incarnation, err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET tier='custom',operations_json='["probe","trickplay"]',revision=revision+1 WHERE library_id=?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	if remote {
		if _, err = db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation) VALUES('remote','webdav','Remote',?,1)`, root); err != nil {
			t.Fatal(err)
		}
		identity := storage.RemoteDirectoryIdentity("remote", root) + ":generation:1"
		if result, err := db.Exec(`UPDATE library_sources SET kind='remote',remote_source_id='remote',root_identity=?,identity_confirmed=1 WHERE id=?`, identity, source); err != nil {
			t.Fatal(err)
		} else if n, _ := result.RowsAffected(); n != 1 {
			t.Fatalf("remote binding updated %d source rows", n)
		}
	}
	if _, err = db.Exec(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES('asset',?,16,23,'mkv','h264','aac',1920,1080,60)`, filepath.Join(root, relative)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('object',?,'asset',?,?,'revision-1','{}',16,23,'available')`, source, incarnation, relative); err != nil {
		t.Fatal(err)
	}
	if n := countManualAnalysis(t, db, `SELECT count(*) FROM inventory_objects WHERE id='object' AND source_id=? AND root_incarnation=? AND relative_path=?`, source, incarnation, relative); n != 1 {
		t.Fatalf("seeded inventory object rows: %d", n)
	}
	return manualAnalysisFixture{db: db, service: &Service{db: db}, library: lib.ID, source: source, asset: "asset", revision: "revision-1"}
}

// profile records the remote source's normalized read profile. "missing" leaves
// no row; "legacy" keeps the retired whole-file value with the new consent at
// its stored default; "off" is the same row with consent explicitly zero; "on"
// records the independent opt-in.
func (f manualAnalysisFixture) profile(t *testing.T, mode string) {
	t.Helper()
	var statement string
	switch mode {
	case "missing":
		return
	case "legacy":
		statement = `INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',1,'complete',1,8,10,0)`
	case "off":
		statement = `INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',1,'complete',0,8,10,0)`
	case "on":
		statement = `INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second,allow_advanced_media_analysis) VALUES('remote',1,'complete',0,8,10,0,1)`
	default:
		t.Fatalf("unknown profile mode %q", mode)
	}
	if _, err := f.db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

// activeAnalysisJob seeds the exact coalescing target QueueItemAnalysis would
// otherwise reuse, plus one failed stage it would otherwise reset.
func (f manualAnalysisFixture) activeAnalysisJob(t *testing.T) string {
	t.Helper()
	const job = "active-analysis"
	created := time.Now().UTC().Format(time.RFC3339Nano)
	updated := time.Now().UnixMilli()
	for _, command := range []struct {
		statement string
		args      []any
	}{
		{`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'queued',?)`, []any{job, f.library, created}},
		{`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode) SELECT ?,id,generation,incarnation,root_identity,(SELECT revision FROM library_scan_policies WHERE library_id=?),'analysis_enqueue','incremental' FROM library_sources WHERE id=?`, []any{job, f.library, f.source}},
		{`INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?)`, []any{f.source, job}},
		{`INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,attempt,processed_bytes,error_code,next_ms,updated_ms) SELECT ?,id,revision,(SELECT revision FROM library_scan_policies WHERE library_id=?),'trickplay','fixture','failed',3,12,'decode_failed',77,? FROM inventory_objects WHERE id='object' AND source_id=?`, []any{job, f.library, updated, f.source}},
	} {
		if _, err := f.db.Exec(command.statement, command.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, check := range []struct {
		statement string
		args      []any
	}{
		{`SELECT count(*) FROM inventory_runs WHERE job_id=? AND source_id=?`, []any{job, f.source}},
		{`SELECT count(*) FROM inventory_source_active WHERE job_id=? AND source_id=?`, []any{job, f.source}},
		{`SELECT count(*) FROM analysis_stage_runs WHERE job_id=? AND object_id='object' AND source_revision='revision-1' AND stage='trickplay'`, []any{job}},
	} {
		if n := f.count(t, check.statement, check.args...); n != 1 {
			t.Fatalf("seeded active analysis rows: %d for %s", n, check.statement)
		}
	}
	return job
}

func countManualAnalysis(t *testing.T, db *sql.DB, statement string, args ...any) int {
	t.Helper()
	var value int
	if err := db.QueryRow(statement, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func (f manualAnalysisFixture) count(t *testing.T, statement string, args ...any) int {
	t.Helper()
	return countManualAnalysis(t, f.db, statement, args...)
}

func TestManualAnalysisRefusesWithoutRecordedSourceConsent(t *testing.T) {
	for _, mode := range []string{"missing", "legacy", "off"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newManualAnalysisFixture(t, true, "film.mkv")
			f.profile(t, mode)
			job := f.activeAnalysisJob(t)
			if _, err := f.service.QueueItemAnalysis(ctx, f.library, f.asset, f.revision, nil); !errors.Is(err, mediaanalysis.ErrUnsupported) {
				t.Fatalf("source without recorded consent admitted analysis: %v", err)
			}
			if n := f.count(t, `SELECT count(*) FROM jobs`); n != 1 {
				t.Fatalf("refusal created a job: %d", n)
			}
			if n := f.count(t, `SELECT count(*) FROM inventory_analysis_queue`); n != 0 {
				t.Fatalf("refusal queued an object: %d", n)
			}
			var active string
			if err := f.db.QueryRow(`SELECT job_id FROM inventory_source_active WHERE source_id=?`, f.source).Scan(&active); err != nil || active != job {
				t.Fatalf("refusal replaced the active analysis job: %q %v", active, err)
			}
			var state, code string
			var attempt int
			var next int64
			if err := f.db.QueryRow(`SELECT state,attempt,error_code,next_ms FROM analysis_stage_runs WHERE job_id=? AND object_id='object' AND source_revision='revision-1' AND stage='trickplay'`, job).Scan(&state, &attempt, &code, &next); err != nil {
				t.Fatal(err)
			}
			if state != "failed" || attempt != 3 || code != "decode_failed" || next != 77 {
				t.Fatalf("refusal reset a failed stage: %q/%d/%q/%d", state, attempt, code, next)
			}
		})
	}
}

func TestManualAnalysisActorRefusalStaysFirst(t *testing.T) {
	actor := errors.New("actor refused")
	for _, tc := range []struct{ name, asset, profile string }{
		{name: "before source consent", asset: "asset", profile: "off"},
		{name: "before source facts", asset: "missing-asset", profile: "on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newManualAnalysisFixture(t, true, "film.mkv")
			f.profile(t, tc.profile)
			if _, err := f.service.QueueItemAnalysis(ctx, f.library, tc.asset, f.revision, func(*sql.Tx) error { return actor }); !errors.Is(err, actor) || errors.Is(err, mediaanalysis.ErrUnsupported) {
				t.Fatalf("actor refusal did not precede consent/source facts: %v", err)
			}
			if n := f.count(t, `SELECT count(*) FROM jobs`); n != 0 {
				t.Fatalf("actor refusal created a job: %d", n)
			}
			if n := f.count(t, `SELECT count(*) FROM inventory_analysis_queue`); n != 0 {
				t.Fatalf("actor refusal queued an object: %d", n)
			}
		})
	}
}

func TestManualAnalysisAcceptsNonDiscWithRecordedConsent(t *testing.T) {
	ctx := context.Background()
	t.Run("new job", func(t *testing.T) {
		f := newManualAnalysisFixture(t, true, "film.mkv")
		f.profile(t, "on")
		job, err := f.service.QueueItemAnalysis(ctx, f.library, f.asset, f.revision, nil)
		if err != nil || job.ID == "" || job.Phase != "analysis_enqueue" {
			t.Fatalf("recorded consent did not admit non-disc analysis: %+v %v", job, err)
		}
		var state, revision string
		if err = f.db.QueryRow(`SELECT state,revision FROM inventory_analysis_queue WHERE job_id=? AND object_id='object'`, job.ID).Scan(&state, &revision); err != nil || state != "pending" || revision != f.revision {
			t.Fatalf("accepted analysis queue row: %q/%q %v", state, revision, err)
		}
	})
	t.Run("resets failed stage", func(t *testing.T) {
		f := newManualAnalysisFixture(t, true, "film.mkv")
		f.profile(t, "on")
		existing := f.activeAnalysisJob(t)
		job, err := f.service.QueueItemAnalysis(ctx, f.library, f.asset, f.revision, nil)
		if err != nil || job.ID != existing {
			t.Fatalf("accepted analysis did not coalesce to the active job: %+v %v", job, err)
		}
		var state, code string
		var attempt int
		var next int64
		if err = f.db.QueryRow(`SELECT state,attempt,error_code,next_ms FROM analysis_stage_runs WHERE job_id=? AND object_id='object' AND source_revision='revision-1' AND stage='trickplay'`, existing).Scan(&state, &attempt, &code, &next); err != nil {
			t.Fatal(err)
		}
		if state != "pending" || attempt != 0 || code != "" || next != 0 {
			t.Fatalf("accepted analysis did not reset the failed stage: %q/%d/%q/%d", state, attempt, code, next)
		}
	})
}

func TestManualAnalysisLocalStillFollowsLibraryPolicy(t *testing.T) {
	ctx := context.Background()
	t.Run("deep policy", func(t *testing.T) {
		f := newManualAnalysisFixture(t, false, "film.mkv")
		job, err := f.service.QueueItemAnalysis(ctx, f.library, f.asset, f.revision, nil)
		if err != nil || job.ID == "" {
			t.Fatalf("local library with deep policy required a remote consent: %+v %v", job, err)
		}
	})
	t.Run("no deep policy", func(t *testing.T) {
		f := newManualAnalysisFixture(t, false, "film.mkv")
		if _, err := f.db.Exec(`UPDATE library_scan_policies SET tier='custom',operations_json='["probe"]',revision=revision+1 WHERE library_id=?`, f.library); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.QueueItemAnalysis(ctx, f.library, f.asset, f.revision, nil); !errors.Is(err, catalog.ErrAdminQuery) {
			t.Fatalf("local library ignored its own analysis policy: %v", err)
		}
	})
}

func TestManualAnalysisRefusesRemoteDiscImagesEvenWithConsent(t *testing.T) {
	for _, relative := range []string{"film.iso", "film.img"} {
		t.Run(relative, func(t *testing.T) {
			ctx := context.Background()
			f := newManualAnalysisFixture(t, true, relative)
			f.profile(t, "on")
			if _, err := f.service.QueueItemAnalysis(ctx, f.library, f.asset, f.revision, nil); !errors.Is(err, mediaanalysis.ErrUnsupported) {
				t.Fatalf("recorded consent admitted remote disc image analysis: %v", err)
			}
			if n := f.count(t, `SELECT count(*) FROM jobs`); n != 0 {
				t.Fatalf("disc image refusal created a job: %d", n)
			}
			if n := f.count(t, `SELECT count(*) FROM inventory_analysis_queue`); n != 0 {
				t.Fatalf("disc image refusal queued an object: %d", n)
			}
		})
	}
}
