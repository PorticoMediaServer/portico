package remotesources

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestScanProducerRejectsMissingJobAndEscapesBeforeProcess(t *testing.T) {
	f := newObservationFixture(t)
	for _, request := range []struct{ job, file string }{{"", f.file}, {"missing", f.file}, {f.job, filepath.Join(filepath.Dir(f.file), "..", "outside")}, {f.job, filepath.Dir(f.file)}} {
		if _, err := f.s.ObserveScanFile(context.Background(), request.job, request.file); err == nil {
			t.Fatal("invalid inventory metadata request accepted", request)
		}
	}
	if len(f.s.serveLeases) != 0 {
		t.Fatal("unadmitted inventory metadata launched a process")
	}
}

// These cases prove real transactional inventory/profile/root checks only.
// The separately tagged integration test qualifies upstream process metadata.
func TestScanProducerCurrentJobPolicyRootAndABAFences(t *testing.T) {
	for _, change := range []string{"job", "library_root", "source_root", "generation", "profile", "library_policy"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			f := newObservationFixture(t)
			view, err := dbwork.BeginSnapshot(ctx, f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			source, err := catalog.InventoryFenceTx(ctx, view.Tx(), f.job)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := SourceReadProfileTx(ctx, view.Tx(), "remote")
			if err != nil {
				t.Fatal(err)
			}
			view.Rollback()
			capture := &ScanFileObservation{life: ctx, service: f.s, job: f.job, file: f.file, librarySource: source, binding: SourceReadBinding{Purpose: SourceInventory, Observation: ObservationRef{SourceID: "remote", Generation: "1", Object: "film.mkv"}, Root: filepath.Dir(f.file), Path: f.file, Profile: profile}}
			if err = capture.check(ctx); err != nil {
				t.Fatal("initial running job", err)
			}
			writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "job":
				_, err = writer.Tx().Exec(`UPDATE jobs SET status='cancelled'`)
			case "library_root":
				_, err = writer.Tx().Exec(`UPDATE library_sources SET root_identity='replacement'`)
			case "source_root":
				_, err = writer.Tx().Exec(`UPDATE remote_sources SET root=?`, t.TempDir())
			case "generation":
				_, err = writer.Tx().Exec(`UPDATE remote_sources SET generation=2`)
			case "profile":
				// The consent column postdates this fixture; list its original columns so
				// the row keeps its stored default rather than shifting positionally.
				_, err = writer.Tx().Exec(`INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',1,'file_list_only',0,1,2,0)`)
			case "library_policy":
				_, err = writer.Tx().Exec(`UPDATE library_scan_policies SET revision=revision+1`)
			}
			if err != nil {
				writer.Rollback()
				t.Fatal(err)
			}
			if err = capture.checkTx(ctx, writer.Tx()); err == nil {
				writer.Rollback()
				t.Fatal("changed metadata authority accepted", change)
			}
			writer.Rollback()
			if err = capture.check(ctx); err == nil {
				t.Fatal("authority A-to-B-to-A revived old scan capture", change)
			}
		})
	}
}
