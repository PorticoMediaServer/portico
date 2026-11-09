package writer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	usPasswordOwnerID = "security-owner"

	// Token()-sized (43-octet base64url-width) identifiers. The 520 statements
	// compare every TEXT witness inside SQLite and materialize no variable
	// source, so identifier length must never change a control decision. The
	// reviewed US-01 fixture keeps its own family/profile/device strings.
	usPasswordServerID  = "SSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSSS"
	usPasswordAccountID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	// The reviewed US-01 fixture always births its family/profile/device with
	// these fixed strings; only the server and account are caller-shaped here.
	usPasswordProfileID = "issuer-profile"
)

// Sentinel-bearing, password-shaped bcrypt images. They are synthetic
// witnesses: the sentinel text makes any accidental copy into a result,
// receipt, error, log or other source column detectable.
const (
	usPasswordPreviousHash = "$2a$10$SENTINELPREV" + "........................................."
	usPasswordNextHash     = "$2b$10$SENTINELNEXT" + "........................................."
)

// Synthetic harness only. US-01 mints the authority stamp after its own
// current-source checks; nothing here constructs a production issuer, registers
// a product command or qualifies a hold.
func usPasswordFixture(t *testing.T) (*executor, *usOwnerAuthorityProof, *usPasswordCommand) {
	t.Helper()
	return usPasswordFixtureIssuer(t, nil)
}

