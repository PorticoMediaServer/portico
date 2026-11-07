package writer

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usSessionNativeFixture(t *testing.T) (*executor, *stmt.Registry) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	i, q, clocks := usSessionFixture(usSessionRenewal)
	c, err := prepareUSSessionRenewal(i, q, clocks)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('us-account','us-account',X'','us-profile',7)`)
	exec(`INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('us-account',?,'member')`, q.pins.memberIncarnation)
	exec(`INSERT INTO direct_profiles(id,account_id,lifetime,name) VALUES('us-profile','us-account',?,'Profile')`, q.pins.profileLifetime)
	exec(`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('us-device','us-account',?,'Device','test','test','1',?,?)`, q.pins.installation, clocks.now, clocks.now)
	for _, family := range []string{"us-family", "us-other-family"} {
		exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,authority_generation,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES(?,'us-server','us-account','us-profile','local','member',7,?,3,'us-device',2,?,?,?)`, family, q.pins.horizon, q.pins.memberIncarnation, q.pins.profileLifetime, q.pins.incarnation[:])
	}
	exec(`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,'us-family',3,?)`, q.pins.accessDigest, q.pins.accessExpiresAt)
	exec(`INSERT INTO identity_refresh_credentials(family_id,device_id,token_hash,generation,expires_at) VALUES('us-family','us-device',?,1,?)`, usCredentialFixtureHash("session-fixture-e"), q.pins.horizon)
	exec(`INSERT INTO identity_refresh_receipts(predecessor_hash,family_id,request_id,ciphertext,expires_at) VALUES(?,'us-family','old',X'01',?),(?,'us-other-family','other',X'02',?)`, usCredentialFixtureHash("session-fixture-0"), q.pins.horizon, usCredentialFixtureHash("session-fixture-1"), q.pins.horizon)
	// Capture prepares but never executes any effect, on the actual modernc
	// connection and schema. Source pins include only this helper's own files.
	registry := nativeInventoryFixtureRegistry(t, w, usSessionDefinitions(), "us_session_prepare.go", "us_session_statements.go", "us_session_statements_test.go")
	var gen int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT current_generation FROM authorization_session_families WHERE id=?`, c.request.pins.family).Scan(&gen); err != nil || gen != 3 {
		t.Fatal("Capture executed effects", gen, err)
	}
	return w, registry
}

func usSessionNativeTurn(t *testing.T, w *executor, registry *stmt.Registry, c *usSessionRotation) *commandTurn {
	t.Helper()
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := w.begin(t.Context(), SecurityAccept, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	// Test-only frame: a registered FULL fixture class opens the native turn.
	// The private descriptor is then substituted solely to exercise this inert
	// prefix, without ever registering command 511/512 or claiming admission.
	for _, d := range usSessionDescriptors() {
		if d.ID == c.kind {
			tx.descriptor = d
		}
	}
	var steps []statementStep
	for _, d := range usSessionDefinitions() {
		steps = append(steps, statementStep{d.ID, Work{16, 2000, 16, 128 << 10, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func usSessionNativeReadback(t *testing.T, w *executor, wantGen int64, wantRetired int64) {
	t.Helper()
	var gen, retired, otherGen int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT current_generation FROM authorization_session_families WHERE id='us-family'`).Scan(&gen); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT retired FROM authorization_family_tokens WHERE token_hash=?`, usCredentialFixtureHash("session-fixture-c")).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT current_generation FROM authorization_session_families WHERE id='us-other-family'`).Scan(&otherGen); err != nil {
		t.Fatal(err)
	}
	if gen != wantGen || retired != wantRetired || otherGen != 3 {
		t.Fatal(gen, retired, otherGen)
	}
}

