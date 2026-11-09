package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	usCredentialOwnerID = "credential-owner"

	// Token()-sized (43-octet) identifiers. Every 521/522 statement compares
	// these witnesses inside SQLite, so identifier length must not change a
	// control decision.
	usCredentialServerID  = "SSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSS"
	usCredentialAccountID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	usCredentialFamilyID = "credential-family-0001"
	usCredentialDeviceID = "credential-device-0001"

	// Never passed to the writer: it stands for the generated plaintext secret
	// that root keeps outside the turn.
	usCredentialPlaintextSentinel = "SENTINELPLAINTEXTSECRET"
	// The sealed artifact body is the only place this sentinel may live.
	usCredentialSealedSentinel = "SENTINELSEALEDBODY"
)

func usCredentialSealedBody(kind string) []byte {
	width := 0
	switch kind {
	case "key":
		width = 62
	case "token":
		width = 63
	case "app_password":
		width = 64
	}
	body := bytes.Repeat([]byte{'Z'}, width)
	copy(body, usCredentialSealedSentinel)
	return body
}

func usCredentialSecretFor(kind string) usCredentialResultSecret {
	key := "credential-key-id"
	return usCredentialResultSecret{keyID: &key, nonce: bytes.Repeat([]byte{7}, 12), body: usCredentialSealedBody(kind), aadDigest: bytes.Repeat([]byte{9}, 32)}
}

// usCredentialAuthorityFixture reuses the reviewed US-01 session fixture (so
// its Group B rows stay in the listed helper) and adds the current fences the
// credential family birth requires (PIN, remember, lineage and its own API
// device). short expires the family token after one second so a deterministic
// fake clock can cross it after entry.
func usCredentialAuthorityFixture(t *testing.T, short bool) (*executor, *usOwnerAuthorityProof) {
	t.Helper()
	w, _, proof := usOwnerAuthorityFixtureIssuer(t, func(i *usPolicyIssuer) {
		i.server, i.account = usCredentialServerID, usCredentialAccountID
	})
	i := proof.witness.issuer
	if short {
		witness := proof.witness
		witness.tokenExpiry = w.now().Add(time.Second).Format(time.RFC3339)
		witness.horizon = w.now().Add(time.Hour).Format(time.RFC3339)
		next, err := prepareUSOwnerAuthority(witness)
		if err != nil {
			t.Fatal(err)
		}
		proof = next
		wc008NativeExec(t, w, `UPDATE authorization_family_tokens SET expires_at=? WHERE family_id='issuer-family'`, witness.tokenExpiry)
		wc008NativeExec(t, w, `UPDATE authorization_session_families SET authorization_horizon=? WHERE id='issuer-family'`, witness.horizon)
	}
	for n, scope := range []string{"profile_pin", "profile_remember", "profile_lineage"} {
		// The closed schema CHECK allows a lineage id only on the lineage scope;
		// the PIN gates carry profile/lifetime with an empty lineage.
		lineage := ""
		if scope == "profile_lineage" {
			lineage = "issuer-lineage"
		}
		wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,lineage_id,updated_ms) VALUES(?,?,?,?,?,?,?,0)`, n+2, scope, i.account, i.memberIncarnation, i.profile, i.profileLifetime, lineage)
	}
	// The credential family is born with its own API device, not the issuer's, so
	// the birth's device fence is a separate current witness.
	wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,device_id,updated_ms) VALUES(5,'device',?,?,?,0)`, i.account, i.memberIncarnation, usCredentialDeviceID)
	return w, proof
}

func usCredentialIssueRequestFor(proof *usOwnerAuthorityProof, kind string) (usCredentialIssueRequest, usFamilyBirthInput) {
	i := proof.witness.issuer
	target := usCredentialTarget{account: i.account, memberIncarnation: i.memberIncarnation, profile: i.profile, profileLifetime: i.profileLifetime, username: i.account, profileName: "Issuer", epoch: 1, memberRevision: 1, profileRevision: 1}
	birth := usFamilyBirthInput{family: usCredentialFamilyID, server: i.server, account: i.account, profile: i.profile, member: i.memberIncarnation, lifetime: i.profileLifetime, device: usCredentialDeviceID, lineage: proof.witness.lineage, horizon: proof.witness.horizon, epoch: 1, authorityGeneration: 1, incarnation: [16]byte{4, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}}
	request := usCredentialIssueRequest{target: target, id: "credential-id-0001", kind: kind, name: "Automation", scopes: []string{"playback", "read"},
		secretHash: usCredentialFixtureHash(usCredentialPlaintextSentinel), hint: "abcdef", family: birth.family, device: birth.device}
	if kind == "app_password" {
		request.hint = ""
	}
	return request, birth
}

func usCredentialIssueCommandFixture(t *testing.T, w *executor, proof *usOwnerAuthorityProof, kind string, identity CommandIdentity) *usCredentialNativeCommand {
	t.Helper()
	request, birth := usCredentialIssueRequestFor(proof, kind)
	command, err := prepareUSCredentialNativeIssue(identity, usCredentialOwnerID, proof.witness.issuer, request, birth, usCredentialSecretFor(kind), w.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func usCredentialRevokeRequestFor(w *executor, issue *usCredentialNativeCommand) usCredentialRevokeRequest {
	p := issue.issue.prepared
	return usCredentialRevokeRequest{target: p.target, id: p.id, kind: p.kind, name: p.name, scopes: p.scopes, secretHash: p.secretHash,
		hint: p.hint, family: p.family, device: p.device, createdMS: w.now().UnixMilli(), expiresMS: p.expiresMS}
}

func usCredentialRevokeCommandFixture(t *testing.T, w *executor, proof *usOwnerAuthorityProof, issue *usCredentialNativeCommand, identity CommandIdentity) *usCredentialNativeCommand {
	t.Helper()
	command, err := prepareUSCredentialNativeRevoke(identity, usCredentialOwnerID, proof.witness.issuer, usCredentialRevokeRequestFor(w, issue))
	if err != nil {
		t.Fatal(err)
	}
	return command
}

// usCredentialIssuedAcknowledged issues one token and advances the owner's ACK
// floor so a following revoke is admissible.
func usCredentialIssuedAcknowledged(t *testing.T, w *executor, proof *usOwnerAuthorityProof, r *stmt.Registry) *usCredentialNativeCommand {
	t.Helper()
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	if _, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command)); err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	if _, floor, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request, ack: true}); err != nil || floor.AcknowledgedThrough != 1 {
		t.Fatal("ack", floor, err)
	}
	return command
}

func usCredentialOwnerFixture(t *testing.T, w *executor, proof *usOwnerAuthorityProof, incarnation [16]byte) {
	t.Helper()
	i := proof.witness.issuer
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`,
		usCredentialOwnerID, incarnation[:], i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation, i.family)
}

func usCredentialRegistry(t *testing.T) *stmt.Registry {
	t.Helper()
	defs := append([]stmt.Definition(nil), usAuthorityDefinitions()...)
	defs = append(defs, usFamilyBirthDefinitions()...)
	defs = append(defs, usCredentialDefinitions()...)
	defs = append(defs, usCredentialReceiptDefinitions()...)
	// Family-birth statements are only ever executed through this fixture (the
	// production birth path owns its own capture), so they carry no qualified
	// cohort. Give each missing one the same synthetic, type-checked bindings the
	// raw native turn uses; the fixture never treats them as evidence.
	for i := range defs {
		if len(defs[i].Cohorts) != 0 {
			continue
		}
		var args []any
		for _, p := range defs[i].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "unit")
			case stmt.Blob:
				args = append(args, []byte{1})
			default:
				args = append(args, int64(1))
			}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "unqualified-native-control", Bindings: args}}
	}
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func usCredentialTurn(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, kind CommandID, memory uint64) (*commandTurn, *usOwnerAuthorization, context.CancelFunc) {
	t.Helper()
	if memory == 0 {
		memory = 4 << 20
	}
	recipe := testRecipe()
	recipe.limit = Work{100000, 10_000_000, 100000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), kind, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{8, 5000, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	lifetime, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	budget := usOwnerAuthorityBudget{memory: memory, fixture: &usOwnerAuthorityFixturePermit{frame: tx}}
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, lifetime, budget)
	if err != nil {
		t.Fatal(err)
	}
	return turn, a, cancel
}

func usCredentialInput(t *testing.T, c *usCredentialNativeCommand) *usCredentialInvocation {
	t.Helper()
	q, err := usCredentialReceiptForCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	return &usCredentialInvocation{request: q, input: c}
}

func usCredentialCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	return usCredentialDBCount(t, w.db, query, args...)
}

func usCredentialDBCount(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// usCredentialTableRow is a byte-identical oracle for one exact source row.
// Names are test constants only; every value is rendered with its Go type so a
// rollback cannot hide a changed column.
func usCredentialTableRow(t *testing.T, w *executor, table, idColumn, id string) string {
	t.Helper()
	return usCredentialDBRow(t, w.db, table, idColumn, id)
}

// usCredentialDBRow reads the same oracle through any pool, including a fresh
// read-only connection used after a fenced/ambiguous writer.
func usCredentialDBRow(t *testing.T, db *sql.DB, table, idColumn, id string) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT * FROM `+table+` WHERE `+idColumn+`=?`, id)
	if err != nil {
		t.Fatal(table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(table, err)
	}
	var out []string
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			t.Fatal(table, err)
		}
		var b strings.Builder
		for i, value := range values {
			switch v := value.(type) {
			case nil:
				fmt.Fprintf(&b, "%s=NULL;", columns[i])
			case []byte:
				fmt.Fprintf(&b, "%s=%x;", columns[i], v)
			default:
				fmt.Fprintf(&b, "%s=%v;", columns[i], v)
			}
		}
		out = append(out, b.String())
	}
	if err = rows.Err(); err != nil {
		t.Fatal(table, err)
	}
	return strings.Join(out, "\n")
}

