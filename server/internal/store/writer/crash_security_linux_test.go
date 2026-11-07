package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Functional component fixtures, not an issuer, admin capability, codec or
// production profile. Historical public classification remains W-owned.
type securityCrashSource struct {
	Initial  effectiveRawJournal
	Identity CommandIdentity
	Request  RequestDigest
	Result   ResultDigest
}

var securityCrashTables = append(append([]string{}, progressCrashTables...),
	"authorization_session_families", "authorization_family_tokens", "identity_devices", "identity_pin_lineages")

var securityCrashBytes = strings.Fields(`id server_id account_id profile_id authority role authorization_horizon revoked_reason revoked_at
remember_until manage_until device_id pin_lineage_id command_member_incarnation command_profile_lifetime command_incarnation
revoke_operation_id revoke_request_digest revoke_command_kind revoke_issuer_account_id revoke_issuer_profile_id
revoke_issuer_member_incarnation revoke_issuer_profile_lifetime revoke_issuer_family_id revoke_result_reason revoke_result_at revoke_result_digest`)

func securityCrashTarget(j effectiveRawJournal) map[string]effectiveRawCell {
	var target map[string]effectiveRawCell
	for _, row := range j["authorization_session_families"].Rows {
		if row["id"].Text == "target" {
			progressRequire(target == nil, "duplicate security target")
			target = row
		}
	}
	progressRequire(target != nil, "missing security target")
	return target
}

// Explicit native field transitions and UTF8 byte sums over ORIGINAL input.
// Neither the production effect nor its receipt store/reducer supplies state.
func expectedSecurityCrash(source securityCrashSource) (want effectiveRawJournal, effect wc008SecurityEffect, charge int64, event wcAPIEventEffect, err error) {
	defer func() {
		if value := recover(); value != nil {
			want, effect, charge, event, err = nil, wc008SecurityEffect{}, 0, wcAPIEventEffect{}, fmt.Errorf("security oracle: %v", value)
		}
	}()
	progressRequire(source.Identity == (CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}) && source.Request == (RequestDigest{3}) && source.Result == (ResultDigest{4}), "original security identity/digests")
	data, err := json.Marshal(source.Initial)
	progressRequire(err == nil && json.Unmarshal(data, &want) == nil && len(want) == len(securityCrashTables), "original security checkpoint")
	row := securityCrashTarget(want)
	progressRequire(row["revoked"].Int == 0 && row["command_last_sequence"].Int == 0 && row["command_acknowledged_sequence"].Int == 0 && row["revoke_operation_id"].Kind == "null", "fresh target input")
	for key, value := range map[string]any{"revoked": int64(1), "revoked_reason": "admin_revoke", "revoked_at": "1970-01-01T00:00:01Z", "teardown_due_ms": int64(1123)} {
		progressSet(row, key, value)
	}
	effect = wc008SecurityEffect{family: "target", reason: "admin_revoke", revokedAt: "1970-01-01T00:00:01Z", appliedMS: 1123, controlVariableBytes: progressBytes(row, securityCrashBytes)}
	for key, value := range map[string]any{
		"command_last_sequence": int64(1), "revoke_command_sequence": int64(1), "revoke_operation_id": source.Identity.OperationID[:],
		"revoke_request_digest": source.Request[:], "revoke_command_kind": "admin_revoke", "revoke_result_version": int64(1),
		"revoke_issuer_account_id": "account", "revoke_issuer_profile_id": "profile", "revoke_issuer_family_id": "issuer",
		"revoke_issuer_member_incarnation": strings.Repeat("a", 32), "revoke_issuer_profile_lifetime": strings.Repeat("b", 32),
		"revoke_result_reason": "admin_revoke", "revoke_result_at": "1970-01-01T00:00:01Z", "revoke_applied_ms": int64(1123),
		"revoke_result_count": int64(1), "revoke_result_digest": source.Result[:],
	} {
		progressSet(row, key, value)
	}
	charge = progressBytes(row, securityCrashBytes)
	progressSet(row, "control_variable_bytes", charge)
	// Ring INPUT has gaps and multiple eligible rows. Exactly the oldest goes;
	// neither an aggregate count nor the candidate's victim chooses this oracle.
	progressRequire(progressNextID(want, "api_events") == 40001, "original ring sequence")
	table := want["api_events"]
	progressRequire(len(table.Rows) == 3, "original ring inventory")
	var kept []map[string]effectiveRawCell
	for _, old := range table.Rows {
		if old["id"].Int == 1 {
			event.removedID, event.removedVariableBytes, event.removed = 1, progressBytes(old, strings.Fields("audience type resource_kind resource_id revision data")), true
		} else {
			kept = append(kept, old)
		}
	}
	progressRequire(event.removed, "original oldest ring row")
	table.Rows = kept
	want["api_events"] = table
	newRow := effectiveModelRow(map[string]any{"id": int64(40001), "audience": "profile:local:account:profile", "type": "devices.changed", "resource_kind": "devices", "resource_id": "target", "revision": "1", "data": `{"family":"target"}`, "at_ms": int64(1123)})
	event.insertedID, event.insertedVariableBytes = 40001, progressBytes(newRow, strings.Fields("audience type resource_kind resource_id revision data"))
	progressAppend(want, "api_events", newRow)
	for name, table := range want {
		sortEffectiveRows(&table)
		want[name] = table
	}
	return want, effect, charge, event, nil
}

