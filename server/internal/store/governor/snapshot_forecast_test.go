package governor

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func forecastSnapshot(t *testing.T) (*Snapshots, *sql.Conn, *Authority, Request) {
	t.Helper()
	path, writer := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	defs := []stmt.Definition{}
	for id, query := range map[stmt.ID]string{
		"lengths":       "SELECT octet_length(value) FROM source WHERE id=?",
		"payload":       "SELECT value FROM source WHERE id=?",
		"wrong_lengths": "SELECT 4 FROM source WHERE id=?",
	} {
		defs = append(defs, stmt.Definition{ID: id, SQL: query, Owner: "forecast-test", Kind: stmt.Read, CallSites: []string{"snapshot_forecast_test"}, Params: []stmt.Param{{Name: "id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{int64(1)}}}})
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 16384, Columns: 16}}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSnapshots(connector, cfg, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := snapshotRequest()
	r.Need[MemoryBytes] = (64 << 10) + 5
	return s, writer, a, r
}

func TestSnapshotForecastExactPointAndPhysicalCustody(t *testing.T) {
	s, writer, a, r := forecastSnapshot(t)
	out, err := s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{5, 2}, func(scope *ReadScope) error {
		row, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1))
		if err != nil {
			return err
		}
		if len(row) != 1 || row[0] != "first" || a.Usage()[Foreground][MemoryBytes] != r.Need[MemoryBytes] {
			t.Fatal("wrong point or early refund", row)
		}
		if _, err = scope.Query("lengths", int64(1)); !errors.Is(err, ErrBusy) {
			t.Fatal("finite query recipe ignored", err)
		}
		return nil
	})
	if err != nil || out.Refusal != nil || out.NativeReservedBytes != 5 || out.GoReservedBytes != 64<<10 || a.Usage()[Foreground] != (Amounts{}) {
		t.Fatal("actual-byte partition/retirement", out, err)
	}
	// Exact native UTF8+BLOB bytes include the embedded NUL; character count and
	// NUL-terminated length must never be substituted for the native byte point.
	if _, err = writer.ExecContext(t.Context(), "UPDATE source SET value=? WHERE id=1", "é\x00x"); err != nil {
		t.Fatal(err)
	}
	r.Deadline = time.Now().Add(time.Second)
	out, err = s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{4, 2}, func(scope *ReadScope) error {
		row, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1))
		if err == nil && row[0] != "é\x00x" {
			t.Fatal("native bytes changed")
		}
		return err
	})
	if err != nil || out.Refusal != nil || out.NativeReservedBytes != 4 {
		t.Fatal(out, err)
	}
}

func TestSnapshotForecastPreflightAndCopyMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bytes  int64
		length stmt.ID
		calls  int
	}{
		{"before_payload", 4, "lengths", 1},
		{"copy_mismatch", 5, "wrong_lengths", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, a, r := forecastSnapshot(t)
			out, err := s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{tc.bytes, 2}, func(scope *ReadScope) error {
				row, err := scope.QueryExactBytes(tc.length, "payload", []int{0}, int64(1))
				if !errors.Is(err, ErrSnapshotBudget) || row != nil || scope.calls != tc.calls {
					t.Fatal("bad size passed or payload copied early", scope.calls, err)
				}
				return err
			})
			if !errors.Is(err, ErrSnapshotBudget) || out.Refusal != nil || a.Usage()[Foreground] != (Amounts{}) {
				t.Fatal("bad point leaked holding/output", out, err)
			}
		})
	}
}

func TestSnapshotForecastSameSnapshotAndFiniteCalls(t *testing.T) {
	s, writer, a, r := forecastSnapshot(t)
	out, err := s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{5, 9}, func(scope *ReadScope) error {
		lengths, err := scope.Query("lengths", int64(1))
		if err != nil {
			return err
		}
		if _, err = writer.ExecContext(t.Context(), "UPDATE source SET value='a much longer current value' WHERE id=1"); err != nil {
			return err
		}
		// The old protected snapshot, not a later writer commit, supplies BOTH sizes
		// and copy. No current-row recapture is mixed into the prepared proof.
		row, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1))
		if err != nil {
			return err
		}
		if lengths[0][0] != int64(5) || row[0] != "first" {
			t.Fatal("snapshot changed between byte point and copy")
		}
		for scope.calls < 9 {
			if _, err = scope.Query("lengths", int64(1)); err != nil {
				return err
			}
		}
		if _, err = scope.Query("lengths", int64(1)); !errors.Is(err, ErrBusy) {
			t.Fatal("query profile was unbounded", err)
		}
		return nil
	})
	if err != nil || out.Refusal != nil || a.Usage()[Foreground] != (Amounts{}) {
		t.Fatal(out, err)
	}
}

func TestSnapshotForecastRejectsUnfundedOrInvalidProfiles(t *testing.T) {
	s, _, a, r := forecastSnapshot(t)
	called := false
	read := func(*ReadScope) error { called = true; return nil }
	for _, f := range []SnapshotForecast{{0, 2}, {-1, 2}, {s.NativeRowReservation() + 1, 2}, {5, 0}, {5, 33}} {
		if _, err := s.ReadForecasted(t.Context(), r, time.Second, f, read); !errors.Is(err, ErrSnapshotBudget) {
			t.Fatal("invalid forecast", f, err)
		}
	}
	r.Need[MemoryBytes] = 5
	if _, err := s.ReadForecasted(context.Background(), r, time.Second, SnapshotForecast{5, 2}, read); !errors.Is(err, ErrSnapshotBudget) {
		t.Fatal("unfunded copy space", err)
	}
	if called || a.Usage()[Foreground] != (Amounts{}) {
		t.Fatal("bad profile reached native/consumer or retained capacity")
	}
}