func usCredentialRevokeRows(t *testing.T, w *executor) (credential, family, device string) {
	t.Helper()
	return usCredentialTableRow(t, w, "api_credentials", "family_id", usCredentialFamilyID),
		usCredentialTableRow(t, w, "authorization_session_families", "id", usCredentialFamilyID),
		usCredentialTableRow(t, w, "identity_devices", "id", usCredentialDeviceID)
}

// The statement inventory stays closed, hot and plan-captured.
func TestUSCredentialStatementCapture(t *testing.T) {
	defs := append(append([]stmt.Definition(nil), usCredentialDefinitions()...), usCredentialReceiptDefinitions()...)
	if len(usCredentialDefinitions()) != 13 || len(usCredentialReceiptDefinitions()) != 13 {
		t.Fatal("credential statement inventory changed", len(usCredentialDefinitions()), len(usCredentialReceiptDefinitions()))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal("statement must stay hot, captured and waiver-free", d.ID)
		}
	}
	w := wc008NativeExecutor(t)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_credential_native.go", "us_credential_result.go")
	if len(registry.Manifest()) != len(defs) {
		t.Fatal("registry did not bind the complete credential inventory")
	}
	wc008NativePrepare(t, w, defs)
}

// Complete top-level issue, original replay with no secret re-disclosure, floor
// ACK and top-level revoke.
func TestUSCredentialNative(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	identity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}
	command := usCredentialIssueCommandFixture(t, w, proof, "token", identity)

	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	original, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	if original.Outcome.State != Applied || original.Outcome.Replayed || original.Value.state != "active" || !original.Secret.present() || original.Digest == (ResultDigest{}) || original.ChargedBytes <= 0 {
		t.Fatal("original", original)
	}
	// Independent oracle: the source row, family marker, receipt debt and the
	// typed result all equal the prepared/returned DTO.
	source := usCredentialSourceRow{}
	if err = w.db.QueryRowContext(t.Context(), `SELECT id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,revoked_ms,ended_reason,last_used_ms FROM api_credentials WHERE id=?`, command.issue.prepared.id).Scan(&source.id, &source.kind, &source.account, &source.profile, &source.name, &source.scopes, &source.secretHash, &source.hint, &source.family, &source.device, &source.createdMS, &source.expiresMS, &source.revokedMS, &source.endedReason, &source.lastUsedMS); err != nil {
		t.Fatal(err)
	}
	if source.id != original.Value.id || source.kind != original.Value.kind || source.name != original.Value.name || source.scopes != strings.Join(original.Value.scopes, ",") || source.family != original.Value.family || source.device != original.Value.device || source.createdMS != original.Value.createdMS || source.secretHash != original.Value.secretHash {
		t.Fatal("source oracle disagreed", source)
	}
	if kind := usCredentialCount(t, w, `SELECT security_kind FROM authorization_session_families WHERE id=?`, usCredentialFamilyID); kind != 1 {
		t.Fatal("credential family was not born security_kind=1", kind)
	}
	header := usCredentialCount(t, w, `SELECT variable_bytes FROM security_command_receipts WHERE owner_id=?`, usCredentialOwnerID)
	typed := usCredentialCount(t, w, `SELECT variable_bytes FROM security_credential_results WHERE owner_id=?`, usCredentialOwnerID)
	scopes := usCredentialCount(t, w, `SELECT coalesce(sum(variable_bytes),0) FROM security_credential_result_scopes WHERE owner_id=?`, usCredentialOwnerID)
	if header+typed+scopes != original.ChargedBytes {
		t.Fatal("charge oracle", header, typed, scopes, original.ChargedBytes)
	}
	// A token has no recoverable owner secret.
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_credential_secrets`); n != 0 {
		t.Fatal("token wrote recoverable custody", n)
	}

	// Replay: the saved original, zero charge, and NO protected wire again. The
	// sealed columns stay exactly as the first response wrote them; replay must
	// never read them back into the returned value.
	sealedBefore := usCredentialStoredSecretBody(t, w)
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Digest != original.Digest {
		t.Fatal("replay was not the saved original", replay)
	}
	if original.Value.secret.present() || replay.Value.secret.present() {
		t.Fatal("saved value carried the sealed wire", original.Value, replay.Value)
	}
	if !reflect.DeepEqual(replay.Value, original.Value) {
		t.Fatal("replay differed from the saved original", replay)
	}
	if replay.Secret.present() || replay.ChargedBytes != 0 || replay.OwnerCount != 1 {
		t.Fatal("replay re-disclosed the secret or rebuilt a receipt", replay)
	}
	// A real scan of the returned value, not its redacted Format string.
	usCredentialScanValue(t, "replay", replay)
	if sealedAfter := usCredentialStoredSecretBody(t, w); !bytes.Equal(sealedAfter, sealedBefore) || !bytes.Equal(sealedAfter, usCredentialSealedBody("token")) {
		t.Fatal("replay rewrote the sealed column", len(sealedBefore), len(sealedAfter))
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 1 {
		t.Fatal("replay wrote source", n)
	}

	// ACK advances only the floor.
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	ack, floor, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request, ack: true})
	if err != nil || floor.AcknowledgedThrough != 1 || !reflect.DeepEqual(ack, usCredentialDispatchResult{}) {
		t.Fatal("ack", ack, floor, err)
	}

	// Top-level revoke of the same source, then its own original replay.
	revokeIdentity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2}
	revoke := usCredentialRevokeCommandFixture(t, w, proof, command, revokeIdentity)
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	revoked, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Outcome.State != Applied || revoked.Value.state != "revoked" || revoked.Value.revokedMS <= 0 || revoked.Secret.present() {
		t.Fatal("revoke result", revoked)
	}
	if ms := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id=?`, command.issue.prepared.id); ms != revoked.Value.revokedMS {
		t.Fatal("source revoke oracle", ms)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials WHERE parent_id IS NOT NULL`); n != 0 {
		t.Fatal("a descendant was created or touched", n)
	}
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	revokeReplay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, revoke).request})
	if err != nil || revokeReplay.Outcome.State != Applied || !revokeReplay.Outcome.Replayed || !reflect.DeepEqual(revokeReplay.Value, revoked.Value) {
		t.Fatal("revoke replay", revokeReplay, err)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 2 {
		t.Fatal("replay built a receipt", n)
	}
}

// The saved-result read binds the sealed columns to concrete Go destinations.
// The guarded command row refuses *sql.RawBytes with
// "raw_scan_destination: invalid writer input (limit 0)", so the replay path
// must scan the stored artifact even though it never projects it.
func TestUSCredentialResultReadScansSealedColumns(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "key", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	if _, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command)); err != nil {
		t.Fatal(err)
	}
	q, err := usCredentialReceiptForCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	turn, _, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	v, typedBytes, scopeCount, err := readUSCredentialResult(t.Context(), turn, q)
	if err != nil {
		t.Fatalf("saved result scan refused: %v", err)
	}
	if typedBytes <= 0 || scopeCount != 2 || !v.secret.present() || !bytes.Equal(v.secret.body, usCredentialSealedBody("key")) {
		t.Fatal("saved result scan did not read the sealed columns", typedBytes, scopeCount, v.secret.present())
	}
}

// A replay is independent of later source drift: only the retained receipt and
// its typed cohort are read.
func TestUSCredentialReplayAfterSourceDrift(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	original, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE api_credentials SET name='Renamed',scopes='admin' WHERE id=?`, command.issue.prepared.id)
	wc008NativeExec(t, w, `UPDATE direct_profiles SET name='Renamed',revision=revision+1 WHERE id=?`, command.issue.prepared.target.profile)
	wc008NativeExec(t, w, `UPDATE direct_memberships SET revision=revision+1 WHERE account_id=?`, usCredentialAccountID)
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replay.Value, original.Value) || replay.Value.secret.present() || replay.Secret.present() {
		t.Fatal("source drift changed the saved original", replay)
	}
	usCredentialScanValue(t, "drift replay", replay)
}

