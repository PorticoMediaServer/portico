package writer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

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
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// The statement inventory stays closed, hot and plan-captured.
func TestUSCredentialStatementCapture(t *testing.T) {
	defs := append(append([]stmt.Definition(nil), usCredentialDefinitions()...), usCredentialReceiptDefinitions()...)
	if len(usCredentialDefinitions()) != 7 || len(usCredentialReceiptDefinitions()) != 13 {
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
	revokeIdentity := CommandIdentity{Incarnation: [16]byte{5}, OperationID: [16]byte{6}, Sequence: 2}
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
	turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialIssue, 0)
	replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, command).request})
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed {
		t.Fatal("retry did not resolve the original", replay, err)
	}
	if replay.Secret.present() || replay.Value.secret.present() {
		t.Fatal("retry re-disclosed the sealed wire", replay)
	}
	usCredentialScanValue(t, "cancelled retry", replay)
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
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
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
	revokeIdentity := CommandIdentity{Incarnation: [16]byte{5}, OperationID: [16]byte{7}, Sequence: 2}

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

// A credential-born family never mints ordinary US-01 authority, even after its
// revoked source marker is drained. The security_kind=1 marker is independent.
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
	revoke := usCredentialRevokeCommandFixture(t, w, proof, command, CommandIdentity{Incarnation: [16]byte{5}, OperationID: [16]byte{6}, Sequence: 2})
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
	// A fully matching ordinary session family is denied the moment it carries
	// the credential marker, and a security_kind=1 family is denied on its own.
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET security_kind=1 WHERE id='issuer-family'`)
	turn, b := usOwnerAuthorityTurn(t, w, r)
	if a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), b); a != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("credential kind granted ordinary authority", a, err)
	}
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
