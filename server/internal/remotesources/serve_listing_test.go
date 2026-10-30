package remotesources

import (
	"context"
	"path/filepath"
	"testing"
)

func TestServeListingRequiresActualInventoryAndCurrentSourcePolicy(t *testing.T) {
	f := newObservationFixture(t)
	b, _, _ := f.s.find(f.file)
	ctx := context.Background()
	if _, err := f.s.db.Exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,created_at) VALUES('listing',?,'remote','1',?,0)`, f.job, b.Root); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, sql string
	}{
		{"cancelled", `UPDATE jobs SET status='cancelled' WHERE id='scan'`},
		{"unknown-job", `UPDATE remote_listing_sessions SET job_id='unknown' WHERE id='listing'`},
		{"root-incarnation", `UPDATE library_sources SET incarnation='replacement'`},
		{"root-identity", `UPDATE library_sources SET root_identity='replacement'`},
		{"disabled-library-source", `UPDATE library_sources SET enabled=0`},
		{"remote-generation", `UPDATE remote_sources SET generation=2`},
		{"remote-root", `UPDATE remote_sources SET root=root||'/replacement'`},
		{"removed", `UPDATE remote_sources SET removed=1`},
		{"policy-revision", `UPDATE library_scan_policies SET revision=revision+1`},
		{"already-ready", `UPDATE remote_listing_sessions SET ready=1`},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := f.s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = f.s.serveListingFenceTx(ctx, tx, b, "", "listing", "1"); err != nil {
				t.Fatal("actual original job was refused", err)
			}
			if _, err = tx.ExecContext(ctx, test.sql); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.serveListingFenceTx(ctx, tx, b, "", "listing", "1"); err == nil {
				t.Fatal("retired or unknown inventory admitted")
			}
		})
	}
	// Verification's opaque listing job resolves only through the actual stored
	// original inventory. It cannot acquire authority as an independent job.
	if _, err := f.s.db.Exec(`INSERT INTO remote_inventory_checks(job_id,directory,listing_id,check_job,verified_ms) VALUES(?,?,'listing','verification',0)`, f.job, b.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`UPDATE remote_listing_sessions SET job_id='verification' WHERE id='listing'`); err != nil {
		t.Fatal(err)
	}
	tx, err := f.s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	capture, err := f.s.serveListingFenceTx(ctx, tx, b, "", "listing", "1")
	if err != nil || capture.job != f.job {
		t.Fatal("verification lost its original inventory", capture, err)
	}
	if _, err = f.s.serveListingFenceTx(ctx, tx, b, filepath.Base(f.file), "listing", "1"); err == nil {
		t.Fatal("listing scope was retargeted")
	}
	changed := capture
	changed.source.LastProgressAt = "normal-progress"
	if !changed.same(capture) {
		t.Fatal("ordinary progress retired immutable source identity")
	}
	changed.profile.Revision++
	if changed.same(capture) {
		t.Fatal("profile revision did not retire captured listing")
	}
}
