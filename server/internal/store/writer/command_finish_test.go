package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestCommandFinishClearsEveryErrorAndLifetime(t *testing.T) {
	for _, mode := range []string{"caller", "protected", "error", "nil_caller", "nil_protected", "success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			lifetime, stop := context.WithCancel(t.Context())
			defer stop()
			var cause error
			switch mode {
			case "caller":
				cancel()
			case "protected":
				stop()
			case "error":
				cause = ErrOverrun
			case "nil_caller":
				ctx = nil
			case "nil_protected":
				lifetime = nil
			}
			value := commandCompletion{output: commandOutput{inventory: InventoryResult{RunID: "private", Outcome: ReceiptOutcome{State: Applied}}, effective: EffectiveStageResult{Value: EffectiveStageBeginResult{ImageID: 9}}}, ack: AckResult{AcknowledgedThrough: 17, Evidence: &ExecutionEvidence{}}}
			out, err := finishCommand(ctx, lifetime, value, cause)
			if mode == "success" {
				if err != nil || !reflect.DeepEqual(out, value) {
					t.Fatal(out, err)
				}
			} else if err == nil || !reflect.DeepEqual(out, commandCompletion{}) {
				t.Fatal("error leaked output", out, err)
			}
		})
	}
}

type finishScopeContext struct {
	context.Context
	onErr func()
}

func (c finishScopeContext) Err() error { c.onErr(); return c.Context.Err() }

type finishReceiptConn struct {
	writerDriverConn
	hook func()
}

func (c *finishReceiptConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && (strings.Contains(q, "FROM scan_command_receipts") || strings.Contains(q, "FROM enrichment_command_receipts")) {
		return &receiptCloseRows{Rows: rows, hook: c.hook}, nil
	}
	return rows, err
}

