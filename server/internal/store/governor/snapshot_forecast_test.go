package governor

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func forecastSnapshot(t *testing.T, limits ...sqliteconn.ReadLimits) (*Snapshots, *sql.Conn, *Authority, Request) {
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
	if len(limits) > 0 {
		cfg.ReadLimits = limits[0]
	}
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
	r.Need[MemoryBytes] = s.NativeRowReservation() + (64 << 10)
	return s, writer, a, r
}

func TestSnapshotForecastExactPointAndPhysicalCustody(t *testing.T) {
	s, writer, a, r := forecastSnapshot(t)
	out, err := s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{NativeRowBytes: 5, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *ReadScope) error {
		row, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1))
		if err != nil {
			return err
		}
		if len(row) != 1 || row[0] != "first" || a.Usage()[Foreground][MemoryBytes] != 5+s.nativeRowDecodeOverhead()+(64<<10) {
			t.Fatal("wrong point or early refund", row)
		}
		if _, err = scope.Query("lengths", int64(1)); !errors.Is(err, ErrBusy) {
			t.Fatal("finite query recipe ignored", err)
		}
		return nil
	})
	if err != nil || out.Refusal != nil || out.NativeReservedBytes != 5+s.nativeRowDecodeOverhead() || out.GoReservedBytes != 64<<10 || a.Usage()[Foreground] != (Amounts{}) {
		t.Fatal("actual-byte partition/retirement", out, err)
	}
	// Exact native UTF8+BLOB bytes include the embedded NUL; character count and
	// NUL-terminated length must never be substituted for the native byte point.
	if _, err = writer.ExecContext(t.Context(), "UPDATE source SET value=? WHERE id=1", "é\x00x"); err != nil {
		t.Fatal(err)
	}
	r.Deadline = time.Now().Add(time.Second)
	out, err = s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{NativeRowBytes: 4, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *ReadScope) error {
		row, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1))
		if err == nil && row[0] != "é\x00x" {
			t.Fatal("native bytes changed")
		}
		return err
	})
	if err != nil || out.Refusal != nil || out.NativeReservedBytes != 4+s.nativeRowDecodeOverhead() {
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
		{"copy_over_forecast", 4, "wrong_lengths", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, a, r := forecastSnapshot(t)
			out, err := s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{NativeRowBytes: tc.bytes, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *ReadScope) error {
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
	out, err := s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{NativeRowBytes: 5, MaxQueries: 9, GoCopyBytes: 64 << 10}, func(scope *ReadScope) error {
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
	for _, f := range []SnapshotForecast{
		{NativeRowBytes: 0, MaxQueries: 2},
		{NativeRowBytes: -1, MaxQueries: 2, GoCopyBytes: 64 << 10},
		{NativeRowBytes: s.NativeRowReservation() + 1, MaxQueries: 2, GoCopyBytes: 64 << 10},
		{NativeRowBytes: 5, MaxQueries: 0, GoCopyBytes: 64 << 10},
		{NativeRowBytes: 5, MaxQueries: 33, GoCopyBytes: 64 << 10},
		{NativeRowBytes: 5, MaxQueries: 2}, // no inferred Go allowance
		{NativeRowBytes: 5, MaxQueries: 2, GoCopyBytes: -1},
	} {
		if _, err := s.ReadForecasted(t.Context(), r, time.Second, f, read); !errors.Is(err, ErrSnapshotBudget) {
			t.Fatal("invalid forecast", f, err)
		}
	}
	r.Need[MemoryBytes] = 5
	if _, err := s.ReadForecasted(context.Background(), r, time.Second, SnapshotForecast{NativeRowBytes: 5, MaxQueries: 2, GoCopyBytes: 64 << 10}, read); !errors.Is(err, ErrSnapshotBudget) {
		t.Fatal("unfunded copy space", err)
	}
	if called || a.Usage()[Foreground] != (Amounts{}) {
		t.Fatal("bad profile reached native/consumer or retained capacity")
	}
}

func TestSnapshotForecastAdmitsOnlyExplicitBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _, a, r := forecastSnapshot(t, sqliteconn.ReadLimits{Columns: 128, LengthBytes: 65536})
		if s.NativeRowReservation() != 8<<20 {
			t.Fatal("control lost actual-reader hard ceiling")
		}
		// The request is a ceiling larger than the whole partition, not a hold.
		// Even the old native floor alone could not fit this 4 MiB partition.
		r.Need[MemoryBytes] = 12 << 20
		const goBytes = 64 << 10
		const overhead = 64 + 64*128
		const want = 5 + overhead + goBytes
		called := false
		out, err := s.ReadForecasted(t.Context(), r, time.Second,
			SnapshotForecast{NativeRowBytes: 5, MaxQueries: 2, GoCopyBytes: goBytes}, func(scope *ReadScope) error {
				called = true
				if got := a.Usage()[Foreground]; got[MemoryBytes] != want || got[Reader] != 1 || scope.expected.Need[MemoryBytes] != want {
					t.Fatal("forecast was only relabeled, not admitted", got, scope.expected.Need)
				}
				row, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1))
				if err == nil && (len(row) != 1 || row[0] != "first") {
					t.Fatal("payload changed", row)
				}
				if a.Usage()[Foreground][MemoryBytes] != want {
					t.Fatal("preflight/copy refunded consumer custody")
				}
				return err
			})
		if err != nil || out.Refusal != nil || !called || out.NativeReservedBytes != 5+overhead || out.GoReservedBytes != goBytes || a.Usage()[Foreground] != (Amounts{}) {
			t.Fatal("exact reservation/retirement", out, called, err)
		}
	})
}