// Key and app-password keep current owner-recovery custody; a token does not.
// The sealed body is stored only in the sealed secret columns.
func TestUSCredentialSecretBinding(t *testing.T) {
	for _, kind := range []string{"key", "token", "app_password"} {
		t.Run(kind, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			command := usCredentialIssueCommandFixture(t, w, proof, kind, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
			out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
			if err != nil {
				t.Fatal(err)
			}
			width := len(usCredentialSealedBody(kind))
			if len(out.Secret.body) != width || !bytes.Equal(out.Secret.body, usCredentialSealedBody(kind)) {
				t.Fatal("first response did not carry the exact sealed wire", len(out.Secret.body))
			}
			wantSecrets := int64(0)
			if kind != "token" {
				wantSecrets = 1
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM security_credential_secrets`); n != wantSecrets {
				t.Fatal("recoverable custody count", n, wantSecrets)
			}
			if out.Value.secret.present() {
				t.Fatal("first-response value carried the sealed wire")
			}
			// The digest binds the sealed artifact even though the dispatch
			// value does not: re-attaching the original sealed wire reproduces
			// the saved digest, and any change is a different result.
			v := cloneUSCredentialResult(out.Value)
			v.secret = usCredentialSecretFor(kind)
			bound, err := usCredentialResultDigest(command.identity, command.digest, v)
			if err != nil || bound != out.Digest {
				t.Fatal("sealed wire did not reconstruct the saved digest", err)
			}
			v.secret.body[0]++
			if d, e := usCredentialResultDigest(command.identity, command.digest, v); e != nil || d == out.Digest {
				t.Fatal("sealed body not bound", e)
			}
		})
	}
}

// A deny that arrives after classification REPLACES the body outcome.
func TestUSCredentialDeferredDenialReplacesOutcome(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	q, err := usCredentialReceiptForCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	a.until = w.now().Add(-time.Second)
	custody, err := lookupUSCredentialCustody(t.Context(), turn, a, q)
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(custody, usCredentialCustody{}) {
		t.Fatal("deferred custody denial joined or leaked", custody, err)
	}
	v, err := usCredentialFreshResult(command.kind, command.owner, command, w.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	digest, err := usCredentialResultDigest(command.identity, command.digest, v)
	if err != nil {
		t.Fatal(err)
	}
	charged, err := storeUSCredentialResult(t.Context(), turn, a, q, v, digest)
	if !errors.Is(err, ErrDisclosureDenied) || charged != 0 {
		t.Fatal("deferred store denial joined or leaked", charged, err)
	}
	// The open frame holds the writer's only connection, so release it before the
	// direct oracle query.
	_, _ = turn.transaction.rollback(nil)
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 0 {
		t.Fatal("denied store wrote a receipt", n)
	}
}

// The entry recheck passes, the clock then moves past the stamp's own expiry and
// a later deferred recheck denies, replacing any partial body outcome.
func TestUSCredentialEntryPassesDeferredDenies(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, true)
	base := w.now()
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	// The same stamp and source pass an entry recheck.
	if err := a.recheck(t.Context(), turn); err != nil {
		t.Fatal("entry recheck must pass", err)
	}
	// Move only the trusted writer clock, deterministically, after a handful of
	// reads. The transaction deadline (start+3s) still holds.
	calls := 0
	w.now = func() time.Time {
		calls++
		if calls <= 4 {
			return base
		}
		return base.Add(2 * time.Second)
	}
	out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("late recheck did not replace the outcome", out, floor, err)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 0 {
		t.Fatal("denied issue survived", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 0 {
		t.Fatal("denied issue wrote a receipt", n)
	}
}

// The same deferred deny, but landing AFTER the source write and before release,
// must still replace the outcome with zero output and roll the whole prefix
// back. The reviewed path reads the trusted clock 16 times before the effect
// (three per successful owner recheck for quota, custody and lookup, plus the
// applied-ms sample); the 17th read is the family birth, which runs after
// api_credentials is inserted. The store's later recheck then denies on the
// expired token.
func TestUSCredentialPostWriteDeferredDenial(t *testing.T) {
	const preEffectClockReads = 16
	w, proof := usCredentialAuthorityFixture(t, true)
	base := w.now()
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	calls := 0
	w.now = func() time.Time {
		calls++
		if calls <= preEffectClockReads {
			return base
		}
		return base.Add(2 * time.Second)
	}
	out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("post-write deny did not replace the outcome", out, floor, err)
	}
	if calls <= preEffectClockReads {
		t.Fatal("the deferred clock flip never landed", calls)
	}
	for _, query := range []string{
		`SELECT count(*) FROM api_credentials`,
		`SELECT count(*) FROM authorization_session_families WHERE id='credential-family-0001'`,
		`SELECT count(*) FROM identity_devices WHERE id='credential-device-0001'`,
		`SELECT count(*) FROM security_command_receipts`,
		`SELECT count(*) FROM security_credential_results`,
		`SELECT retained_count FROM security_command_owners WHERE id='credential-owner'`,
	} {
		if n := usCredentialCount(t, w, query); n != 0 {
			t.Fatal("post-write deny left a prefix", query, n)
		}
	}
}

// A failure after a nonempty prefix (including the scope children) rolls the
// whole issue back: source, family, device, receipt, result and debt.
func TestUSCredentialChildScopeRollback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(*commandTurn)
	}{
		{"scope_child", func(turn *commandTurn) { delete(turn.steps, usCredentialScopeInsert) }},
		{"typed_result", func(turn *commandTurn) { delete(turn.steps, usCredentialResultInsert) }},
		{"receipt_header", func(turn *commandTurn) { delete(turn.steps, usCredentialReceiptInsert) }},
		{"owner_advance", func(turn *commandTurn) { delete(turn.steps, usCredentialOwnerAdvance) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			command := usCredentialIssueCommandFixture(t, w, proof, "key", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
			tc.remove(turn)
			if out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command)); err == nil || !reflect.DeepEqual(out, usCredentialDispatchResult{}) {
				t.Fatal("partial prefix escaped", out, err)
			}
			for _, query := range []string{
				`SELECT count(*) FROM api_credentials`,
				`SELECT count(*) FROM authorization_session_families WHERE id='credential-family-0001'`,
				`SELECT count(*) FROM identity_devices WHERE id='credential-device-0001'`,
				`SELECT count(*) FROM security_credential_results`,
				`SELECT count(*) FROM security_credential_result_scopes`,
				`SELECT count(*) FROM security_credential_secrets`,
				`SELECT retained_count FROM security_command_owners WHERE id='credential-owner'`,
			} {
				if n := usCredentialCount(t, w, query); n != 0 {
					t.Fatal("prefix survived rollback", query, n)
				}
			}
		})
	}
}

// An uncertain COMMIT is never Applied.
func TestUSCredentialUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
			var path string
			if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			native := false
			fault.afterNativeCommit = func() { native = true }
			installFaultPool(w, fault)
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
			out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("uncertain commit exposed success", out, floor, err)
			}
			if fault.commits.Load() != 1 || native != after {
				t.Fatal("physical COMMIT disposition changed", fault.commits.Load(), native, after)
			}
			if _, err := w.begin(t.Context(), usAPICredentialIssue, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("fenced writer admitted another turn", err)
			}
		})
	}
}

// A caller cancellation landing immediately after the physical COMMIT must not
// publish output, and a retry must resolve the committed original without the
// sealed wire.
func TestUSCredentialPostCommitCallerCancel(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	var path string
	if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fault := &faultConnector{Connector: connector, afterNativeCommit: cancel}
	installFaultPool(w, fault)
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	out, floor, err := runUSCredentialDispatch(ctx, turn, a, usCredentialInput(t, command))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("late caller cancel published output", out, floor, err)
	}
	if fault.commits.Load() != 1 {
		t.Fatal("physical COMMIT did not happen", fault.commits.Load())
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 1 {
		t.Fatal("committed source missing", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 1 {
		t.Fatal("committed receipt missing", n)
	}
	// Retry: the same original resolves, without the first-response secret.
	before := usCredentialCount(t, w, `SELECT total_changes()`)
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request})
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed {
		t.Fatal("retry did not resolve the original", replay, err)
	}
	if replay.Secret.present() || replay.Value.secret.present() {
		t.Fatal("retry re-disclosed the sealed wire", replay)
	}
	usCredentialScanValue(t, "cancelled retry", replay)
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 1 {
		t.Fatal("retry re-applied the source", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 0 {
		t.Fatal("retry appended an event", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 1 {
		t.Fatal("retry wrote a second receipt", n)
	}
	if after := usCredentialCount(t, w, `SELECT total_changes()`); after != before {
		t.Fatal("retry made new effects", before, after)
	}
}

// The same withdrawal applies to 522: a caller cancellation after the physical
// COMMIT must publish nothing, and a retry resolves the ORIGINAL with no second
// marker, device revision bump, hint event or receipt.
func TestUSCredentialRevokePostCommitCallerCancel(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	issue := usCredentialIssuedAcknowledged(t, w, proof, r)
	revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
	var path string
	if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fault := &faultConnector{Connector: connector, afterNativeCommit: cancel}
	installFaultPool(w, fault)
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	out, floor, err := runUSCredentialDispatch(ctx, turn, a, usCredentialInput(t, revoke))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("late caller cancel published output", out, floor, err)
	}
	if fault.commits.Load() != 1 {
		t.Fatal("physical COMMIT did not happen", fault.commits.Load())
	}
	credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 1 {
		t.Fatal("committed 522 receipt missing", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 1 {
		t.Fatal("committed 522 hint missing", n)
	}
	// Retry: the same original resolves with no second source, marker, family,
	// device, hint or receipt effect.
	before := usCredentialCount(t, w, `SELECT total_changes()`)
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, revoke).request})
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Value.state != "revoked" || replay.Value.revokedMS <= 0 {
		t.Fatal("retry did not resolve the original", replay, err)
	}
	credentialAgain, familyAgain, deviceAgain := usCredentialRevokeRows(t, w)
	if credentialAgain != credentialAfter || familyAgain != familyAfter || deviceAgain != deviceAfter {
		t.Fatal("retry wrote a source/family/device effect")
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 1 {
		t.Fatal("retry appended an event", n)
	}
	if n := usCredentialCount(t, w, `SELECT revision FROM identity_devices WHERE id=?`, usCredentialDeviceID); n != 2 {
		t.Fatal("retry bumped the device revision", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 1 {
		t.Fatal("retry wrote a second receipt", n)
	}
	if after := usCredentialCount(t, w, `SELECT total_changes()`); after != before {
		t.Fatal("retry made new effects", before, after)
	}
}

// A canceled preparation denies before any read and writes nothing.
func TestUSCredentialCanceledEntryDenies(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, cancel := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	cancel()
	if out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command)); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) {
		t.Fatal("canceled preparation was accepted", out, err)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 0 {
		t.Fatal("canceled preparation wrote source", n)
	}
}

// Both retained bounds refuse BEFORE any source effect.
func TestUSCredentialQuotaByteAndCountRefuse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int64
		bytes int64
	}{
		{"byte_bound", 1, usCredentialOwnerReceiptBytes - 1},
		{"count_bound", usCredentialOwnerReceipts, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			wc008NativeExec(t, w, `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=?,retained_variable_bytes=? WHERE id=?`, tc.count, tc.bytes, usCredentialOwnerID)
			r := usCredentialRegistry(t)
			identity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{4}, Sequence: 2}
			request, birth := usCredentialIssueRequestFor(proof, "token")
			request.id, request.family = "credential-id-0002", "credential-family-0002"
			birth.family, birth.incarnation = request.family, [16]byte{9}
			next, err := prepareUSCredentialNativeIssue(identity, usCredentialOwnerID, proof.witness.issuer, request, birth, usCredentialSecretFor("token"), w.now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
			out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, next))
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usCredentialDispatchResult{}) {
				t.Fatal("quota", out, err)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 0 {
				t.Fatal("capacity refusal wrote source", n)
			}
		})
	}
}

// A retired owner cannot be resurrected by a write or an ACK.
func TestUSCredentialRetiredOwner(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	original, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE security_command_owners SET retired_ms=1 WHERE id=?`, usCredentialOwnerID)
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request})
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) {
		t.Fatal("retired owner replay", replay, err)
	}
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	if out, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request, ack: true}); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) {
		t.Fatal("retired owner ACK", out, err)
	}
	identity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{4}, Sequence: 2}
	request, birth := usCredentialIssueRequestFor(proof, "token")
	request.id, request.family = "credential-id-0002", "credential-family-0002"
	birth.family, birth.incarnation = request.family, [16]byte{9}
	next, err := prepareUSCredentialNativeIssue(identity, usCredentialOwnerID, proof.witness.issuer, request, birth, usCredentialSecretFor("token"), w.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	if out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, next)); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) {
		t.Fatal("retired owner accepted a fresh write", out, err)
	}
}