func TestUSSessionNativeRenewalAndRefresh(t *testing.T) {
	for _, kind := range []CommandID{usSessionRenewal, usSessionRefresh} {
		t.Run(map[CommandID]string{usSessionRenewal: "renewal", usSessionRefresh: "refresh"}[kind], func(t *testing.T) {
			w, registry := usSessionNativeFixture(t)
			i, q, clocks := usSessionFixture(kind)
			if kind == usSessionRefresh {
				q.priorReceiptDigest = usCredentialFixtureHash("session-fixture-0")
				// A refresh is intentionally valid after the access token's
				// expiry. Its independent native refresh expiry still fences it.
				q.pins.accessExpiresAt = "2026-11-07T11:55:00Z"
				if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, q.pins.accessExpiresAt, q.pins.accessDigest); err != nil {
					t.Fatal(err)
				}
			}
			c, err := prepareUSSessionRotation(kind, i, q, clocks)
			if err != nil {
				t.Fatal(err)
			}
			turn := usSessionNativeTurn(t, w, registry, c)
			var before, after int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			result, err := applyUSSessionRotation(t.Context(), turn, c, clocks.now)
			if err != nil || result != c.nativeResult() {
				t.Fatal(result, err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after-before != int64(len(c.effectIDs())-1) {
				t.Fatal("each effect must change exactly one native row", before, after)
			}
			// Native readback before rollback is component evidence only. There is no
			// saved command receipt and these fixtures never claim Applied/issuance.
			var gen int64
			var horizon string
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT current_generation,authorization_horizon FROM authorization_session_families WHERE id='us-family'`).Scan(&gen, &horizon); err != nil || gen != 4 || horizon != q.output.horizon {
				t.Fatal(gen, horizon, err)
			}
			var digest string
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT token_hash FROM authorization_family_tokens WHERE family_id='us-family' AND generation=4 AND retired=0 AND expires_at=?`, q.output.accessExpiresAt).Scan(&digest); err != nil || digest != q.output.accessDigest {
				t.Fatal(digest, err)
			}
			if kind == usSessionRenewal {
				var controller, epoch, request, predecessor string
				var nonce, wire []byte
				if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT controller_id,controller_epoch,request_id,predecessor_hash,nonce,ciphertext FROM authorization_family_renewals WHERE family_id='us-family'`).Scan(&controller, &epoch, &request, &predecessor, &nonce, &wire); err != nil || controller != q.controllerID || epoch != q.controllerEpoch || request != q.requestID || predecessor != q.pins.accessDigest || string(nonce) != string(q.output.nonce) || string(wire) != string(q.output.ciphertext) {
					t.Fatal("controller-bound renewal not preserved", err)
				}
			} else {
				var refresh string
				var refreshGen int64
				if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT token_hash,generation FROM identity_refresh_credentials WHERE family_id='us-family'`).Scan(&refresh, &refreshGen); err != nil || refresh != q.output.refreshDigest || refreshGen != 4 {
					t.Fatal(refresh, refreshGen, err)
				}
				var request, until, predecessor string
				var wire []byte
				if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT request_id,expires_at,ciphertext FROM identity_refresh_receipts WHERE predecessor_hash=? AND family_id=?`, q.refreshDigest, q.pins.family).Scan(&request, &until, &wire); err != nil || request != q.requestID || until != q.output.horizon || string(wire) != string(q.output.ciphertext) {
					t.Fatal("exact refresh receipt changed", err)
				}
				if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT predecessor_hash FROM identity_refresh_predecessors WHERE predecessor_hash=? AND family_id=?`, q.refreshDigest, q.pins.family).Scan(&predecessor); err != nil || predecessor != q.refreshDigest {
					t.Fatal("refresh predecessor not retained", err)
				}
				var count int64
				if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM identity_refresh_receipts WHERE family_id='us-other-family'`).Scan(&count); err != nil || count != 1 {
					t.Fatal("another family's receipt changed", count, err)
				}
			}
			// Re-executing native CAS is NOT receipt replay. It refuses; W's retained
			// typed receipt must make real retries zero-effect before invoking this.
			if again, err := applyUSSessionRotation(t.Context(), turn, c, clocks.now); !errors.Is(err, ErrConflict) || again != (usSessionNativeResult{}) {
				t.Fatal(again, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			usSessionNativeReadback(t, w, 3, 0)
		})
	}
}

func TestUSSessionNativeStaleBindingsAndPrefixRollback(t *testing.T) {
	w, registry := usSessionNativeFixture(t)
	cases := []struct {
		name string
		kind CommandID
		edit func(*usSessionRotationRequest)
	}{
		{"generation", usSessionRenewal, func(q *usSessionRotationRequest) { q.pins.generation++ }},
		{"epoch", usSessionRenewal, func(q *usSessionRotationRequest) { q.pins.epoch++ }},
		{"authority_generation", usSessionRenewal, func(q *usSessionRotationRequest) { q.pins.authorityGeneration++ }},
		{"member_lifetime", usSessionRenewal, func(q *usSessionRotationRequest) { q.pins.memberIncarnation = strings.Repeat("2", 32) }},
		{"profile_lifetime", usSessionRenewal, func(q *usSessionRotationRequest) { q.pins.profileLifetime = strings.Repeat("2", 32) }},
		{"family_incarnation", usSessionRenewal, func(q *usSessionRotationRequest) { q.pins.incarnation[0]++ }},
		{"installation", usSessionRefresh, func(q *usSessionRotationRequest) { q.pins.installation = strings.Repeat("j", 32) }},
		// The family CAS succeeds but a later exact token/credential/receipt step
		// refuses. The parent must roll back every earlier effect.
		{"token_generation_row", usSessionRenewal, func(q *usSessionRotationRequest) { q.pins.accessDigest = usCredentialFixtureHash("session-fixture-2") }},
		{"refresh_generation_row", usSessionRefresh, func(q *usSessionRotationRequest) { q.refreshGeneration = 2 }},
		{"refresh_expiry_row", usSessionRefresh, func(q *usSessionRotationRequest) { q.refreshExpiresAt = "2026-11-08T13:00:00Z" }},
		{"prior_receipt_row", usSessionRefresh, func(q *usSessionRotationRequest) { q.priorReceiptDigest = usCredentialFixtureHash("session-fixture-2") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i, q, clocks := usSessionFixture(tc.kind)
			tc.edit(&q)
			c, err := prepareUSSessionRotation(tc.kind, i, q, clocks)
			if err != nil {
				t.Fatal(err)
			}
			turn := usSessionNativeTurn(t, w, registry, c)
			result, err := applyUSSessionRotation(t.Context(), turn, c, clocks.now)
			if !errors.Is(err, ErrConflict) || result != (usSessionNativeResult{}) {
				t.Fatal(result, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			usSessionNativeReadback(t, w, 3, 0)
		})
	}
}

func TestUSSessionNativeClockAndLifetime(t *testing.T) {
	w, registry := usSessionNativeFixture(t)
	i, q, clocks := usSessionFixture(usSessionRenewal)
	c, err := prepareUSSessionRenewal(i, q, clocks)
	if err != nil {
		t.Fatal(err)
	}
	for _, now := range []string{"2026-11-07T11:59:59Z", q.pins.accessExpiresAt, q.output.accessExpiresAt} {
		turn := usSessionNativeTurn(t, w, registry, c)
		result, err := applyUSSessionRotation(t.Context(), turn, c, now)
		if !errors.Is(err, ErrConflict) || result != (usSessionNativeResult{}) {
			t.Fatal(now, result, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	turn := usSessionNativeTurn(t, w, registry, c)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := applyUSSessionRotation(ctx, turn, c, clocks.now); !errors.Is(err, context.Canceled) || result != (usSessionNativeResult{}) {
		t.Fatal(result, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	usSessionNativeReadback(t, w, 3, 0)
}

func TestUSSessionSourceBoundary(t *testing.T) {
	for _, name := range []string{"us_session_prepare.go", "us_session_statements.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		allowedImports := map[string]bool{"context": true, "database/sql": true, "errors": true, "math": true, "slices": true, "strings": true, "time": true, "github.com/PorticoMediaServer/portico/server/internal/store/stmt": true}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !allowedImports[path] {
				t.Fatal("unexpected helper dependency", imp.Path.Value)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok {
				if fn.Name.IsExported() {
					t.Error("exported capability", fn.Name)
				}
				if fn.Name.Name == "applyUSSessionRotation" {
					deferred, ok := fn.Body.List[0].(*ast.DeferStmt)
					if !ok || !guardedFinishDefer(fn, deferred) {
						t.Error("native result lacks first-statement zero-on-error release guard")
					}
				}
			}
			if call, ok := n.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch strings.ToLower(selector.Sel.Name) {
					case "open", "opendb", "new", "begin", "begintx", "commit", "grant", "admit":
						t.Error("session helper acquired root capability", selector.Sel.Name)
					}
				}
			}
			return true
		})
	}
	defs := usSessionDefinitions()
	if len(defs) != 10 {
		t.Fatal("unexpected finite effect inventory", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 || len(d.Params) != len(d.Cohorts[0].Bindings) {
			t.Fatal("unbounded/unpinned definition", d.ID)
		}
		if d.ID == usSessionDeviceCheck {
			if d.Kind != stmt.Read || !strings.HasPrefix(d.SQL, "SELECT revision FROM identity_devices INDEXED BY ") {
				t.Fatal("installation guard changed", d.ID)
			}
		} else if d.Kind != stmt.Write || !strings.HasSuffix(d.SQL, "RETURNING family_id") && !strings.HasSuffix(d.SQL, "RETURNING id") {
			t.Fatal("unbounded effect", d.ID)
		}
		if strings.Contains(d.SQL, "DELETE") && d.SQL != `DELETE FROM identity_refresh_receipts INDEXED BY sqlite_autoindex_identity_refresh_receipts_1 WHERE predecessor_hash=? AND family_id=? RETURNING family_id` {
			t.Fatal("non-point deletion", d.ID)
		}
	}
	result, err := applyUSSessionRotation(context.Background(), nil, nil, "")
	if err == nil || result != (usSessionNativeResult{}) {
		t.Fatal(result, err)
	}
}