func TestSnapshotForecastCeilingAndZeroOctets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, writer, a, r := forecastSnapshot(t)
		f := SnapshotForecast{NativeRowBytes: 5, MaxQueries: 2, GoCopyBytes: 64 << 10}
		called := false
		read := func(*ReadScope) error { called = true; return nil }
		r.Need[MemoryBytes] = 5 + (64 + 64*16) + (64 << 10) - 1
		if _, err := s.ReadForecasted(t.Context(), r, time.Second, f, read); !errors.Is(err, ErrSnapshotBudget) || called || a.Usage()[Foreground] != (Amounts{}) {
			t.Fatal("forecast exceeded caller ceiling", called, err)
		}
		r.Need[MemoryBytes] = math.MaxInt64
		f.GoCopyBytes = math.MaxInt64
		if _, err := s.ReadForecasted(t.Context(), r, time.Second, f, read); !errors.Is(err, ErrSnapshotBudget) || called {
			t.Fatal("forecast sum overflow accepted", called, err)
		}
		if _, err := writer.ExecContext(t.Context(), "UPDATE source SET value=NULL WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		r.Need[MemoryBytes] = 64 << 10
		f.NativeRowBytes, f.GoCopyBytes = 0, 32<<10
		out, err := s.ReadForecasted(t.Context(), r, time.Second, f, func(scope *ReadScope) error {
			row, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1))
			if err == nil && (len(row) != 1 || row[0] != nil) {
				t.Fatal("NULL point changed", row)
			}
			return err
		})
		if err != nil || out.Refusal != nil || out.NativeReservedBytes != 64+64*16 || out.GoReservedBytes != 32<<10 || a.Usage()[Foreground] != (Amounts{}) {
			t.Fatal("zero-octet forecast", out, err)
		}
	})
}

func TestSnapshotForecastConservativeCompatibility(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _, a, r := forecastSnapshot(t)
		const goBytes = 64 << 10
		r.Need[MemoryBytes] = s.NativeRowReservation() + goBytes
		// Both the default and the existing full-ceiling forecast keep their
		// original exact partition and full request hold; no overhead is added.
		for _, legacyForecast := range []bool{false, true} {
			called := false
			read := func(scope *ReadScope) error {
				called = true
				if a.Usage()[Foreground][MemoryBytes] != r.Need[MemoryBytes] {
					t.Fatal("conservative hold changed")
				}
				_, err := scope.Query("payload", int64(1))
				return err
			}
			var out SnapshotResult
			var err error
			if legacyForecast {
				out, err = s.ReadForecasted(t.Context(), r, time.Second, SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: 2}, read)
			} else {
				out, err = s.Read(t.Context(), r, time.Second, read)
			}
			if err != nil || out.Refusal != nil || !called || out.NativeReservedBytes != s.NativeRowReservation() || out.GoReservedBytes != goBytes || a.Usage()[Foreground] != (Amounts{}) {
				t.Fatal("conservative default/legacy changed", legacyForecast, out, err)
			}
		}
	})
}

func TestSnapshotForecastInstallsCopyBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _, a, r := forecastSnapshot(t)
		// Model a broken recipe that skipped/misreported its byte point. Even
		// without QueryExactBytes' later comparison, the driver must refuse
		// the stored five-byte row against this four-byte forecast.
		out, err := s.ReadForecasted(t.Context(), r, time.Second,
			SnapshotForecast{NativeRowBytes: 4, MaxQueries: 1, GoCopyBytes: 64 << 10}, func(scope *ReadScope) error {
				rows, err := scope.Query("payload", int64(1))
				if !errors.Is(err, ErrSnapshotBudget) || rows != nil {
					t.Fatal("driver did not enforce snapshot forecast", rows, err)
				}
				return err
			})
		if !errors.Is(err, ErrSnapshotBudget) || out.Refusal != nil || a.Usage()[Foreground] != (Amounts{}) {
			t.Fatal("oversize copy leaked admission", out, err)
		}
	})
}

func TestSnapshotForecastConsumerKeepsExactHoldingAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _, a, r := forecastSnapshot(t)
		const want = 5 + (64 + 64*16) + (64 << 10)
		entered, release := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := s.ReadForecasted(t.Context(), r, time.Second,
				SnapshotForecast{NativeRowBytes: 5, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *ReadScope) error {
					if _, err := scope.QueryExactBytes("lengths", "payload", []int{0}, int64(1)); err != nil {
						return err
					}
					close(entered)
					<-release
					return nil
				})
			done <- err
		}()
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		select {
		case <-entered:
		case err := <-done:
			t.Fatal("forecast consumer never entered", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if got := a.Usage()[Foreground]; got[MemoryBytes] != want || got[Reader] != 1 {
			t.Fatal("physical close refunded a retained consumer", got)
		}
		close(release)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal("close cancellation lost", err)
		}
		if a.Usage()[Foreground] != (Amounts{}) {
			t.Fatal("consumer return did not retire exact holding")
		}
	})
}