// The dispatcher funds its own authority rechecks out of the parent's grant.
func TestUSCredentialRecheckFunding(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "token", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	for _, memory := range []uint64{uint64(usCredentialDispatchRechecks*usCredentialRecheckBytes) - 1, uint64(usCredentialDispatchRechecks * usCredentialRecheckBytes)} {
		turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, memory)
		out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, usCredentialDispatchResult{}) {
			t.Fatal("unqualified funding", memory, out, err)
		}
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 0 {
		t.Fatal("unfunded dispatch wrote source", n)
	}
	if usCredentialDispatchRechecks != 10 {
		t.Fatal("declared recheck count changed without a funding review")
	}
}

// Foreign account/profile/member incarnation/lifetime targets are refused before
// any source effect, with zero output and no receipt.
func TestUSCredentialForeignAccountTarget(t *testing.T) {
	for name, mutate := range map[string]func(*usCredentialTarget){
		"account": func(t *usCredentialTarget) { t.account = "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF" },
		"profile": func(t *usCredentialTarget) { t.profile = "foreign-profile" },
		"member":  func(t *usCredentialTarget) { t.memberIncarnation = strings.Repeat("f", 32) },
		"lifetime": func(t *usCredentialTarget) {
			t.profileLifetime = strings.Repeat("f", 32)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			request, birth := usCredentialIssueRequestFor(proof, "token")
			mutate(&request.target)
			birth.account, birth.member, birth.profile, birth.lifetime = request.target.account, request.target.memberIncarnation, request.target.profile, request.target.profileLifetime
			command, err := prepareUSCredentialNativeIssue(CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, usCredentialOwnerID, proof.witness.issuer, request, birth, usCredentialSecretFor("token"), w.now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
			out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
			if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) {
				t.Fatal("foreign target", out, err)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials`); n != 0 {
				t.Fatal("foreign target wrote source", n)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 0 {
				t.Fatal("foreign target wrote a receipt", n)
			}
		})
	}
}

func usCredentialAssertRevokeRefused(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, command *usCredentialNativeCommand) {
	t.Helper()
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
	if err == nil || (!errors.Is(err, ErrConflict) && !errors.Is(err, ErrDisclosureDenied)) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("revoke authority refusal", out, floor, err)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 1 {
		t.Fatal("refused revoke wrote a receipt", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_credential_results`); n != 1 {
		t.Fatal("refused revoke wrote a result", n)
	}
	if n := usCredentialCount(t, w, `SELECT retained_count FROM security_command_owners WHERE id=?`, usCredentialOwnerID); n != 1 {
		t.Fatal("refused revoke charged the owner", n)
	}
}

// Revoke is bounded to the exact current TOP-LEVEL source row under the caller's
// account key. A foreign credential id, a descendant row and a stale witness are
// each refused with zero output, no receipt and no source change.
func TestUSCredentialRevokeRefusesForeignChildAndStaleWitness(t *testing.T) {
	revokeIdentity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{7}, Sequence: 2}

	t.Run("foreign_credential_id", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		// A credential that really exists, but under another account, while the
		// request still carries the caller's account key.
		wc008NativeExec(t, w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('foreign-account','foreign-user',X'','foreign-profile')`)
		wc008NativeExec(t, w, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('credential-foreign-0001','token','foreign-account','foreign-profile','Automation','read,playback',?,'abcdef','credential-foreign-family','credential-foreign-device',?)`, usCredentialFixtureHash("foreign-credential"), w.now().UnixMilli())
		request := usCredentialRevokeRequestFor(w, issue)
		request.id, request.family, request.device, request.secretHash = "credential-foreign-0001", "credential-foreign-family", "credential-foreign-device", usCredentialFixtureHash("foreign-credential")
		command, err := prepareUSCredentialNativeRevoke(revokeIdentity, usCredentialOwnerID, proof.witness.issuer, request)
		if err != nil {
			t.Fatal(err)
		}
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id='credential-foreign-0001'`); n != 0 {
			t.Fatal("foreign credential was revoked", n)
		}
	})

	t.Run("child_row", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		i := proof.witness.issuer
		wc008NativeExec(t, w, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,parent_id) VALUES('credential-child-0001','token',?,?,'Automation','read,playback',?,'abcdef','credential-child-family','credential-child-device',?,0,?)`, i.account, i.profile, usCredentialFixtureHash("child-credential"), w.now().UnixMilli(), issue.issue.prepared.id)
		request := usCredentialRevokeRequestFor(w, issue)
		request.id, request.family, request.device, request.secretHash = "credential-child-0001", "credential-child-family", "credential-child-device", usCredentialFixtureHash("child-credential")
		command, err := prepareUSCredentialNativeRevoke(revokeIdentity, usCredentialOwnerID, proof.witness.issuer, request)
		if err != nil {
			t.Fatal(err)
		}
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id='credential-child-0001'`); n != 0 {
			t.Fatal("descendant credential was revoked", n)
		}
		if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials WHERE parent_id=?`, issue.issue.prepared.id); n != 1 {
			t.Fatal("descendant row disappeared", n)
		}
	})

	t.Run("stale_witness", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		request := usCredentialRevokeRequestFor(w, issue)
		request.name = "Stale automation"
		command, err := prepareUSCredentialNativeRevoke(revokeIdentity, usCredentialOwnerID, proof.witness.issuer, request)
		if err != nil {
			t.Fatal(err)
		}
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id=?`, issue.issue.prepared.id); n != 0 {
			t.Fatal("stale witness revoked the source", n)
		}
		if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials WHERE id=? AND name='Automation'`, issue.issue.prepared.id); n != 1 {
			t.Fatal("stale witness changed the source", n)
		}
	})
}

