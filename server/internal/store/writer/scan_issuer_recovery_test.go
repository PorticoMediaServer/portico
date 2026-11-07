package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestScanIssuerRestartKeepsOriginalHistoryWithoutLeaseAuthority(t *testing.T) {
	f := scanIssuerRuntime(t)
	d := f.driver
	create, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	createRef, err := create.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err := create.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = create.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = create.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	claim, err := d.PrepareClaim(t.Context(), createRef)
	if err != nil {
		t.Fatal(err)
	}
	original, err := claim.Reference()
	if err != nil {
		t.Fatal(err)
	}
	result, err = claim.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := result.Snapshot(t.Context())
	if err != nil || saved.Outcome.State != Applied || saved.LeaseGeneration != 1 {
		t.Fatal(saved, err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	// Stop BEFORE ACK. The saved immutable receipt, not a reconstructed current
	// head, must settle this original after all old physical ownership is joined.
	path := d.opening.binding.path
	if err = d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = d.opening.close(); err != nil {
		t.Fatal(err)
	}
	if err = d.owner.Close(); err != nil {
		t.Fatal(err)
	}
	if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("old epoch retained a holding after physical close")
	}
	owner := scanOpeningOwner(t, path)
	opening := openScanFixture(t, path, owner)
	reopened := scanDriverFixtureOnOpening(t, opening, owner, f.create, []scanDriverStatementFixture{scanIssuerRuntimeFixture}, false)
	_, plan, ready := scanIssuerRuntimePlans(t, reopened)
	next := reopened.driver
	if err = configureScanIssuer(t.Context(), next, plan, plan, ready, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = opening.executor.db.ExecContext(t.Context(), `UPDATE library_sources SET enabled=0,root='/rebound',generation=generation+1 WHERE id='lifecycle-new-source'`); err != nil {
		t.Fatal(err)
	}
	if _, err = opening.executor.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET retired=1 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	recovered, err := next.OpenRun(t.Context(), original)
	if err != nil {
		t.Fatal("original reopen", err)
	}
	if _, err = recovered.Apply(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("history acquired fresh authority", err)
	}
	result, err = recovered.Resolve(t.Context())
	if err != nil {
		t.Fatal("original Resolve", err)
	}
	actual, err := result.Snapshot(t.Context())
	if err != nil || actual.Outcome.State != Applied || !actual.Outcome.Replayed || actual.AcceptedRunRevision != saved.AcceptedRunRevision || actual.AppliedMS != saved.AppliedMS || actual.Digest != saved.Digest || actual.LeaseOwner != saved.LeaseOwner || actual.LeaseGeneration != saved.LeaseGeneration || actual.RootRevision != saved.RootRevision {
		t.Fatal("restart replaced saved fields", actual, saved, err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = recovered.Ack(t.Context()); err != nil {
		t.Fatal("restart ACK", err)
	}
	if lease, err := recovered.Lease(t.Context()); err == nil || lease != nil {
		t.Fatal("saved lease became current after restart", lease, err)
	}
	if err = recovered.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	retired, err := next.OpenRun(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	result, err = retired.Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err = result.Snapshot(t.Context())
	if err != nil || actual.Outcome.State != ResultRetired || actual.Digest != (ResultDigest{}) || actual.RunID != "" || actual.LeaseOwner != 0 {
		t.Fatal("floor resurrected saved values", actual, err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if page, err := retired.preparation.ObserveInventory(t.Context()); err == nil || page.Version != 0 {
		t.Fatal("history fabricated a native observation", page, err)
	}
	if err = retired.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if next.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("recovery reservation survived consumer and native return")
	}
}
