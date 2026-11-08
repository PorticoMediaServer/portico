package writer

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The entity key is nullable because RB models a real unknown resolution
// cohort. The settle materializes the probe value on every Accept, so only the
// pre-state can be NULL; NULL->NULL and value->NULL are not reachable through
// this protocol and the projection asserts it always produces a value.
func TestProjectBasicFirstLeafNullResolutionTransitions(t *testing.T) {
	base := basicLeafTechnicalBefore{
		duration:           sql.NullFloat64{Float64: 90, Valid: true},
		resolution:         sql.NullInt64{Int64: 720, Valid: true},
		durationKey:        90,
		resolutionKey:      sql.NullInt64{Int64: 720, Valid: true},
		scalarRevision:     17,
		aggregateRevision:  23,
		membershipSequence: 9,
	}
	unknown := base
	unknown.resolution = sql.NullInt64{}
	unknown.resolutionKey = sql.NullInt64{}
	for _, tc := range []struct {
		name          string
		before        basicLeafTechnicalBefore
		duration      float64
		height        int64
		wantKey       int64
		wantAggregate bool
		wantErr       error
	}{
		{"null_to_value", unknown, 100, 1080, 1080, true, nil},
		{"value_to_value", base, 100, 1080, 1080, true, nil},
		{"value_unchanged", base, 90, 720, 720, false, nil},
		{"zero_default_scalar_absent", func() basicLeafTechnicalBefore {
			b := base
			b.resolution = sql.NullInt64{}
			b.resolutionKey = sql.NullInt64{Int64: 0, Valid: true}
			return b
		}(), 100, 1080, 1080, true, nil},
		{"nonzero_key_scalar_absent", func() basicLeafTechnicalBefore {
			b := base
			b.resolution = sql.NullInt64{}
			return b
		}(), 100, 1080, 0, false, ErrConflict},
		{"null_key_with_scalar_value", func() basicLeafTechnicalBefore {
			b := base
			b.resolutionKey = sql.NullInt64{}
			return b
		}(), 100, 1080, 0, false, ErrConflict},
		{"scalar_disagrees_with_key", func() basicLeafTechnicalBefore {
			b := base
			b.resolutionKey = sql.NullInt64{Int64: 1080, Valid: true}
			return b
		}(), 100, 1080, 0, false, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, err := projectBasicFirstLeafTechnical(tc.before, basicFirstLinkTechnical{}, tc.duration, tc.height)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || next != (basicLeafTechnicalAfter{}) {
					t.Fatal(next, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// NULL->NULL and value->NULL are unreachable: the settle always
			// materializes the probe value, exactly like the scalar CAS.
			if !next.resolutionKey.Valid || next.resolutionKey.Int64 != tc.wantKey || !next.resolution.Valid || next.resolution.Int64 != tc.wantKey {
				t.Fatal("settle did not materialize a resolution", next)
			}
			if next.durationKey != next.duration.Float64 || next.duration.Float64 <= 0 || math.IsNaN(next.duration.Float64) {
				t.Fatal(next)
			}
			if next.aggregateChanged != tc.wantAggregate || next.aggregateRevision != tc.before.aggregateRevision+boolInt(tc.wantAggregate) {
				t.Fatal("aggregate accounting", next)
			}
		})
	}
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func TestSettleBasicFirstLeafMatchesNullEntityKey(t *testing.T) {
	w, command := basicFirstAcceptCommandFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET resolution_key=NULL WHERE id=31`); err != nil {
		t.Fatal(err)
	}
	defs := basicFirstAcceptTestDefinitions()
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_accept_handler_test.go", "basic_accept_command.go", "basic_accept_handler.go", "basic_accept_control.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	value, err := applyBasicAccept(t.Context(), turn, command)
	if err != nil || value.ResultCount != 1 || len(value.Members) != 1 {
		t.Fatal(value, err)
	}
	basicHandlerCommit(t, turn, BasicPurposeAccept, command.request.original.run.leaseExpiresMS)
	var got sql.NullInt64
	if err = w.db.QueryRowContext(t.Context(), `SELECT resolution_key FROM catalog_entities WHERE id=31`).Scan(&got); err != nil || !got.Valid || got.Int64 != 1080 {
		t.Fatal(got, err)
	}
}

func TestSettleBasicFirstLeafRefusesMixedNullResolution(t *testing.T) {
	w, command := basicFirstAcceptCommandFixture(t)
	for _, q := range []string{
		`UPDATE catalog_entities SET resolution_key=NULL WHERE id=31`,
		`UPDATE catalog_scalar_contributions SET resolution=1080 WHERE parent_id=31`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	defs := basicFirstAcceptTestDefinitions()
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_accept_handler_test.go", "basic_accept_command.go", "basic_accept_handler.go", "basic_accept_control.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	value, err := applyBasicAccept(t.Context(), turn, command)
	if !errors.Is(err, ErrConflict) || value.Outcome != (ReceiptOutcome{}) || value.ResultCount != 0 || len(value.Members) != 0 || value.Digest != (ResultDigest{}) {
		t.Fatal(value, err)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var resolution sql.NullInt64
	var epoch int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT resolution FROM catalog_scalar_contributions WHERE parent_id=31),(SELECT info_epoch FROM catalog_assets WHERE id=41)`).Scan(&resolution, &epoch); err != nil {
		t.Fatal(err)
	}
	if !resolution.Valid || resolution.Int64 != 1080 || epoch != 17 {
		t.Fatal("mixed null state changed native rows", resolution, epoch)
	}
}

// The NULL-key variant must stay a primary-key point read: the extra IS NULL
// term changes the CAS predicate, not the access path.
func TestSettleNullEntityCASPlanSeeksPrimaryKey(t *testing.T) {
	w, _ := basicFirstAcceptCommandFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var defs []stmt.Definition
	for _, d := range basicSettleLeafDefinitions() {
		if d.ID == basicSettleEntityNullCAS || d.ID == basicSettleEntityCAS {
			defs = append(defs, d)
		}
	}
	if len(defs) != 2 {
		t.Fatal("missing entity CAS variants", len(defs))
	}
	golden := captureEffectiveFixture(t, conn, w.config, defs, "basic_settle_leaf_statements.go")
	if len(golden.Plans) != 2 {
		t.Fatal("incomplete capture", len(golden.Plans))
	}
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 {
			t.Fatal("missing EQP", p.ID)
		}
		var detail string
		for _, row := range p.QueryPlan.Values {
			for _, v := range row {
				detail += " " + fmt.Sprint(v)
			}
		}
		if !strings.Contains(detail, "SEARCH catalog_entities USING INTEGER PRIMARY KEY (rowid=?)") {
			t.Fatalf("%s is not a primary-key point read: %s", p.ID, detail)
		}
	}
}