// One 522 original writes the source marker, the credential's OWN
// security_kind=1 family denial with its durable teardown obligation, the OWN
// API device's logical retirement and exactly ONE devices hint. Its replay is
// the saved original and writes none of them again.
func TestUSCredentialRevokeOwnFamilyAndDevice(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	issue := usCredentialIssuedAcknowledged(t, w, proof, r)
	issuerFamilyBefore := usCredentialTableRow(t, w, "authorization_session_families", "id", "issuer-family")
	issuerDeviceBefore := usCredentialTableRow(t, w, "identity_devices", "id", "issuer-device")

	revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	revoked, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
	if err != nil {
		t.Fatal(err)
	}
	ms := revoked.Value.revokedMS
	if revoked.Outcome.State != Applied || revoked.Outcome.Replayed || ms <= 0 || revoked.Value.state != "revoked" {
		t.Fatal("revoke result", revoked)
	}

	// (1) Source marker.
	if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id=?`, issue.issue.prepared.id); n != ms {
		t.Fatal("credential marker", n, ms)
	}
	// (2) Own family denial + durable teardown obligation, no family command.
	var flag, kind, due, last int64
	var reason, at string
	var noReceipt bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT revoked,revoked_reason,revoked_at,teardown_due_ms,security_kind,command_last_sequence,revoke_operation_id IS NULL FROM authorization_session_families WHERE id=?`, usCredentialFamilyID).Scan(&flag, &reason, &at, &due, &kind, &last, &noReceipt); err != nil {
		t.Fatal(err)
	}
	if flag != 1 || reason != "admin_revoke" || at != time.UnixMilli(ms).UTC().Format(time.RFC3339) || due != ms || kind != 1 || last != 0 || !noReceipt {
		t.Fatal("own family denial", flag, reason, at, due, kind, last, noReceipt)
	}
	if n := usCredentialCount(t, w, `SELECT command_last_sequence FROM authorization_session_families WHERE id=?`, usCredentialFamilyID); n != 0 {
		t.Fatal("family command was invented", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM authorization_session_families WHERE id=? AND revoke_operation_id IS NULL`, usCredentialFamilyID); n != 1 {
		t.Fatal("family revoke receipt was invented", n)
	}
	// (3) Own API device logical retirement with one revision bump.
	var retired, revision int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT retired_ms,revision FROM identity_devices WHERE id=?`, usCredentialDeviceID).Scan(&retired, &revision); err != nil {
		t.Fatal(err)
	}
	if retired != ms || revision != 2 {
		t.Fatal("own device retirement", retired, revision)
	}
	// (4) Exactly ONE 522 hint and the original receipt.
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events WHERE audience=? AND type='list.changed' AND resource_kind='list' AND resource_id='devices'`, apievents.AdminAudience); n != 1 {
		t.Fatal("522 hint count", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 1 {
		t.Fatal("522 receipt count", n)
	}
	// Issuer session/device are not this operation's target.
	if got := usCredentialTableRow(t, w, "authorization_session_families", "id", "issuer-family"); got != issuerFamilyBefore {
		t.Fatal("issuer family changed")
	}
	if got := usCredentialTableRow(t, w, "identity_devices", "id", "issuer-device"); got != issuerDeviceBefore {
		t.Fatal("issuer device changed")
	}

	// Replay: saved original, no second effect, no second event, no revision
	// bump and no second teardown mark. It is read-only: total_changes does not
	// move.
	credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
	changesBefore := usCredentialCount(t, w, `SELECT total_changes()`)
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, revoke).request})
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, revoked.Value) {
		t.Fatal("revoke replay", replay, err)
	}
	if replay.Secret.present() || replay.Value.secret.present() {
		t.Fatal("revoke replay disclosed a secret")
	}
	if changesAfter := usCredentialCount(t, w, `SELECT total_changes()`); changesAfter != changesBefore {
		t.Fatal("revoke replay wrote changes", changesBefore, changesAfter)
	}
	credentialAgain, familyAgain, deviceAgain := usCredentialRevokeRows(t, w)
	if credentialAgain != credentialAfter || familyAgain != familyAfter || deviceAgain != deviceAfter {
		t.Fatal("replay wrote a source/family/device effect")
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 1 {
		t.Fatal("replay appended an event", n)
	}
	if n := usCredentialCount(t, w, `SELECT revision FROM identity_devices WHERE id=?`, usCredentialDeviceID); n != 2 {
		t.Fatal("replay bumped the device revision", n)
	}

	// The issuer's OWN session still authorizes after the commit.
	turn, b := usOwnerAuthorityTurn(t, w, r)
	if _, err = authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), b); err != nil {
		t.Fatal("issuer session stopped authorizing", err)
	}
	_, _ = turn.transaction.rollback(nil)
}

// Every prefix step fails closed: marker, family, device and hint each roll the
// whole frame back, so no marker-only or family-only response can commit.
func TestUSCredentialRevokePrefixRollback(t *testing.T) {
	for _, step := range []struct{ name, trigger string }{
		{"marker", `CREATE TRIGGER us_credential_fail_marker BEFORE UPDATE ON api_credentials WHEN OLD.revoked_ms=0 AND NEW.revoked_ms>0 BEGIN SELECT RAISE(ABORT,'fault'); END`},
		{"family", `CREATE TRIGGER us_credential_fail_family BEFORE UPDATE ON authorization_session_families WHEN OLD.revoked=0 AND NEW.revoked=1 BEGIN SELECT RAISE(ABORT,'fault'); END`},
		{"device", `CREATE TRIGGER us_credential_fail_device BEFORE UPDATE ON identity_devices WHEN OLD.retired_ms=0 AND NEW.retired_ms>0 BEGIN SELECT RAISE(ABORT,'fault'); END`},
		{"hint", `CREATE TRIGGER us_credential_fail_hint BEFORE INSERT ON api_events BEGIN SELECT RAISE(ABORT,'fault'); END`},
	} {
		t.Run(step.name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
			wc008NativeExec(t, w, step.trigger)
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
			if err == nil || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("faulted prefix carried output", out, floor, err)
			}
			credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
			if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
				t.Fatal("faulted prefix changed a source/family/device row")
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 0 {
				t.Fatal("faulted prefix left an event", n)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 0 {
				t.Fatal("faulted prefix left a 522 receipt", n)
			}
		})
	}
}

// Even a parent that ignores applyUSCredentialRevoke's error cannot COMMIT the
// marker/family/device prefix.
func TestUSCredentialRevokeIgnoredErrorCannotCommit(t *testing.T) {
	for _, missing := range []struct {
		name string
		id   stmt.ID
	}{{"marker", usCredentialRevokeWrite}, {"family", usCredentialFamilyRevoke}, {"device", usCredentialDeviceRetire}} {
		t.Run(missing.name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
			turn, _, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			delete(turn.steps, missing.id)
			out, err := applyUSCredentialRevoke(t.Context(), turn, revoke.revoke, w.now().UnixMilli())
			if err == nil || !reflect.DeepEqual(out, usCredentialRevokeEffect{}) {
				t.Fatal("missing step produced an effect", out, err)
			}
			// The parent ignores err and asks for COMMIT anyway.
			if _, err = turn.transaction.commit(); err == nil {
				t.Fatal("ignored prefix error committed")
			}
			credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
			if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
				t.Fatal("ignored prefix error changed a row")
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 0 {
				t.Fatal("ignored prefix error left an event", n)
			}
		})
	}
}

// Foreign / ordinary-family / mismatched-device / platform-mismatch sources are
// refused with zero output and byte-identical source rows.
func TestUSCredentialRevokeRefusesForeignFamilyAndDevice(t *testing.T) {
	revokeIdentity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{7}, Sequence: 2}

	t.Run("ordinary_family", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=0 WHERE id=?`, usCredentialFamilyID)
		credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
		command := usCredentialRevokeCommandFixture(t, w, proof, issue, revokeIdentity)
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
		if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
			t.Fatal("ordinary family refusal changed a row")
		}
	})

	t.Run("device_of_another_account", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		wc008NativeExec(t, w, `UPDATE identity_devices SET account_id='foreign-account' WHERE id=?`, usCredentialDeviceID)
		credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
		command := usCredentialRevokeCommandFixture(t, w, proof, issue, revokeIdentity)
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
		if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
			t.Fatal("foreign device refusal changed a row")
		}
	})

	t.Run("platform_mismatch", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		wc008NativeExec(t, w, `UPDATE identity_devices SET platform='ios' WHERE id=?`, usCredentialDeviceID)
		credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
		command := usCredentialRevokeCommandFixture(t, w, proof, issue, revokeIdentity)
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
		if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
			t.Fatal("platform mismatch refusal changed a row")
		}
	})

	// SOL-USERS' chain is credential row -> family -> device. A family whose
	// OWN device_id drifted onto another same-account API device must not be
	// denied (retiring the credential row's device, leaving that other device
	// live) and must not commit Applied.
	t.Run("family_on_another_api_device", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		stamp := time.UnixMilli(w.now().UnixMilli()).UTC().Format(time.RFC3339)
		wc008NativeExec(t, w, `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('credential-device-0002',?,'api-credential-other','Second API','api','api-token','',?,?)`, usCredentialAccountID, stamp, stamp)
		wc008NativeExec(t, w, `UPDATE authorization_session_families SET device_id='credential-device-0002' WHERE id=?`, usCredentialFamilyID)
		credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
		otherBefore := usCredentialTableRow(t, w, "identity_devices", "id", "credential-device-0002")
		command := usCredentialRevokeCommandFixture(t, w, proof, issue, revokeIdentity)
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
		if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
			t.Fatal("mismatched family device refusal changed a row")
		}
		if got := usCredentialTableRow(t, w, "identity_devices", "id", "credential-device-0002"); got != otherBefore {
			t.Fatal("mismatched family device changed the other device")
		}
		if n := usCredentialCount(t, w, `SELECT revoked FROM authorization_session_families WHERE id=?`, usCredentialFamilyID); n != 0 {
			t.Fatal("mismatched family was denied", n)
		}
		if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 0 {
			t.Fatal("mismatched family device left a hint", n)
		}
	})

	// A family with the nullable device_id unset is the same refusal: the
	// credential row's device cannot stand in for a family that names none.
	t.Run("family_without_device", func(t *testing.T) {
		w, proof := usCredentialAuthorityFixture(t, false)
		usCredentialOwnerFixture(t, w, proof, [16]byte{1})
		r := usCredentialRegistry(t)
		issue := usCredentialIssuedAcknowledged(t, w, proof, r)
		wc008NativeExec(t, w, `UPDATE authorization_session_families SET device_id=NULL WHERE id=?`, usCredentialFamilyID)
		credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
		command := usCredentialRevokeCommandFixture(t, w, proof, issue, revokeIdentity)
		usCredentialAssertRevokeRefused(t, w, r, proof, command)
		credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
		if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
			t.Fatal("null family device refusal changed a row")
		}
		if n := usCredentialCount(t, w, `SELECT revoked FROM authorization_session_families WHERE id=?`, usCredentialFamilyID); n != 0 {
			t.Fatal("null-device family was denied", n)
		}
		if n := usCredentialCount(t, w, `SELECT retired_ms FROM identity_devices WHERE id=?`, usCredentialDeviceID); n != 0 {
			t.Fatal("null-device family retired the credential device", n)
		}
	})
}