func usPasswordFixtureIssuer(t *testing.T, more func(*usPolicyIssuer)) (*executor, *usOwnerAuthorityProof, *usPasswordCommand) {
	t.Helper()
	w, _, proof := usOwnerAuthorityFixtureIssuer(t, func(i *usPolicyIssuer) {
		i.server, i.account = usPasswordServerID, usPasswordAccountID
		if more != nil {
			more(i)
		}
	})
	i := proof.witness.issuer
	identity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}
	request := usPasswordReplaceRequest{
		target: usCredentialTarget{account: i.account, memberIncarnation: i.memberIncarnation, profile: i.profile,
			profileLifetime: i.profileLifetime, username: i.account, profileName: "Issuer",
			epoch: 1, memberRevision: 1, profileRevision: 1},
		previousHash: []byte(usPasswordPreviousHash),
		nextHash:     []byte(usPasswordNextHash),
	}
	command, err := prepareUSPasswordCommand(identity, usPasswordOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	// The current source image is the caller's verified proof witness; it must
	// already be in place when the turn opens.
	wc008NativeExec(t, w, `UPDATE accounts SET password_hash=? WHERE id=?`, request.previousHash, i.account)
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`,
		usPasswordOwnerID, identity.Incarnation[:], i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation, i.family)
	return w, proof, command
}

func usPasswordRegistry(t *testing.T, w *executor) *stmt.Registry {
	t.Helper()
	defs := append([]stmt.Definition(nil), usAuthorityDefinitions()...)
	defs = append(defs, usPasswordDefinitions()...)
	defs = append(defs, usPasswordReceiptDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func usPasswordTurn(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, memory uint64) (*commandTurn, *usOwnerAuthorization, context.CancelFunc) {
	t.Helper()
	if memory == 0 {
		memory = 4 << 20
	}
	recipe := testRecipe()
	recipe.limit = Work{100000, 10_000_000, 100000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), usLocalPasswordReplace, recipe, &testCustody{})
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

func usPasswordInput(t *testing.T, c *usPasswordCommand) *usPasswordInvocation {
	t.Helper()
	q, err := usPasswordReceiptForCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	return &usPasswordInvocation{request: q, input: c}
}

// The statement inventory stays closed, hot and plan-captured.
func TestUSPasswordStatementCapture(t *testing.T) {
	source := usPasswordDefinitions()
	receipts := usPasswordReceiptDefinitions()
	if len(source) != 4 || len(receipts) != 10 {
		t.Fatal("password statement inventory changed", len(source), len(receipts))
	}
	defs := append(append([]stmt.Definition(nil), source...), receipts...)
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal("statement must stay hot, captured and waiver-free", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w := wc008NativeExecutor(t)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_password_statements.go", "us_password_result.go")
	if len(registry.Manifest()) != len(defs) {
		t.Fatal("registry did not bind the complete password inventory")
	}
}

// The purpose and codec stay closed: 520 only, never the credential family.
func TestUSPasswordClosedPurpose(t *testing.T) {
	if d, err := Describe(usLocalPasswordReplace); err != nil || d != (Descriptor{ID: usLocalPasswordReplace, Name: "us_local_password_replace", Durability: Full}) {
		t.Fatal(d, err)
	}
	if codec, err := usPasswordCodec(usLocalPasswordReplace); err != nil || codec != "password_replace_v1" {
		t.Fatal(codec, err)
	}
	for _, id := range []CommandID{usAPICredentialIssue, usAPICredentialRevoke, usPINSet, usPINClear, usMemberRoleChange} {
		if _, err := usPasswordCodec(id); !errors.Is(err, ErrInvalid) {
			t.Fatal("password adapter widened to", id, err)
		}
		if err := usPasswordReceiptKind(id); !errors.Is(err, ErrInvalid) {
			t.Fatal("password receipt widened to", id, err)
		}
	}
}

// Prepares nothing that the caller should have proven: identity, owner, hash
// envelope and the epoch ceiling stay closed; the writer never hashes.
func TestUSPasswordPreparationStaysOutside(t *testing.T) {
	identity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}
	base := usPasswordReplaceRequest{
		target: usCredentialTarget{account: usPasswordAccountID, memberIncarnation: strings.Repeat("a", 32),
			profile: usPasswordProfileID, profileLifetime: strings.Repeat("b", 32), username: usPasswordAccountID,
			profileName: "Issuer", epoch: 1, memberRevision: 1, profileRevision: 1},
		previousHash: []byte(usPasswordPreviousHash),
		nextHash:     []byte(usPasswordNextHash),
	}
	if _, err := prepareUSPasswordCommand(CommandIdentity{}, usPasswordOwnerID, base); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero identity", err)
	}
	if _, err := prepareUSPasswordCommand(identity, "", base); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty owner", err)
	}
	for _, mutate := range []func(*usPasswordReplaceRequest){
		func(q *usPasswordReplaceRequest) { q.nextHash = []byte("short") },
		func(q *usPasswordReplaceRequest) { q.target.epoch = usMaxRevision },
		func(q *usPasswordReplaceRequest) { q.target.mustChange = true },
		func(q *usPasswordReplaceRequest) { q.target.disabled = true },
		func(q *usPasswordReplaceRequest) { q.target.username = "" },
		func(q *usPasswordReplaceRequest) { q.target.profileLifetime = "not-hex" },
	} {
		q := base
		mutate(&q)
		if _, err := prepareUSPasswordCommand(identity, usPasswordOwnerID, q); err == nil {
			t.Fatal("preparation accepted an invalid witness")
		}
	}
	// The frozen command must redact both images in every formatting verb.
	c, err := prepareUSPasswordCommand(identity, usPasswordOwnerID, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d", "%100.2s"} {
		out := fmt.Sprintf(format, c)
		if strings.Contains(out, "SENTINEL") || strings.Contains(out, usPasswordNextHash) || strings.Contains(out, usPasswordPreviousHash) {
			t.Fatal("command leaked a hash image", format, out)
		}
	}
	if cloneUSPasswordResult(usPasswordSavedResult{}) != (usPasswordSavedResult{}) {
		t.Fatal("saved result clone")
	}
}

// The scalar CAS moves accounts.epoch once, replaces the image once and tears
// down no family, token, device or credential.
func TestUSPasswordSourceEpochAndNoFamilyTeardown(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	// The independent count oracle reads the executor pool, which the open turn
	// owns (one connection). Sample it BEFORE the frame or it waits forever.
	before := usPasswordCounts(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.previousEpoch != 1 || out.Value.resultEpoch != 2 ||
		out.Value.account != usPasswordAccountID || out.Value.profile != usPasswordProfileID ||
		out.ChargedBytes <= 0 || out.OwnerCount != 1 || out.OwnerBytes != out.ChargedBytes {
		t.Fatal("original was not the closed accepted result", out)
	}
	epoch, hash, mustChange := usPasswordAccount(t, w, usPasswordAccountID)
	if epoch != 2 || string(hash) != usPasswordNextHash || mustChange {
		t.Fatal("source replacement", epoch, mustChange)
	}
	// Credentials, families, devices and events are untouched: 520 is not an
	// F-family revoke and appends no api_event in this component.
	after := usPasswordCounts(t, w)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("password replacement tore down another family", before, after)
	}
	if n := usPasswordCount(t, w, `SELECT count(*) FROM api_events`); n != 0 {
		t.Fatal("password replacement appended an api_event", n)
	}
	if n := usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=520 AND result_count=0 AND target_account_id IS NULL AND result_revision IS NULL`); n != 1 {
		t.Fatal("receipt header shape", n)
	}
	if n := usPasswordCount(t, w, `SELECT count(*) FROM security_password_results WHERE previous_epoch=1 AND result_epoch=2`); n != 1 {
		t.Fatal("typed child shape", n)
	}
	stored, err := usPasswordResultVariableBytes(out.Value)
	if err != nil || out.ChargedBytes != stored {
		t.Fatal("charge", out.ChargedBytes, stored, err)
	}
	if n := usPasswordCount(t, w, `SELECT retained_variable_bytes FROM security_command_owners WHERE id=?`, usPasswordOwnerID); n != out.ChargedBytes {
		t.Fatal("owner debt", n)
	}
}

