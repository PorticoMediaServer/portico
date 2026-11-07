package writer

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestN1OperatingForecastCompleteReservations(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ordinary-file forecast")
	}
	d, _ := n1ComposedDriverFixture(t, n1OperatingForecastFixture)
	p := d.writer.effective.inventoryDriverPlan
	if p == nil || p.need[governor.MemoryBytes] != 16<<20 || p.inventoryMemory.preparationBytes != 8<<20 || p.inventoryMemory.reservedBytes != 8<<20 {
		t.Fatal("forecast partition not installed under same frame", p)
	}
	reads, writes := 0, 0
	for _, def := range inventoryDriverCommandDefinitions() {
		n := inventoryDriverMaxUses(def.ID)
		if def.Kind == stmt.Read {
			reads += n
		} else if def.Kind == stmt.Write {
			writes += n
		} else {
			t.Fatal("foreign effect kind")
		}
	}
	if reads != 43 || writes != 26 || len(p.steps) != 64 {
		t.Fatal("closed forecast inventory changed", reads, writes, len(p.steps))
	}
	remaining := subtract(subtract(p.recipe.limit, p.recipe.setup), p.recipe.terminal)
	left, err := reserveInventoryDriverSteps(remaining, p.steps)
	if err != nil || left.Hold <= 0 || left.Rows == 0 || left.VMOps == 0 || left.Pages == 0 || left.WALBytes == 0 {
		t.Fatal("complete repeated uses borrow terminal or another dimension", left, err)
	}
	// The native storage component must fit EVERY preserved-record rewrite,
	// including source progress; no empty spare step lends it an allowance.
	if err := validateInventoryRecordEnvelope(*p); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveInventoryDriverSteps(Work{}, p.steps); !errors.Is(err, ErrQuantum) {
		t.Fatal("empty unit funded a prefix", err)
	}
}

func TestN1OperatingForecastRunsOriginalPrefix(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ordinary-file forecast")
	}
	d, preparation, command, original := n1ComposedDriverPrepared(t, n1OperatingForecastFixture)
	got, err := preparation.Accept(t.Context(), command)
	if err != nil || got.Outcome.State != Applied || got.Outcome.Reference != original.Receipt || len(got.Members) != 1 {
		t.Fatal("operating forecast did not execute original", got, err)
	}
	if err := preparation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := d.ResolveScan(t.Context(), original)
	if err != nil || saved.Outcome.State != Applied || !saved.Outcome.Replayed || saved.Outcome.Reference != original.Receipt || !reflect.DeepEqual(saved.Members, got.Members) {
		t.Fatal("original history changed", saved, err)
	}
	ack, err := d.AckScan(t.Context(), original)
	if err != nil || ack.AcknowledgedThrough != original.Receipt.Identity.Sequence {
		t.Fatal("consumer ACK lost actual floor", ack, err)
	}
	retired, err := d.ResolveScan(t.Context(), original)
	if err != nil || retired.Outcome.State != ResultRetired || len(retired.Members) != 0 {
		t.Fatal("ACK floor resurrected saved payload", retired, err)
	}
}

func TestN1OperatingForecastCapacityRefusesBeforeEffects(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ordinary-file forecast")
	}
	d, preparation, command, original := n1ComposedDriverPrepared(t, n1OperatingForecastFixture)
	db := d.opening.executor.db
	// A legal but unusually large stored label is not a new product prohibition.
	// This operating envelope must safely refuse it before native effects.
	if _, err := db.ExecContext(t.Context(), "UPDATE library_sources SET name=? WHERE id='lifecycle-new-source'", strings.Repeat("x", 128<<10)); err != nil {
		t.Fatal(err)
	}
	got, err := preparation.Accept(t.Context(), command)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(got, InventoryResult{}) {
		t.Fatal("capacity miss released partial success", got, err)
	}
	var assets, objects, receipts, last int64
	for q, dst := range map[string]*int64{
		"SELECT count(*) FROM catalog_assets":                                              &assets,
		"SELECT count(*) FROM inventory_objects":                                           &objects,
		"SELECT count(*) FROM scan_command_receipts WHERE command_kind='inventory_accept'": &receipts,
		"SELECT last_command_sequence FROM inventory_runs":                                 &last,
	} {
		if err := db.QueryRowContext(t.Context(), q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if assets != 0 || objects != 0 || receipts != 0 || last != original.Receipt.Identity.Sequence-1 {
		t.Fatal("capacity refusal changed durable effects", assets, objects, receipts, last)
	}
	if err := preparation.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