// A repeat 522 against a source that is already fully completed for ANY first
// cause is an idempotent Applied: no second marker/family/device write, no
// second hint, no revision bump and no re-armed teardown. A family revoked for
// another reason, or whose teardown obligation is already completed (NULL), is
// the same exact current target. 522 preserves the first cause and the existing
// lifecycle, and revoked_ms/retired_ms are sticky: they keep their ORIGINAL
// values instead of being rewritten with the new command's now.
func TestUSCredentialRevokeAlreadyRevokedSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate string
	}{
		{"completed_admin_revoke", ""},
		{"completed_another_reason", `UPDATE authorization_session_families SET revoked_reason='password_change' WHERE id='credential-family-0001'`},
		{"completed_teardown_cleared", `UPDATE authorization_session_families SET teardown_due_ms=NULL WHERE id='credential-family-0001'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			revoked, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
			if err != nil {
				t.Fatal(err)
			}
			originalMS := revoked.Value.revokedMS
			// Advance the owner's ACK floor so a second fresh original at
			// sequence 3 is admissible.
			turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			if _, floor, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, revoke).request, ack: true}); err != nil || floor.AcknowledgedThrough != 2 {
				t.Fatal("revoke ack", floor, err)
			}
			if tc.mutate != "" {
				wc008NativeExec(t, w, tc.mutate)
			}
			// Freeze the repeat request against the ORIGINAL clock, then move
			// the trusted clock forward so a non-sticky rewrite would be visible.
			request := usCredentialRevokeRequestFor(w, issue)
			request.revokedMS = originalMS
			repeat, err := prepareUSCredentialNativeRevoke(CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{9}, Sequence: 3}, usCredentialOwnerID, proof.witness.issuer, request)
			if err != nil {
				t.Fatal(err)
			}
			base := w.now()
			w.now = func() time.Time { return base.Add(5 * time.Second) }
			credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
			turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, repeat))
			if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.revokedMS != originalMS {
				t.Fatal("completed repeat", out, floor, err)
			}
			credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
			if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
				t.Fatal("repeat 522 changed a source row")
			}
			// Sticky: the original timestamps, not the new now.
			if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id=?`, issue.issue.prepared.id); n != originalMS {
				t.Fatal("repeat 522 rewrote the marker", n, originalMS)
			}
			if n := usCredentialCount(t, w, `SELECT retired_ms FROM identity_devices WHERE id=?`, usCredentialDeviceID); n != originalMS {
				t.Fatal("repeat 522 rewrote the device retirement", n, originalMS)
			}
			if n := usCredentialCount(t, w, `SELECT revision FROM identity_devices WHERE id=?`, usCredentialDeviceID); n != 2 {
				t.Fatal("repeat 522 bumped the device revision", n)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 2 {
				t.Fatal("completed repeat receipt", n)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 1 {
				t.Fatal("repeat 522 appended a hint", n)
			}
		})
	}
}

