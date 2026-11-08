package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRBPublicationNativeCommitFootprintGate(t *testing.T) {
	for _, mode := range []string{"covered", "widened", "unresolved", "undeclared", "undeclared-unresolved", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			w, path, clock := testExecutor(t)
			now := func() time.Time { return *clock }
			wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			cfg := governor.Config{Now: now, MaxGrantsPerClass: 32, WAL: wal}
			for c := range cfg.Partitions {
				cfg.Partitions[c] = governor.Amounts{governor.MemoryBytes: 96 << 20, governor.WALBytes: 1 << 20}
			}
			if mode != "disabled" {
				cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}
			}
			g, a, err := governor.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := refreshWriterWAL(path, wal, now); err != nil {
				t.Fatal(err)
			}
			limit := testRecipe().limit
			r := governor.Request{Key: governor.JobKey{Domain: uint16(InventoryAccept), ID: "phase2-footprint"}, Owner: 1, Class: governor.ScanMinimum, Writer: true, MaxHold: limit.Hold, Deadline: now().Add(time.Second), Budget: governor.UnitBudget{RawRows: uint32(limit.Rows), VMOps: limit.VMOps, Pages: uint32(limit.Pages)}, Need: governor.Amounts{governor.MemoryBytes: 65536, governor.WALBytes: int64(limit.WALBytes)}, Footprint: []governor.Scope{{Library: 8}}}
			if mode == "undeclared" || mode == "undeclared-unresolved" {
				r.Footprint = nil
			}
			d := g.Try(r)
			if d.Grant == nil {
				t.Fatal(d.Refusal)
			}
			custody, err := bindGovernedTurn(InventoryAccept, limit, g, a, wal, d.Grant, r)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			if mode == "widened" {
				if err := tx.resolvePublication(governor.PublicationFootprint{{Library: 7}}); !errors.Is(err, governor.ErrPublicationFootprint) {
					t.Fatal(err)
				}
				// Ignoring resolution failure or trying to narrow it afterwards must
				// never turn this poisoned accepting transaction into a commit.
				_ = tx.resolvePublication(governor.PublicationFootprint{{Library: 8}})
			} else if mode == "covered" || mode == "undeclared" {
				actual := governor.PublicationFootprint{{Library: 8, Domain: 1}}
				if err := tx.resolvePublication(actual); err != nil {
					t.Fatal(err)
				}
				actual[0].Library = 99 // retained resolved proof owns its own slice
			}
			mutateFixture(t, tx)
			result, err := tx.commit()
			want := mode == "covered" || mode == "undeclared" || mode == "undeclared-unresolved" || mode == "disabled"
			if want {
				if err != nil || result.state != committed || readRevision(t, path) != 2 {
					t.Fatal(result, err)
				}
			} else if !errors.Is(err, governor.ErrPublicationFootprint) || result.state != rolledBack || readRevision(t, path) != 1 {
				t.Fatal("escaped effects committed", result, err)
			}
			if w.db.Stats().InUse != 0 || a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) {
				t.Fatal("failed gate leaked transaction/custody")
			}
		})
	}
}
