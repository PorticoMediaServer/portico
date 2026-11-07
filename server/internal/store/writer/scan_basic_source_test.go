package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func basicSourceIssuerFixture(t *testing.T) (*scanDriverFixture, *ScanRunLease, ScanRunReference) {
	t.Helper()
	f := scanIssuerRuntime(t) // ONE existing opening, real Capture/governor and frozen clock
	d := f.driver
	create, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := create.Reference()
	if err != nil {
		t.Fatal(err)
	}
	applyAndAcknowledgeScanOperation(t, create)
	if err := create.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	claim, err := d.PrepareClaim(t.Context(), created)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := claim.Reference()
	if err != nil {
		t.Fatal(err)
	}
	applyAndAcknowledgeScanOperation(t, claim)
	lease, err := claim.Lease(t.Context()) // actual post-ACK snapshot, never a historical lease echo
	if err != nil {
		t.Fatal(err)
	}
	if err := claim.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	return f, lease, claimed
}

func TestBasicSourceIssuesOnlyExactFundedMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		purpose CommandID
	}{{"stage", BasicStage}, {"seal", BasicSeal}, {"accept", BasicAccept}} {
		t.Run(tc.name, func(t *testing.T) {
			f, lease, last := basicSourceIssuerFixture(t)
			d := f.driver
			p, snapshot, source, err := prepareBasicSource(t.Context(), d, lease, last, tc.purpose)
			if err != nil {
				t.Fatal(err)
			}
			if p != d.preparation || p.mode != scanBasicPreparation || p.expected.Writer || p.expected.Key.Domain != uint16(tc.purpose) || p.expected.Key.ID != last.Scan.RunID ||
				len(p.expected.Footprint) != 1 || p.expected.Footprint[0] != (governor.Scope{Library: 7, Domain: uint16(tc.purpose)}) || p.expected.Deadline.After(time.UnixMilli(lease.expiresMS)) {
				t.Fatal("holding changed original purpose/scope/lifetime", p.expected)
			}
			if snapshot.run.lastSequence != snapshot.run.acknowledgedSequence || snapshot.run.lastSequence != last.Scan.Receipt.Identity.Sequence || snapshot.run.scope.runID != last.Scan.RunID ||
				!source.matches(scanOriginalFromNative(snapshot.run.scope)) || source.policyRevision != 7 || !lease.matches(scanOriginalFromNative(snapshot.run.scope), inventoryRun{leaseOwner: snapshot.run.leaseOwner, leaseGeneration: snapshot.run.leaseGeneration, leaseExpiresMS: snapshot.run.leaseExpiresMS}) {
				t.Fatal("not actual settled native source/run facts", snapshot, source)
			}
			if p.frame == nil || !p.frame.done || !p.frame.connectionReturned || !p.frame.retirementComplete || !p.frame.cleanupComplete {
				t.Fatal("facts released before physical return")
			}
			if outer, err := p.beginObservation(); outer != nil || !errors.Is(err, ErrClosed) {
				t.Fatal("Basic acquired inventory helper authority", outer, err)
			}
			if d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
				t.Fatal("holding refunded before consumer release")
			}
			snapshot, source = scanRunSnapshot{}, scanSourceFacts{} // consumer releases owned values before Close
			if err := p.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := d.ReleasePending(t.Context()); err != nil {
				t.Fatal(err)
			}
			if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("holding not physically retired")
			}
		})
	}
}

func TestBasicSourceRefusesUnsettledOrChangedNativeAuthority(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"floor", `UPDATE inventory_runs SET last_command_sequence=last_command_sequence+1 WHERE job_id=?`},
		{"source", `UPDATE library_sources SET enabled=0 WHERE id=(SELECT source_id FROM inventory_runs WHERE job_id=?)`},
		{"active", `DELETE FROM inventory_source_active WHERE job_id=?`},
		{"generation", `UPDATE inventory_runs SET job_lease_generation=job_lease_generation+1 WHERE job_id=?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, lease, last := basicSourceIssuerFixture(t)
			// Explicit native corruption/refusal fixture only; never a floor repair
			// or substitute for the real create/ACK/claim/ACK protocol above.
			if _, err := f.driver.opening.executor.db.ExecContext(t.Context(), tc.sql, last.Scan.RunID); err != nil {
				t.Fatal(err)
			}
			p, snapshot, source, err := prepareBasicSource(t.Context(), f.driver, lease, last, BasicStage)
			if err == nil || p != nil || !reflect.DeepEqual(snapshot, scanRunSnapshot{}) || !reflect.DeepEqual(source, scanSourceFacts{}) {
				t.Fatal("native refusal released facts", p, snapshot, source, err)
			}
			if f.driver.preparation != nil || f.driver.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("refusal retained an unissued holding")
			}
		})
	}
}

func TestBasicSourceRejectsWrongPurposeAndLateCancellation(t *testing.T) {
	f, lease, last := basicSourceIssuerFixture(t)
	d := f.driver
	if p, snapshot, source, err := prepareBasicSource(t.Context(), d, lease, last, InventoryAccept); !errors.Is(err, ErrInvalid) || p != nil || !reflect.DeepEqual(snapshot, scanRunSnapshot{}) || !reflect.DeepEqual(source, scanSourceFacts{}) {
		t.Fatal("N1 purpose borrowed Basic custody", p, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e := d.opening.executor
	connector, err := sqliteconn.Connector(d.opening.binding.path, e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(e, &faultConnector{Connector: connector, afterNativeCommit: cancel})
	p, snapshot, source, err := prepareBasicSource(ctx, d, lease, last, BasicStage)
	if !errors.Is(err, context.Canceled) || p != nil || !reflect.DeepEqual(snapshot, scanRunSnapshot{}) || !reflect.DeepEqual(source, scanSourceFacts{}) {
		t.Fatal("late cancellation released Basic facts", p, snapshot, source, err)
	}
	if d.preparation == nil || !d.preparation.failed || d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("discarded output lost physical holding")
	}
	if err := d.ReleasePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("late canceled holding did not retire")
	}
}
