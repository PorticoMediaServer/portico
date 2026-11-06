package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Engine faults only; SQL and all other native policy methods keep delegating.
type searchEngineProbeConn struct {
	writerDriverConn
	read func() (string, string, error)
}

func (c *searchEngineProbeConn) SQLiteEngine() (string, string, error) { return c.read() }

func TestSearchImageEngineSamePhysicalAndReplacement(t *testing.T) {
	w, path, _ := testExecutor(t)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	var physical []*ownedPhysicalConn
	var reads int
	replacementErr := errors.New("replacement engine evidence unavailable")
	w.openPool(base, func(raw driver.Conn) driver.Conn {
		native := raw.(*ownedPhysicalConn)
		physical = append(physical, native)
		generation := len(physical)
		original := native.writerDriverConn
		native.writerDriverConn = &searchEngineProbeConn{original, func() (string, string, error) {
			reads++
			if generation == 2 {
				return "old version must not escape", "old source must not escape", replacementErr
			}
			return original.(nativeEngineGetter).SQLiteEngine()
		}}
		return native
	})
	// Physical close on every return forces the next turn to acquire a NEW
	// native connection from the SAME shared connector and policy.
	w.db.SetMaxIdleConns(0)
	for generation := 1; generation <= 3; generation++ {
		tx, err := w.begin(t.Context(), SearchImageStage, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		turn := &commandTurn{transaction: tx}
		actual, err := turn.searchImageEngine(t.Context())
		if generation == 2 {
			if !errors.Is(err, replacementErr) || actual != (searchpolicy.Engine{}) {
				t.Fatal("replacement reused previous evidence", actual, err)
			}
		} else if err != nil || actual.Version != tx.settings.Engine || actual.SourceID != searchpolicy.SQLiteSourceID {
			t.Fatal("same-physical native pair", actual, err)
		}
		if len(physical) != generation || reads != generation {
			t.Fatal("engine observation was cached", len(physical), reads)
		}
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}
		if stats := w.db.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 {
			t.Fatal("physical return incomplete", stats)
		}
	}
}

func TestSearchImageEngineLifetimePurposeAndMalformedEvidence(t *testing.T) {
	for _, turn := range []*commandTurn{nil, {}} {
		if value, err := turn.searchImageEngine(t.Context()); !errors.Is(err, ErrInvalid) || value != (searchpolicy.Engine{}) {
			t.Fatal("invalid turn supplied engine", value, err)
		}
	}
	w, path, _ := testExecutor(t)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	var physical *ownedPhysicalConn
	w.openPool(base, func(raw driver.Conn) driver.Conn {
		physical = raw.(*ownedPhysicalConn)
		return physical
	})
	for _, mode := range []string{"caller_cancel", "turn_cancel", "late_caller_cancel", "late_turn_cancel", "wrong_purpose", "missing_policy", "missing_getter", "empty", "invalid_utf8", "getter_error", "closed"} {
		t.Run(mode, func(t *testing.T) {
			purpose := SearchImageStage
			if mode == "wrong_purpose" {
				purpose = InventoryAccept
			}
			tx, err := w.begin(t.Context(), purpose, testRecipe(), &testCustody{})
			if err != nil {
				t.Fatal(err)
			}
			turn := &commandTurn{transaction: tx}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			injected := errors.New("engine read failed")
			want := ErrInvalid
			if mode == "caller_cancel" {
				cancel()
				want = context.Canceled
			} else if mode == "turn_cancel" {
				tx.cancel()
				want = context.Canceled
			} else if mode == "missing_policy" {
				tx.policyVerified = false
			} else if mode == "closed" {
				if _, err := tx.rollback(nil); err != nil {
					t.Fatal(err)
				}
				want = ErrClosed
			} else if mode != "wrong_purpose" {
				// Reach the actual physical owner without a new Raw/opening seam.
				// This test-only fault is deliberately NOT an engine attestation.
				boundary := physical
				original := boundary.writerDriverConn
				defer func() { boundary.writerDriverConn = original }()
				if mode == "missing_getter" {
					boundary.writerDriverConn = struct{ writerDriverConn }{original}
				} else {
					boundary.writerDriverConn = &searchEngineProbeConn{original, func() (string, string, error) {
						switch mode {
						case "late_caller_cancel":
							cancel()
						case "late_turn_cancel":
							tx.cancel()
						case "empty":
							return "", "", nil
						case "invalid_utf8":
							return "version", "\xff", nil
						case "getter_error":
							return "saved", "saved", injected
						}
						return original.(nativeEngineGetter).SQLiteEngine()
					}}
				}
				if mode == "late_caller_cancel" || mode == "late_turn_cancel" {
					want = context.Canceled
				} else if mode == "empty" || mode == "invalid_utf8" {
					want = sqliteconn.ErrPolicy
				} else if mode == "getter_error" {
					want = injected
				}
			}
			actual, err := turn.searchImageEngine(ctx)
			if !errors.Is(err, want) || actual != (searchpolicy.Engine{}) {
				t.Fatal("invalid engine source escaped", actual, err, want)
			}
			if !tx.done {
				_, _ = tx.rollback(nil)
			}
		})
	}
}

func TestSearchImageEnginePanicUsesFrameRetirement(t *testing.T) {
	w, path, _ := testExecutor(t)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	primary := &struct{ message string }{"engine panic"}
	w.openPool(base, func(raw driver.Conn) driver.Conn {
		physical := raw.(*ownedPhysicalConn)
		physical.writerDriverConn = &searchEngineProbeConn{physical.writerDriverConn, func() (string, string, error) { panic(primary) }}
		return physical
	})
	custody := &testCustody{}
	tx, err := w.begin(t.Context(), SearchImageStage, testRecipe(), custody)
	if err != nil {
		t.Fatal(err)
	}
	turn := &commandTurn{transaction: tx}
	func() {
		defer func() {
			if got := recover(); got != primary {
				t.Fatal("primary panic replaced", got)
			}
		}()
		_, _ = turn.searchImageEngine(t.Context())
		t.Fatal("engine panic swallowed")
	}()
	if !tx.done || custody.returns != 1 || !tx.connectionReturned {
		t.Fatal("engine panic stranded turn", tx.done, custody.returns, tx.connectionReturned)
	}
	if stats := w.db.Stats(); stats.InUse != 0 {
		t.Fatal("engine panic stranded physical connection", stats)
	}
}