// The injected cancellation is separate from current principal authority. The
// preterminal trigger is AFTER lookup's permission check and result projection;
// later triggers run inside COMMIT, after physical retirement, and WAL sampling.
func TestDispatcherFinishLateCancellation(t *testing.T) {
	for _, domain := range []string{"inventory", "effective"} {
		for _, method := range []string{"accept", "resolve", "ack"} {
			for _, phase := range []string{"preterminal", "commit", "physical_return", "wal_sample"} {
				t.Run(domain+"/"+method+"/"+phase, func(t *testing.T) {
					var w *Writer
					var e *executor
					var authority *governor.Authority
					var invoke func(context.Context) (commandCompletion, error)
					var setScope func(context.Context)
					var floor func() int64
					if domain == "inventory" {
						var requests map[string]ScanReceiptRequest
						w, requests, e, authority = inventoryFacadeFixture(t)
						q := requests["hit"]
						input, _ := inventoryRequestFixture()
						input.scope = inventoryScope{q.RunID, q.LibraryKey, q.LibraryID}
						input.run.lastSequence, input.run.acknowledgedSequence = 0, 0
						command, err := newInventoryAcceptCommand(q.Receipt.Identity, input)
						if err != nil {
							t.Fatal(err)
						}
						// Bind the independent saved fixture to the complete original command.
						turn, tx := inventoryLookupTurn(t, e, w.effective.registry)
						saved, err := lookupInventoryAccept(t.Context(), turn, q)
						if err != nil {
							t.Fatal(err)
						}
						if _, err = tx.rollback(nil); err != nil {
							t.Fatal(err)
						}
						q.Receipt.RequestDigest = command.digest
						digest, err := inventoryAcceptResultDigest(q.Receipt.Identity, command.digest, saved.result)
						if err != nil {
							t.Fatal(err)
						}
						if _, err = e.db.ExecContext(t.Context(), "UPDATE scan_command_receipts SET request_digest=?,result_digest=? WHERE run_id=?", command.digest[:], digest[:], q.RunID); err != nil {
							t.Fatal(err)
						}
						cap := inventoryTestDisclosure(t, w, q)
						setScope = func(c context.Context) { cap.scope.lifetime = c }
						invoke = func(ctx context.Context) (commandCompletion, error) {
							var out commandCompletion
							var err error
							switch method {
							case "accept":
								out.output.inventory, err = w.AcceptInventory(ctx, cap, command)
							case "resolve":
								out.output.inventory, err = w.ResolveScan(ctx, cap, q)
							case "ack":
								out.ack, err = w.AckScan(ctx, cap, q)
							}
							return out, err
						}
						floor = func() int64 {
							var n int64
							if err := e.db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?", q.RunID).Scan(&n); err != nil {
								t.Fatal(err)
							}
							return n
						}
					} else {
						var cap *EnrichmentDisclosure
						var command *effectiveStageBeginCommand
						w, cap, command, e, authority = effectiveFacadeFixture(t)
						seedEffectiveReceipt(t, e, command)
						setScope = func(c context.Context) { cap.scope.lifetime = c }
						invoke = func(ctx context.Context) (commandCompletion, error) {
							var out commandCompletion
							var err error
							switch method {
							case "accept":
								out.output.effective, err = w.StageEffective(ctx, cap, command)
							case "resolve":
								out.output.effective, err = w.ResolveEffective(ctx, cap, effectiveReference(command))
							case "ack":
								out.ack, err = w.AckEffective(ctx, cap, effectiveReference(command))
							}
							return out, err
						}
						floor = func() int64 {
							var n int64
							if err := e.db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM metadata_provider_operations WHERE id=?", command.request.ownerID).Scan(&n); err != nil {
								t.Fatal(err)
							}
							return n
						}
					}
					oldFloor := floor()
					if err := e.db.Close(); err != nil {
						t.Fatal(err)
					}
					connector, err := sqliteconn.Connector(w.effective.path, e.config)
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					var armed, triggered atomic.Bool
					var scopeChecks, returnedClocks atomic.Int64
					setScope(finishScopeContext{Context: t.Context(), onErr: func() {
						if phase == "preterminal" && armed.Load() && scopeChecks.Add(1) == 2 {
							triggered.Store(true)
							cancel()
						}
					}})
					fault := &faultConnector{Connector: connector, afterNativeCommit: func() {
						armed.Store(true)
						if phase == "commit" {
							triggered.Store(true)
							cancel()
						}
					}}
					if phase == "preterminal" {
						e.openPool(connector, func(conn driver.Conn) driver.Conn {
							return &finishReceiptConn{writerDriverConn: conn.(writerDriverConn), hook: func() { armed.Store(true) }}
						})
					} else {
						installFaultPool(e, fault)
					}
					primeEffectiveFixturePool(t, e)
					clock := e.now()
					e.now = func() time.Time {
						if armed.Load() && authority.Usage() == ([4]governor.Amounts{}) {
							count := returnedClocks.Add(1)
							if (phase == "physical_return" && count == 1) || (phase == "wal_sample" && count == 3) {
								triggered.Store(true)
								cancel()
							}
						}
						return clock
					}
					out, err := invoke(ctx)
					if !triggered.Load() || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, commandCompletion{}) {
						t.Fatal("late cancellation leaked result/proof/floor", triggered.Load(), out, err)
					}
					if authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
						t.Fatal("physical custody not joined")
					}
					want := oldFloor
					if method == "ack" && phase != "preterminal" {
						if domain == "inventory" {
							want = 1
						} else {
							want = 12
						}
					}
					if got := floor(); got != want {
						t.Fatal("zero response misrepresented actual native outcome", got, want)
					}
				})
			}
		}
	}
}

func TestFinishPreservesProtectedCancellationBeforeSQLCleanup(t *testing.T) {
	e, path, _ := testExecutor(t)
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, e.config)
	if err != nil {
		t.Fatal(err)
	}
	var tx *transaction
	fault := &faultConnector{Connector: connector, afterNativeCommit: func() { tx.cancel() }}
	installFaultPool(e, fault)
	tx, err = e.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	physical, err := tx.commit()
	if err != nil || physical.state != committed || !errors.Is(tx.lifetimeErr, context.Canceled) {
		t.Fatal(physical, err, tx.lifetimeErr)
	}
	out, err := finishCommand(t.Context(), t.Context(), InventoryResult{RunID: "secret"}, tx.lifetimeErr)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal(out, err)
	}
}

func TestCommandProtectedDeadlineSurvivesSQLCleanup(t *testing.T) {
	caller := t.Context()
	sqlCtx, sqlCancel := context.WithCancel(caller)
	lifetime, stop := commandDeadline(caller, time.Now().Add(time.Hour))
	defer stop()
	sqlCancel()
	if sqlCtx.Err() == nil || lifetime.Err() != nil {
		t.Fatal("SQL cleanup canceled release lifetime")
	}
	value, err := finishCommand(caller, lifetime, int64(7), nil)
	if err != nil || value != 7 {
		t.Fatal(value, err)
	}
	stop()
	value, err = finishCommand(caller, lifetime, int64(7), nil)
	if !errors.Is(err, context.Canceled) || value != 0 {
		t.Fatal("protected release failure escaped", value, err)
	}
}
