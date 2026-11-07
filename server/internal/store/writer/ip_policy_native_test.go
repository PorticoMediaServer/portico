package writer

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

var ipPolicyTestInc = [16]byte{1, 2, 3}
var ipPolicyTestOwner = [16]byte{4, 5, 6}

func ipPolicyTestExec(t *testing.T, w *executor, q string, args ...any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(q, err)
	}
}
func ipPolicyTestScalar(t *testing.T, w *executor, q string) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func ipPolicyFixture(t *testing.T) *executor {
	t.Helper()
	w := ipDraftExecutor(t)
	for _, q := range []string{
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'ip-test','/fixture')`,
		`INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','offline',1)`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,3,1)`,
	} {
		ipPolicyTestExec(t, w, q)
	}
	ipPolicyTestExec(t, w, `INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,command_incarnation) VALUES(7,'tmdb',?,5,1,1,'US','native-v1','fill_missing',?)`, ipPolicyTestInc[:], ipPolicyTestOwner[:])
	return w
}
func ipPolicyTestDefinitions() []stmt.Definition {
	return append(ipPolicyDefinitions(), ipRefreshDefinitions()...)
}
func ipPolicyTestTurn(t *testing.T, w *executor, id CommandID, definitions ...[]stmt.Definition) *commandTurn {
	t.Helper()
	defs := ipPolicyTestDefinitions()
	if len(definitions) > 0 {
		defs = definitions[0]
	}
	proof, err := stmttest.Receipt(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, proof)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{Work{10000, 2000000, 10000, 64 << 20, 25 * time.Millisecond}, Work{16, 500, 16, 65536, time.Millisecond}, Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := w.begin(t.Context(), id, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{32, 4000, 32, 65536, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func ipPolicyTestCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func ipPolicyTestIdentity(seq int64) CommandIdentity {
	return CommandIdentity{ipPolicyTestOwner, seq, [16]byte{byte(seq), 9}}
}
func ipPolicyTestRequest() ipPolicyRequest {
	return ipPolicyRequest{library: 7, provider: "tmdb", identity: ipPolicyTestIdentity(1), policyIncarnation: ipPolicyTestInc, revision: 5, consentRevision: 3, nowMS: 100000, values: ipPolicyValues{1, 2, 1, "CA", "native-v2", "replace_unlocked"}}
}
func ipPolicyTestDecode() *ipPolicyDecode { return &ipPolicyDecode{remaining: 64 << 10} }

func TestIPPolicyNative(t *testing.T) {
	for _, name := range []string{"fresh", "no_op", "replay_before_fresh_CAS", "conflict", "predecode_bytes", "overflow", "receipt_rollback", "cancel_zero"} {
		t.Run(name, func(t *testing.T) {
			w := ipPolicyFixture(t)
			r := ipPolicyTestRequest()
			switch name {
			case "no_op":
				r.values = ipPolicyValues{1, 1, 1, "US", "native-v1", "fill_missing"}
			case "predecode_bytes":
				ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET algorithm=?`, strings.Repeat("界", 30000))
			case "overflow":
				ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET revision=?`, int64(math.MaxInt64))
				r.revision = math.MaxInt64
			}
			turn := ipPolicyTestTurn(t, w, IPPolicySet)
			ctx := t.Context()
			if name == "cancel_zero" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			out, err := ipPolicySetBody(ctx, turn, r, ipPolicyTestDecode())
			if name == "predecode_bytes" || name == "overflow" || name == "cancel_zero" {
				if err == nil || out != (ipPolicyResult{}) {
					t.Fatalf("nonzero refused output: %+v %v", out, err)
				}
				if _, e := turn.transaction.rollback(err); e == nil {
					t.Fatal("missing refusal")
				}
				if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_policy_receipts`) != 0 {
					t.Fatal("refused receipt")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expectedRevision := int64(6)
			if name == "no_op" {
				expectedRevision = 5
			}
			if out.revision != expectedRevision {
				t.Fatal(out)
			}
			ipPolicyTestCommit(t, turn)
			if ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_policy_receipts`) != 1 {
				t.Fatal("missing receipt")
			}
			if name == "replay_before_fresh_CAS" || name == "conflict" {
				// Historical echo must precede mutable policy AND consent checks.
				ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET revision=99,enabled=0,algorithm='later'`)
				ipPolicyTestExec(t, w, `UPDATE screen_metadata_consent SET revision=99,confirmed=0`)
				if name == "conflict" {
					r.values.priority++
				}
				turn = ipPolicyTestTurn(t, w, IPPolicySet)
				echo, e := ipPolicySetBody(t.Context(), turn, r, ipPolicyTestDecode())
				if name == "conflict" {
					if !errors.Is(e, ErrConflict) || echo != (ipPolicyResult{}) {
						t.Fatal(echo, e)
					}
					_, _ = turn.transaction.rollback(e)
				} else {
					if e != nil || echo != out {
						t.Fatal(echo, e)
					}
					ipPolicyTestCommit(t, turn)
				}
				if ipPolicyTestScalar(t, w, `SELECT revision FROM metadata_provider_policy`) != 99 {
					t.Fatal("replay changed current owner")
				}
			}
			if name == "receipt_rollback" {
				// Real UNIQUE failure at the LAST statement, after policy mutation.
				ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET acknowledged_command_sequence=1`)
				r.identity.Sequence = 2
				r.revision = 6
				r.values.priority = 3
				turn = ipPolicyTestTurn(t, w, IPPolicySet)
				got, e := ipPolicySetBody(t.Context(), turn, r, ipPolicyTestDecode())
				if e == nil || got != (ipPolicyResult{}) {
					t.Fatal(got, e)
				}
				_, _ = turn.transaction.rollback(e)
				if ipPolicyTestScalar(t, w, `SELECT revision FROM metadata_provider_policy`) != 6 || ipPolicyTestScalar(t, w, `SELECT last_command_sequence FROM metadata_provider_policy`) != 1 {
					t.Fatal("last statement failure leaked prefix")
				}
			}
		})
	}
}

func TestIPPolicyNativePlans(t *testing.T) {
	w := ipPolicyFixture(t)
	if _, err := stmt.Describe(ipPolicyTestDefinitions()); err != nil {
		t.Fatal(err)
	}
	for _, d := range ipPolicyTestDefinitions() {
		t.Run(string(d.ID), func(t *testing.T) {
			if strings.Count(d.SQL, "?") != len(d.Params) {
				t.Fatal("parameter arity", len(d.Params), d.SQL)
			}
			if strings.Contains(d.SQL, "OFFSET") {
				t.Fatal("offset")
			}
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			search := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "CORRELATED") {
					t.Fatal(detail)
				}
				search = search || strings.Contains(detail, "SEARCH ")
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				t.Fatal(err)
			}
			if d.Kind == stmt.Read && !search {
				t.Fatal("no indexed SEARCH")
			}
		})
	}
	// Registration without a Capture/test receipt is deliberately inert.
	if _, err := stmt.New(ipPolicyTestDefinitions(), nil); !errors.Is(err, stmt.ErrUnqualified) {
		t.Fatal(err)
	}
}

func TestIPPolicyDigestAndByteLedger(t *testing.T) {
	r := ipPolicyTestRequest()
	base, err := r.digest()
	if err != nil {
		t.Fatal(err)
	}
	r.values.algorithm = "ab"
	r.values.region = "c"
	a, err := r.digest()
	if err != nil {
		t.Fatal(err)
	}
	r.values.algorithm = "a"
	r.values.region = "bc"
	next, err := r.digest()
	if err != nil {
		t.Fatal(err)
	}
	if a == next || base == next {
		t.Fatal("ambiguous digest")
	}
	b := &ipPolicyDecode{remaining: 3}
	if b.reserve(4) == nil || b.remaining != 3 {
		t.Fatal("overdraw")
	}
	if b.reserve(3) != nil || b.remaining != 0 {
		t.Fatal("actual bytes")
	}
	if b.reserve(-1) == nil {
		t.Fatal("negative")
	}
}