// A stale prepared epoch is a scalar denial before any effect; a current source
// hash change invalidates the CAS in the same frame.
func TestUSPasswordScalarEpochAndProofCAS(t *testing.T) {
	// Prepared epoch ahead of the stamped session epoch: refused, no effects.
	w, proof, command := usPasswordFixture(t)
	stale := *command
	stale.request.target.epoch = 2
	stale.digest, _ = canonicalRequestDigest(stale.kind, 1, stale.identity, stale.request)
	r := usPasswordRegistry(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	if out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, &stale)); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
		t.Fatal("stale epoch", out, err)
	}
	if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 || string(hash) != usPasswordPreviousHash {
		t.Fatal("stale epoch wrote a source effect", epoch)
	}
	if n := usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 0 {
		t.Fatal("stale epoch wrote a receipt", n)
	}

	// The current proof witness changed inside the frame: the CAS compares the
	// exact previous image and refuses.
	w, proof, command = usPasswordFixture(t)
	r = usPasswordRegistry(t, w)
	turn, a, _ = usPasswordTurn(t, w, r, proof, 0)
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE accounts SET password_hash=? WHERE id=?`, []byte(usPasswordCapturePreviousHash), usPasswordAccountID); err != nil {
		t.Fatal(err)
	}
	if out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command)); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
		t.Fatal("changed current proof", out, err)
	}
	if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 || string(hash) != usPasswordPreviousHash {
		t.Fatal("proof CAS wrote a source effect", epoch)
	}
}

// A Hosted-linked account is refused before any write: its local password is
// not this operation's target.
func TestUSPasswordHostedTargetRefused(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	wc008NativeExec(t, w, `INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,?)`, usPasswordAccountID, "hosted-account")
	r := usPasswordRegistry(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	if out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command)); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
		t.Fatal("hosted target", out, err)
	}
	if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 || string(hash) != usPasswordPreviousHash {
		t.Fatal("hosted target wrote a source effect", epoch)
	}
}

func usPasswordCount(t *testing.T, w *executor, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

func usPasswordAccount(t *testing.T, w *executor, account string) (epoch int64, hash []byte, mustChange bool) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT epoch,password_hash,must_change FROM accounts WHERE id=?`, account).Scan(&epoch, &hash, &mustChange); err != nil {
		t.Fatal(err)
	}
	return epoch, hash, mustChange
}

// usPasswordCounts is the independent oracle for "no F-family teardown": plain
// SQL counts over the current fixture, not a copy of the writer's logic.
func usPasswordCounts(t *testing.T, w *executor) map[string]int64 {
	t.Helper()
	return map[string]int64{
		"families":    usPasswordCount(t, w, `SELECT count(*) FROM authorization_session_families`),
		"tokens":      usPasswordCount(t, w, `SELECT count(*) FROM authorization_family_tokens`),
		"devices":     usPasswordCount(t, w, `SELECT count(*) FROM identity_devices`),
		"credentials": usPasswordCount(t, w, `SELECT count(*) FROM api_credentials`),
		"grants":      usPasswordCount(t, w, `SELECT count(*) FROM authorization_family_grants`),
	}
}