// A fresh 522 whose credential row is still live may complete the missing
// marker and device retirement for an ALREADY-revoked family, whatever revoked
// it first. The family's first cause, revoked_at, revoke original/sequence and
// teardown lifecycle stay byte-identical: 522 never rewrites another
// revocation and never re-arms a completed teardown.
func TestUSCredentialRevokeCompletesAlreadyRevokedFamily(t *testing.T) {
	const firstAt = "2020-01-02T03:04:05Z"
	for _, tc := range []struct {
		name    string
		reason  string
		dueMS   int64
		dueNull bool
	}{
		{"another_reason", "password_change", 4321, false},
		{"completed_teardown", "membership_removed", 0, true},
		{"admin_revoke", "admin_revoke", 4321, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			var due any
			if !tc.dueNull {
				due = tc.dueMS
			}
			wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=1,revoked_reason=?,revoked_at=?,teardown_due_ms=? WHERE id=? AND revoked=0`, tc.reason, firstAt, due, usCredentialFamilyID)
			familyBefore := usCredentialTableRow(t, w, "authorization_session_families", "id", usCredentialFamilyID)
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			if revoke.revoke.prepared.request.revokedMS != 0 {
				t.Fatal("fixture marker was not unset")
			}
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
			if err != nil {
				t.Fatal(err)
			}
			ms := out.Value.revokedMS
			if out.Outcome.State != Applied || out.Outcome.Replayed || ms <= 0 || out.Value.state != "revoked" {
				t.Fatal("completion result", out)
			}
			// The missing source marker and device retirement are completed.
			if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id=?`, issue.issue.prepared.id); n != ms {
				t.Fatal("completion marker", n, ms)
			}
			var retired, revision int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT retired_ms,revision FROM identity_devices WHERE id=?`, usCredentialDeviceID).Scan(&retired, &revision); err != nil {
				t.Fatal(err)
			}
			if retired != ms || revision != 2 {
				t.Fatal("completion device retirement", retired, revision)
			}
			// The family row is BYTE-IDENTICAL: first cause, revoked_at,
			// revoke original/sequence and teardown_due_ms are all preserved.
			if got := usCredentialTableRow(t, w, "authorization_session_families", "id", usCredentialFamilyID); got != familyBefore {
				t.Fatal("completion rewrote the family first cause")
			}
			var reason string
			var gotDue sql.NullInt64
			if err = w.db.QueryRowContext(t.Context(), `SELECT revoked_reason,teardown_due_ms FROM authorization_session_families WHERE id=?`, usCredentialFamilyID).Scan(&reason, &gotDue); err != nil {
				t.Fatal(err)
			}
			if reason != tc.reason || gotDue.Valid == tc.dueNull || gotDue.Valid && gotDue.Int64 != tc.dueMS {
				t.Fatal("completion changed the teardown lifecycle", reason, gotDue)
			}
			// No second family command/receipt.
			if n := usCredentialCount(t, w, `SELECT count(*) FROM authorization_session_families WHERE id=? AND command_last_sequence=0 AND revoke_operation_id IS NULL`, usCredentialFamilyID); n != 1 {
				t.Fatal("completion invented a family command")
			}
			// Exactly ONE hint and ONE 522 receipt for the completing original.
			if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events WHERE audience=? AND type='list.changed' AND resource_kind='list' AND resource_id='devices'`, apievents.AdminAudience); n != 1 {
				t.Fatal("completion hint count", n)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 1 {
				t.Fatal("completion receipt count", n)
			}
		})
	}
}

// A receipt failure AFTER the 522 source/family/device prefix rolls the whole
// frame back: no marker, no family denial, no retirement and no hint.
func TestUSCredentialRevokeReceiptFailureRollsBackSource(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	issue := usCredentialIssuedAcknowledged(t, w, proof, r)
	revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
	credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	delete(turn.steps, usCredentialReceiptInsert)
	out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
	if err == nil || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("522 receipt failure carried output", out, floor, err)
	}
	credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
	if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
		t.Fatal("522 receipt failure left a source/family/device prefix")
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 0 {
		t.Fatal("522 receipt failure left a hint", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 0 {
		t.Fatal("522 receipt failure left a receipt", n)
	}
}

// A release-time denial landing AFTER the 522 source/family/device writes still
// replaces the outcome with zero output and rolls the prefix back. The reviewed
// pre-effect path reads the trusted clock 16 times (five owner rechecks plus the
// applied-ms sample); the 17th read is the post-effect recheck, after the
// marker, family denial and device retirement have all been written.
func TestUSCredentialRevokePostEffectDeferredDenial(t *testing.T) {
	const preEffectClockReads = 16
	w, proof := usCredentialAuthorityFixture(t, true)
	base := w.now()
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	issue := usCredentialIssuedAcknowledged(t, w, proof, r)
	revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
	credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	calls := 0
	w.now = func() time.Time {
		calls++
		if calls <= preEffectClockReads {
			return base
		}
		return base.Add(2 * time.Second)
	}
	out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("522 post-effect deny did not replace the outcome", out, floor, err)
	}
	if calls <= preEffectClockReads {
		t.Fatal("the 522 deferred clock flip never landed", calls)
	}
	credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
	if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
		t.Fatal("522 post-effect deny left a source/family/device prefix")
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 0 {
		t.Fatal("522 post-effect deny left a hint", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 0 {
		t.Fatal("522 post-effect deny left a receipt", n)
	}
}

// An uncertain 522 COMMIT is never Applied. Before the physical COMMIT the
// marker/family/device prefix is rolled back; after it, all three are committed
// as one unit, never a partial prefix. Rows are read through a fresh read-only
// pool because the failed writer is fenced.
func TestUSCredentialRevokeUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
			var path string
			if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			native := false
			fault.afterNativeCommit = func() { native = true }
			installFaultPool(w, fault)
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("uncertain 522 commit exposed success", out, floor, err)
			}
			if fault.commits.Load() != 1 || native != after {
				t.Fatal("physical COMMIT disposition changed", fault.commits.Load(), native, after)
			}
			oracle := usCredentialOracle(t, path)
			credentialAfter := usCredentialDBRow(t, oracle, "api_credentials", "family_id", usCredentialFamilyID)
			familyAfter := usCredentialDBRow(t, oracle, "authorization_session_families", "id", usCredentialFamilyID)
			deviceAfter := usCredentialDBRow(t, oracle, "identity_devices", "id", usCredentialDeviceID)
			if after {
				if credentialAfter == credentialBefore || familyAfter == familyBefore || deviceAfter == deviceBefore {
					t.Fatal("committed 522 was not applied as one unit")
				}
				if n := usCredentialDBCount(t, oracle, `SELECT count(*) FROM api_events`); n != 1 {
					t.Fatal("committed 522 hint", n)
				}
			} else {
				if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
					t.Fatal("uncertain 522 left a partial source/family/device prefix")
				}
				if n := usCredentialDBCount(t, oracle, `SELECT count(*) FROM api_events`); n != 0 {
					t.Fatal("uncertain 522 left a hint", n)
				}
			}
		})
	}
}

