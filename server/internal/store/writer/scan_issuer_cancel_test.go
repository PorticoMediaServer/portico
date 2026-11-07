package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestScanIssuerLateCommitCancellationKeepsOnlyOriginalRecovery(t *testing.T) {
	f := scanIssuerRuntime(t)
	d := f.driver
	operation, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := operation.Reference()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e := d.opening.executor
	connector, err := sqliteconn.Connector(d.opening.binding.path, e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(e, &faultConnector{Connector: connector, afterNativeCommit: cancel})
	result, err := operation.Apply(ctx)
	if !errors.Is(err, context.Canceled) || result != nil || !reflect.DeepEqual(operation.last, scanLifecycleCommandResult{}) {
		t.Fatal("late canceled COMMIT published a result", result, operation.last, err)
	}
	var saved int64
	if err = e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id=?`, original.Scan.RunID).Scan(&saved); err != nil || saved != 1 {
		t.Fatal("known physical COMMIT was invented as rollback", saved, err)
	}
	if d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("canceled caller refunded retained original")
	}
	if err = d.ReleasePending(t.Context()); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("submitted work was treated as unsubmitted preparation", err)
	}
	if err = operation.Close(t.Context()); err != nil {
		t.Fatal("joined canceled original release", err)
	}
	recovered, err := d.OpenRun(t.Context(), original)
	if err != nil {
		t.Fatal("same original recovery", err)
	}
	result, err = recovered.Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	value, err := result.Snapshot(t.Context())
	if err != nil || value.Outcome.State != Applied || !value.Outcome.Replayed || value.Outcome.Reference != original.Scan.Receipt {
		t.Fatal("did not recover exact physical outcome", value, err)
	}
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = recovered.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = recovered.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("recovered original memory remained charged after close")
	}
}
