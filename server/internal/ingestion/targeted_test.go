package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestReportedPathsScanOnlyTheReportedFolders(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(root, "media")
	for _, dir := range []string{"Alpha (2000)", "Beta (2001)", "Gamma (2002)"} {
		if err = os.MkdirAll(filepath.Join(media, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(media, dir, dir+".mp4"), []byte("first"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(filepath.Join(root, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalog.New(db)
	lib, err := c.Create("Movies", "movie", media)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	s := New(db, c, assets.Probe{})
	ctx := context.Background()
	finish := func(job Job) {
		t.Helper()
		for i := 0; i < 200; i++ {
			s.process(ctx, job.ID, lib.ID)
			state, e := s.Get(job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.Status == "complete" {
				return
			}
			if state.Status == "failed" {
				t.Fatal(state)
			}
		}
		state, _ := s.Get(job.ID)
		t.Fatalf("scan did not finish: %+v", state)
	}
	first, err := s.QueueMode(ctx, lib.ID, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	finish(first)

	// Refusals: nothing readable, too much, and an authority that says no.
	if _, err = s.ReportPaths(ctx, nil, nil); !errors.Is(err, ErrPathReport) {
		t.Fatalf("empty report: %v", err)
	}
	if _, err = s.ReportPaths(ctx, make([]string, MaxReportedPaths+1), nil); !errors.Is(err, ErrPathReport) {
		t.Fatalf("oversized report: %v", err)
	}
	denied := errors.New("denied")
	if _, err = s.ReportPaths(ctx, []string{media}, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatalf("unauthorised report: %v", err)
	}
	var waiting int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_path_notices`).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatalf("a refused report was recorded: %d %v", waiting, err)
	}

	// A new file in Beta, a replaced file in Gamma that is NOT reported, and a
	// file removed from Alpha that is not reported either.
	if err = os.WriteFile(filepath.Join(media, "Beta (2001)", "Beta (2001) - extra.mp4"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(media, "Alpha (2000)", "Alpha (2000).mp4")); err != nil {
		t.Fatal(err)
	}
	report, err := s.ReportPaths(ctx, []string{
		filepath.Join(media, "Beta (2001)", "Beta (2001) - extra.mp4"), // a new file: its known folder
		filepath.Join(media, "Beta (2001)"),                            // the same folder again
		filepath.Join(root, "elsewhere", "file.mp4"),                   // outside every library
		"relative/path.mp4",                                            // not absolute
		filepath.Join(media, "..", "media", "..", "catalog.sqlite"),    // climbs out of the library
		strings.Repeat("/x", MaxPathBytes),                             // too long
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	statuses := []string{}
	for _, p := range report.Paths {
		statuses = append(statuses, p.Status+":"+p.Folder)
	}
	want := "queued:Beta (2001),queued:Beta (2001),outside_libraries:,invalid:,outside_libraries:,invalid:"
	if got := strings.Join(statuses, ","); got != want {
		t.Fatalf("report\n got %s\nwant %s", got, want)
	}
	if len(report.Jobs) != 1 || report.Jobs[0].ScanMode != "incremental" {
		t.Fatalf("jobs: %+v", report.Jobs)
	}
	job := report.Jobs[0]
	finish(job)
	rows, err := db.Query(`SELECT relative_path,skipped FROM inventory_directories WHERE job_id=? ORDER BY relative_path`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	visited := []string{}
	for rows.Next() {
		var path string
		var skipped int
		if err = rows.Scan(&path, &skipped); err != nil {
			t.Fatal(err)
		}
		if skipped != 0 {
			t.Fatalf("a reported folder was skipped: %s", path)
		}
		visited = append(visited, path)
	}
	rows.Close()
	if strings.Join(visited, ",") != "Beta (2001)" {
		t.Fatalf("the scan visited %v", visited)
	}
	var available, betaFiles, alphaObservations int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE retired=0 AND state='available'`).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE retired=0 AND directory_path='Beta (2001)'`).Scan(&betaFiles); err != nil {
		t.Fatal(err)
	}
	// Alpha's file is gone from disk but Alpha was not reported: nothing about
	// it may be judged by this run.
	if err = db.QueryRow(`SELECT missing_observations FROM inventory_objects WHERE relative_path=?`, filepath.Join("Alpha (2000)", "Alpha (2000).mp4")).Scan(&alphaObservations); err != nil {
		t.Fatal(err)
	}
	if betaFiles != 2 || available != 4 || alphaObservations != 0 {
		t.Fatalf("after the limited scan: beta=%d available=%d alpha missing observations=%d", betaFiles, available, alphaObservations)
	}
	if err = db.QueryRow(`SELECT count(*) FROM inventory_path_notices`).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatalf("reports were not consumed: %d %v", waiting, err)
	}

	// Reporting a known removed file marks it missing before the queued scan runs.
	report, err = s.ReportPaths(ctx, []string{filepath.Join(media, "Alpha (2000)", "Alpha (2000).mp4")}, nil)
	if err != nil || len(report.Jobs) != 1 || report.Paths[0].Folder != "Alpha (2000)" {
		t.Fatalf("removed file report: %v %+v", err, report)
	}

	var missingState string
	if err = db.QueryRow(`SELECT state FROM inventory_objects WHERE relative_path=?`, filepath.Join("Alpha (2000)", "Alpha (2000).mp4")).Scan(&missingState); err != nil || missingState != "missing" {
		t.Fatal("removal was not immediate", missingState, err)
	}
	finish(report.Jobs[0])
	if err = db.QueryRow(`SELECT missing_observations FROM inventory_objects WHERE relative_path=?`, filepath.Join("Alpha (2000)", "Alpha (2000).mp4")).Scan(&alphaObservations); err != nil || alphaObservations < 1 {
		t.Fatalf("the removed file was not noticed: %d %v", alphaObservations, err)
	}

	// A folder that was deleted outright is reported by the folder itself. It
	// can no longer be listed, so the run looks from the folder above, sees it
	// is gone, and does not fail.
	if err = os.MkdirAll(filepath.Join(media, "Delta (2003)", "Extras"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(media, "Delta (2003)", "Extras", "Delta (2003) - trailer.mp4"), []byte("extra"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err = s.ReportPaths(ctx, []string{filepath.Join(media, "Delta (2003)")}, nil)
	if err != nil || len(report.Jobs) != 1 {
		t.Fatalf("new folder report: %v %+v", err, report)
	}
	finish(report.Jobs[0])
	var extras int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE retired=0 AND directory_path=?`, filepath.Join("Delta (2003)", "Extras")).Scan(&extras); err != nil || extras != 1 {
		t.Fatalf("a new folder two levels deep was not found: %d %v", extras, err)
	}
	if err = os.RemoveAll(filepath.Join(media, "Delta (2003)", "Extras")); err != nil {
		t.Fatal(err)
	}
	report, err = s.ReportPaths(ctx, []string{filepath.Join(media, "Delta (2003)", "Extras")}, nil)
	if err != nil || len(report.Jobs) != 1 || report.Paths[0].Folder != filepath.Join("Delta (2003)", "Extras") {
		t.Fatalf("deleted folder report: %v %+v", err, report)
	}
	finish(report.Jobs[0])
	var observed int
	if err = db.QueryRow(`SELECT missing_observations FROM inventory_objects WHERE relative_path=?`, filepath.Join("Delta (2003)", "Extras", "Delta (2003) - trailer.mp4")).Scan(&observed); err != nil || observed != 1 {
		t.Fatalf("the files of a deleted folder were not noticed: %d %v", observed, err)
	}

	// A report that arrives while the folder is being scanned waits, and is
	// scanned when that run ends.
	busy, err := s.QueueMode(ctx, lib.ID, "", "incremental", nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err = s.ReportPaths(ctx, []string{filepath.Join(media, "Gamma (2002)", "New Folder", "file.mp4")}, nil)
	if err != nil || len(report.Jobs) != 1 || report.Jobs[0].ID != busy.ID || report.Paths[0].Folder != "Gamma (2002)" {
		t.Fatalf("report during a scan: %v %+v", err, report)
	}
	if err = db.QueryRow(`SELECT count(*) FROM inventory_path_notices`).Scan(&waiting); err != nil || waiting != 1 {
		t.Fatalf("the report was not kept: %d %v", waiting, err)
	}
	finish(busy)
	s.startWaitingReports(ctx)
	var targets int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_run_targets t JOIN inventory_source_active a ON a.job_id=t.job_id WHERE t.relative_path='Gamma (2002)'`).Scan(&targets); err != nil || targets != 1 {
		t.Fatalf("the waiting report did not start a scan: %d %v", targets, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM inventory_path_notices`).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatalf("the waiting report was not consumed: %d %v", waiting, err)
	}
}