// usCredentialOracle opens a separate read-only pool for row checks after the
// writer's own pool is fenced by an ambiguous COMMIT.
func usCredentialOracle(t *testing.T, path string) *sql.DB {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Direct-family control: a session minted for the credential's OWN family is
// denied after commit. The fixture clears the independent security_kind/
// credential-presence/API-installation fences so the ONLY remaining denial is
// the committed revoked bit; clearing that bit makes the same session mint.
func TestUSCredentialRevokedFamilyDirectAuthDenied(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	issue := usCredentialIssuedAcknowledged(t, w, proof, r)
	revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	if _, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke)); err != nil {
		t.Fatal(err)
	}

	// Otherwise fully matching ordinary session for the credential family.
	witness := proof.witness
	const installationID = "credential-session-installation-000001"
	witness.device = usCredentialDeviceID
	witness.installationID = installationID
	witness.issuer.family = usCredentialFamilyID
	witness.issuer.familyGeneration = 1
	witness.issuer.installation = strings.Repeat("d", 32)
	witness.issuer.tokenDigest = usCredentialFixtureHash("credential-family-token")
	credentialProof, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(installationID))
	wc008NativeExec(t, w, `UPDATE identity_devices SET installation_id=? WHERE id=?`, installationID, usCredentialDeviceID)
	wc008NativeExec(t, w, `INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, hex.EncodeToString(sum[:]), witness.issuer.installation)
	wc008NativeExec(t, w, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,1,?)`, witness.issuer.tokenDigest, usCredentialFamilyID, witness.tokenExpiry)
	wc008NativeExec(t, w, `DELETE FROM api_credentials WHERE family_id=?`, usCredentialFamilyID)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=0 WHERE id=?`, usCredentialFamilyID)
	// The committed device retirement is an independent fence too; restore it
	// for this control so the only difference between the two attempts is the
	// family's committed revoked bit.
	wc008NativeExec(t, w, `UPDATE identity_devices SET retired_ms=0,revision=1 WHERE id=?`, usCredentialDeviceID)

	turn, b := usOwnerAuthorityTurn(t, w, r)
	if got, err := authorizeUSOwnerPolicy(t.Context(), turn, credentialProof, t.Context(), b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("revoked credential family authorized", got, err)
	}
	_, _ = turn.transaction.rollback(nil)

	// Only the revoked bit differs: the same session now mints.
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=0,revoked_reason='',revoked_at='',teardown_due_ms=NULL WHERE id=?`, usCredentialFamilyID)
	turn, b = usOwnerAuthorityTurn(t, w, r)
	if _, err = authorizeUSOwnerPolicy(t.Context(), turn, credentialProof, t.Context(), b); err != nil {
		t.Fatal("cleared revoked bit still denied", err)
	}
	_, _ = turn.transaction.rollback(nil)

	// The issuer's own session and device remain untouched by 522.
	turn, b = usOwnerAuthorityTurn(t, w, r)
	if _, err = authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), b); err != nil {
		t.Fatal("issuer session denied", err)
	}
	_, _ = turn.transaction.rollback(nil)
}

func TestUSCredentialRevokedMarkerDrainNeverOrdinaryAuthority(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssuedAcknowledged(t, w, proof, r)
	if kind := usCredentialCount(t, w, `SELECT security_kind FROM authorization_session_families WHERE id=?`, usCredentialFamilyID); kind != 1 {
		t.Fatal("credential family kind", kind)
	}
	// Revoke the live source first so the drain below is real: clearing a marker
	// that was never written proves nothing.
	revoke := usCredentialRevokeCommandFixture(t, w, proof, command, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	if _, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke)); err != nil {
		t.Fatal(err)
	}
	if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE family_id=?`, usCredentialFamilyID); n <= 0 {
		t.Fatal("revoke marker was never written", n)
	}
	// Drain the revoked marker: clearing it must not turn the family ordinary.
	wc008NativeExec(t, w, `UPDATE api_credentials SET revoked_ms=0 WHERE family_id=?`, usCredentialFamilyID)
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_credentials WHERE family_id=? AND revoked_ms<>0`, usCredentialFamilyID); n != 0 {
		t.Fatal("revoke marker was not drained", n)
	}
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	if err := a.recheck(t.Context(), turn); err != nil {
		t.Fatal("ordinary issuer session should still pass", err)
	}
	// Release the open frame: the writer has one connection and the direct
	// fixture updates below need it.
	_, _ = turn.transaction.rollback(nil)
	// A fully matching ordinary session family is denied the moment it carries
	// the credential marker, and a security_kind=1 family is denied on its own.
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=1 WHERE id='issuer-family'`)
	turn, b := usOwnerAuthorityTurn(t, w, r)
	if a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), b); a != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("credential kind granted ordinary authority", a, err)
	}
	_, _ = turn.transaction.rollback(nil)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=0 WHERE id='issuer-family'`)
	// The credential marker alone, with a drained (zero) revoke time, still
	// denies ordinary authority for that family.
	request, _ := usCredentialIssueRequestFor(proof, "token")
	request.id, request.family, request.secretHash = "credential-marker-0001", "issuer-family", usCredentialFixtureHash("drained-marker")
	prepared, err := prepareUSCredentialIssue(request, w.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	q, args := usFamilyBirthCredentialRow(prepared)
	wc008NativeExec(t, w, q, args...)
	turn, b = usOwnerAuthorityTurn(t, w, r)
	if a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), b); a != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("drained credential marker granted ordinary authority", a, err)
	}
}

// The plaintext secret never enters the writer. The sealed artifact is stored
// only in the two sealed secret columns and is projected only on a first
// response.
func TestUSCredentialNoSecretLeak(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	command := usCredentialIssueCommandFixture(t, w, proof, "key", CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(out), usCredentialSealedSentinel) {
		t.Fatal("dispatch result formatting disclosed the sealed wire")
	}
	for _, value := range []any{command, command.issue, command.issue.prepared, out, out.Value, out.Secret, usCredentialCompletion{out, AckResult{}, false}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, usCredentialPlaintextSentinel) || strings.Contains(text, usCredentialSealedSentinel) {
				t.Fatal("output leaked credential material", format)
			}
		}
	}
	usCredentialScanForSentinels(t, w, usCredentialPlaintextSentinel, usCredentialSealedSentinel)

	// Replay projects no protected wire at all.
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Secret.present() || replay.Value.secret.present() {
		t.Fatal("replay disclosed the secret again")
	}
	// fmt.Sprint is redacted, so scan the value reachable from the result.
	usCredentialScanValue(t, "no-secret replay", replay)
}

func usCredentialScanForSentinels(t *testing.T, w *executor, plaintext, sealed string) {
	t.Helper()
	tables, err := w.db.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err = tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err = tables.Err(); err != nil {
		t.Fatal(err)
	}
	if err = tables.Close(); err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("empty table inventory")
	}
	sealedColumn := func(table, column string) bool {
		return table == "security_credential_results" && column == "secret_body" || table == "security_credential_secrets" && column == "body"
	}
	for _, table := range names {
		columns, err := w.db.QueryContext(t.Context(), `SELECT name FROM pragma_table_info('`+strings.ReplaceAll(table, "'", "''")+`')`)
		if err != nil {
			t.Fatal(table, err)
		}
		var kept []string
		for columns.Next() {
			var column string
			if err = columns.Scan(&column); err != nil {
				t.Fatal(table, err)
			}
			kept = append(kept, column)
		}
		if err = columns.Err(); err != nil {
			t.Fatal(table, err)
		}
		if err = columns.Close(); err != nil {
			t.Fatal(table, err)
		}
		if len(kept) == 0 {
			continue
		}
		quoted := make([]string, len(kept))
		for i, column := range kept {
			quoted[i] = `"` + column + `"`
		}
		rows, err := w.db.QueryContext(t.Context(), `SELECT `+strings.Join(quoted, ",")+` FROM "`+table+`"`)
		if err != nil {
			t.Fatal(table, err)
		}
		values := make([]any, len(kept))
		ptrs := make([]any, len(kept))
		for i := range values {
			ptrs[i] = &values[i]
		}
		for rows.Next() {
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(table, err)
			}
			for i, value := range values {
				var text string
				switch v := value.(type) {
				case nil:
					continue
				case []byte:
					text = string(v)
				case string:
					text = v
				default:
					text = fmt.Sprint(v)
				}
				if strings.Contains(text, plaintext) {
					t.Fatalf("plaintext secret leaked into %s.%s", table, kept[i])
				}
				if !sealedColumn(table, kept[i]) && strings.Contains(text, sealed) {
					t.Fatalf("sealed wire leaked into %s.%s", table, kept[i])
				}
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(table, err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(table, err)
		}
	}
}

// usCredentialStoredSecretBody reads the one sealed result body the first
// response wrote. Replay must leave it byte-for-byte sealed.
func usCredentialStoredSecretBody(t *testing.T, w *executor) []byte {
	t.Helper()
	var body []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT secret_body FROM security_credential_results WHERE owner_id=?`, usCredentialOwnerID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// usCredentialScanValue walks every reachable string and byte slice of a value
// and fails on either sentinel. The closed types redact Format, so this is the
// real check that a returned value does not carry the sealed artifact.
func usCredentialScanValue(t *testing.T, label string, value any) {
	t.Helper()
	var check func(string)
	check = func(text string) {
		if strings.Contains(text, usCredentialPlaintextSentinel) || strings.Contains(text, usCredentialSealedSentinel) {
			t.Fatalf("%s leaked credential material", label)
		}
	}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice:
			if v.IsNil() {
				return
			}
			if v.Type().Elem().Kind() == reflect.Uint8 {
				check(string(v.Bytes()))
				return
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Array:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				b := make([]byte, v.Len())
				for i := range b {
					b[i] = byte(v.Index(i).Uint())
				}
				check(string(b))
				return
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.String:
			check(v.String())
		}
	}
	walk(reflect.ValueOf(value))
}