func securityCrashCommand(source securityCrashSource) wc008SecurityCommand {
	return wc008SecurityCommand{family: "target", issuerFamily: "issuer", server: "server", account: "account", profile: "profile",
		memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32), identity: source.Identity,
		epoch: 7, tokenGeneration: 3, resultVersion: 1, appliedMS: 1123, revokedAt: "1970-01-01T00:00:01Z",
		oldControlBytes: securityCrashTarget(source.Initial)["control_variable_bytes"].Int}
}

func securityCrashDefinitions() []stmt.Definition {
	return append(wcSecurityDefinitions(), wcAPIEventDefinitions()...)
}

func securityCrashRegistry(t *testing.T, w *executor) *stmt.Registry {
	t.Helper()
	if schema.SHA256 != progressCrashSchema010 {
		t.Fatal("security captures require schema010", schema.SHA256)
	}
	defs := securityCrashDefinitions()
	if len(defs) != 5 {
		t.Fatal("security complete definition inventory changed", len(defs))
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	return nativeFixtureRegistry(t, conn, w.config, defs, "wc_security_008_native_test.go", "crash_security_linux_test.go")
}

func securityCrashTurn(t *testing.T, w *executor, path string, registry *stmt.Registry) (*commandTurn, *governor.Authority) {
	t.Helper()
	// Existing W fixture/binder, actual FULL/Recovery. Figures are synthetic.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	g, authority, wal, request := governedFixture(t, w.now, governedFixtureProfile{recipe, 8 << 20})
	request.Key.Domain, request.Key.ID, request.Class = uint16(SecurityAccept), "security-crash-component", governor.Recovery
	request.Footprint = []governor.Scope{{Domain: uint16(SecurityAccept)}}
	if err := refreshWriterWAL(path, wal, w.now); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(SecurityAccept, recipe.limit, g, authority, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), SecurityAccept, recipe, custody)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, entry := range registry.Manifest() {
		steps = append(steps, statementStep{entry.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, authority
}

func securityCrashPhases() []string {
	var phases []string
	for i, id := range []stmt.ID{wc008SecurityDeny, wc008SecurityReceipt, wcAPIEventInsert, wcAPIEventOldest, wcAPIEventDelete} {
		phases = append(phases, fmt.Sprintf("native_%02d_%s", i, id))
	}
	return phases
}

func securityCrashCase(c crashCase) bool {
	if c.Domain != "wc_security" || c.Operation != "revoke" {
		return false
	}
	if c.CommitFault != "" {
		return c.Boundary == "uncertain_return" && (c.CommitFault == "before" || c.CommitFault == "after")
	}
	for _, phase := range append(securityCrashPhases(), "before_begin", "after_begin", "before_commit", "after_commit", "physical_return", "api_return") {
		if phase == c.Boundary {
			return true
		}
	}
	return false
}

func crashSecurityFixture(t *testing.T, witness *crashWitness) (*executor, securityCrashSource) {
	t.Helper()
	if witness.Case.Boundary != "trace" && !securityCrashCase(witness.Case) {
		t.Fatal("unsupported security case", witness.Case)
	}
	w, path, _ := testExecutor(t)
	q := wc008NativeSecurity(t, w) // fixture INPUT only; never its stmttest turn
	for _, id := range []string{"issuer", "spectator"} {
		wc008NativeExec(t, w, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incarnation,command_profile_lifetime,command_incarnation) SELECT ?,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incarnation,command_profile_lifetime,command_incarnation FROM authorization_session_families WHERE id='target'`, id)
	}
	wc008NativeExec(t, w, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('target-token','target',3,'2100-01-01Z'),('issuer-token','issuer',3,'2100-01-01Z')`)
	wc008NativeExec(t, w, `INSERT INTO api_events(id,audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(1,'admin','list.changed','devices','first','1','old-音',0),(2,'admin','list.changed','devices','second','2','keep-two',1),(100,'admin','list.changed','devices','third','3','keep-three',2)`)
	wc008NativeExec(t, w, `UPDATE sqlite_sequence SET seq=40000 WHERE name='api_events'`)
	initial, err := readEffectiveJournal(t.Context(), w.db, securityCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	source := securityCrashSource{Initial: initial, Identity: q.identity, Request: RequestDigest{3}, Result: ResultDigest{4}}
	witness.Path, witness.Security = path, &source
	registry := securityCrashRegistry(t, w)
	ids := map[string]stmt.ID{}
	for _, d := range securityCrashDefinitions() {
		ids[strings.TrimSpace(d.SQL)] = d.ID
	}
	var sequence []string
	armed := true
	installCrashDriverAt(t, w, path, witness, crashClock, func(query string) {
		if id, ok := ids[strings.TrimSpace(query)]; armed && ok {
			phase := fmt.Sprintf("native_%02d_%s", len(sequence), id)
			sequence = append(sequence, phase)
			crashPoint(t, witness, phase)
		}
	})
	turn, authority := securityCrashTurn(t, w, path, registry)
	effect, err := applyWC008SecurityDeny(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	issuer := wc008IssuerScope{account: q.account, profile: q.profile, ingressFamily: q.issuerFamily, memberIncarnation: q.memberIncarnation, profileLifetime: q.profileLifetime}
	charge, err := storeWC008SecurityResult(t.Context(), turn, q, issuer, effect, source.Request, source.Result)
	if err != nil {
		t.Fatal(err)
	}
	event, err := appendWCAPIEvent(t.Context(), turn, wcAPIEvent{audience: "profile:local:account:profile", eventType: "devices.changed", resourceKind: "devices", resourceID: "target", revision: "1", data: `{"family":"target"}`, atMS: 1123})
	if err != nil {
		t.Fatal(err)
	}
	armed = false
	if !reflect.DeepEqual(sequence, securityCrashPhases()) {
		t.Fatal("security native sequence changed", sequence)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	want, wantEffect, wantCharge, wantEvent, err := expectedSecurityCrash(source)
	if err != nil {
		t.Fatal(err)
	}
	if effect != wantEffect || charge != wantCharge || event != wantEvent {
		t.Fatal("complete private security return changed", effect, wantEffect, charge, wantCharge, event, wantEvent)
	}
	actual, err := readEffectiveJournal(t.Context(), turn.transaction.conn, securityCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, actual); err != nil {
		t.Fatal("security raw journal differs", err)
	}
	physical, err := turn.transaction.commit()
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || physical.state != commitUnknown || !w.ambiguous || !w.fenced {
			t.Fatal("security ambiguous commit escaped", physical, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	} else if err != nil || physical.state != committed || turn.transaction.descriptor.ID != SecurityAccept || turn.transaction.descriptor.Durability != Full || !turn.transaction.policyVerified || !turn.transaction.commitAttempted || turn.transaction.commitReturnedAt.IsZero() || turn.transaction.physicalReturnedAt.IsZero() || !turn.transaction.retirementComplete {
		t.Fatal("security FULL completion missing", physical, err)
	}
	if authority.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("security custody did not retire")
	}
	crashPoint(t, witness, "api_return") // PRIVATE component/frame result, not public Applied
	if witness.Case.Boundary != "trace" {
		t.Fatal("security boundary not reached", witness.Case)
	}
	return w, source
}

func verifySecurityCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	if !securityCrashCase(witness.Case) || witness.Security == nil || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("unsupported security witness")
	}
	committed := witness.Case.Boundary == "after_commit" || witness.Case.Boundary == "physical_return" || witness.Case.Boundary == "api_return" || witness.Case.CommitFault == "after"
	want := witness.Security.Initial
	if committed {
		var err error
		want, _, _, _, err = expectedSecurityCrash(*witness.Security)
		if err != nil {
			t.Fatal(err)
		}
	}
	e := reopenCrashExecutor(t, witness.Path)
	actual, err := readEffectiveJournal(t.Context(), e.db, securityCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, actual); err != nil {
		t.Fatal("security restart lost atomic receipt/effects", err)
	}
	if committed {
		// No public replay/ACK body exists here. The independent raw permanent
		// receipt must survive; re-executing the ORIGINAL deny must refuse with
		// zero output. No fresh permit or history classifier is synthesized.
		registry := securityCrashRegistry(t, e)
		e.now = func() time.Time { return crashClock }
		turn, _ := securityCrashTurn(t, e, witness.Path, registry)
		out, err := applyWC008SecurityDeny(t.Context(), turn, securityCrashCommand(*witness.Security))
		if !errors.Is(err, ErrConflict) || out != (wc008SecurityEffect{}) {
			t.Fatal("restarted original revoke repeated", out, err)
		}
		if _, err := turn.transaction.rollback(err); err != nil && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	actual, err = readEffectiveJournal(t.Context(), e.db, securityCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, actual); err != nil {
		t.Fatal("security replay changed retained state", err)
	}
}

func TestCrashReplaySecurityComponents(t *testing.T) {
	witness := crashWitness{Case: crashCase{"wc_security", "revoke", "trace", ""}}
	w, _ := crashSecurityFixture(t, &witness)
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	for _, phase := range append(securityCrashPhases(), "before_begin", "after_begin", "before_commit", "after_commit", "physical_return", "api_return") {
		t.Run(phase, func(t *testing.T) {
			verifySecurityCrash(t, runCrashChild(t, crashCase{"wc_security", "revoke", phase, ""}))
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("uncertain_return/"+fault, func(t *testing.T) {
			verifySecurityCrash(t, runCrashChild(t, crashCase{"wc_security", "revoke", "uncertain_return", fault}))
		})
	}
}

func TestSecurityCrashRejectsRawCorruption(t *testing.T) {
	witness := crashWitness{Case: crashCase{"wc_security", "revoke", "trace", ""}}
	w, source := crashSecurityFixture(t, &witness)
	want, _, _, _, err := expectedSecurityCrash(source)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct{ name, sql string }{
		{"permanent_digest", `UPDATE authorization_session_families SET revoke_result_digest=zeroblob(32) WHERE id='target'`},
		{"issuer_retention", `UPDATE authorization_session_families SET role='admin' WHERE id='issuer'`},
		{"saved_event", `UPDATE api_events SET data='wrong' WHERE id=40001`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "SAVEPOINT corrupt_security"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO corrupt_security"); err != nil {
					t.Error(err)
				}
				if _, err := conn.ExecContext(t.Context(), "RELEASE corrupt_security"); err != nil {
					t.Error(err)
				}
			}()
			result, err := conn.ExecContext(t.Context(), mutation.sql)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal("corruption did not reach one native row", n, err)
			}
			actual, err := readEffectiveJournal(t.Context(), conn, securityCrashTables)
			if err != nil {
				t.Fatal(err)
			}
			if compareEffectiveJournal(want, actual) == nil {
				t.Fatal("security oracle accepted corruption")
			}
		})
	}
}

func TestSecurityCrashMutants(t *testing.T) {
	// Explicit standard-runner proof, not three nested builds in every local
	// or normal CI invocation. lane-focused always supplies this evidence dir.
	evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if evidence == "" {
		t.Skip("run with lane-focused.yml for production mutant evidence")
	}
	var proofs []map[string]string
	for _, mutation := range []struct{ name, file, old, new, reason string }{
		{"return_only", "wc_security_receipts_008_draft.go", "return controlBytes, turn.checkLifetimes(ctx)", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" { controlBytes++ }; return controlBytes, turn.checkLifetimes(ctx)", "complete private security return changed"},
		{"native_due", "wc_security_008_draft.go", "q.revokedAt, q.appliedMS, q.family", "q.revokedAt, func() int64 { if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" { return q.appliedMS+1 }; return q.appliedMS }(), q.family", "security raw journal differs"},
		{"saved_digest", "wc_security_receipts_008_draft.go", "controlBytes, err = wc008DraftInteger", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" { result[0]++ }; controlBytes, err = wc008DraftInteger", "security raw journal differs"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			original, err := os.ReadFile(mutation.file)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(original), mutation.old) != 1 {
				t.Fatal("mutant source locator changed")
			}
			modified := strings.Replace(string(original), mutation.old, mutation.new, 1)
			if strings.Contains(modified, "import (") {
				modified = strings.Replace(modified, "import (", "import (\n\"os\"", 1)
			} else {
				modified = strings.Replace(modified, "import \"context\"", "import (\"context\"; \"os\")", 1)
			}
			scratch := t.TempDir()
			overlaySource := filepath.Join(scratch, "mutant.go")
			if err := os.WriteFile(overlaySource, []byte(modified), 0600); err != nil {
				t.Fatal(err)
			}
			absolute, err := filepath.Abs(mutation.file)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(map[string]any{"Replace": map[string]string{absolute: overlaySource}})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(scratch, "overlay.json")
			if err := os.WriteFile(overlay, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-overlay", overlay, "-p", "2", "-parallel", "2", "-count=1", "-timeout=2m", "-run", "^TestCrashReplaySecurityComponents$/^api_return$", ".")
			cmd.Env = append(os.Environ(), "TMPDIR="+scratch, "GOTMPDIR="+scratch)
			output, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), mutation.reason) || !strings.Contains(string(output), "TestCrashReplayChild") || strings.Contains(string(output), "[build failed]") {
				t.Fatalf("mutant failed outside intended child check: %v\n%s", err, output)
			}
			a, b := sha256.Sum256(original), sha256.Sum256([]byte(modified))
			proofs = append(proofs, map[string]string{"name": mutation.name, "source_sha256": hex.EncodeToString(a[:]), "mutant_sha256": hex.EncodeToString(b[:]), "reason": mutation.reason})
			t.Logf("rejected at child semantic check: %s", mutation.reason)
		})
	}
	if t.Failed() {
		return
	}
	data, err := json.MarshalIndent(proofs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "security-mutants.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
