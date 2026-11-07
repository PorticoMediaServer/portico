package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usCredentialTargetRead  stmt.ID = "us.credential.target"
	usCredentialHostedRead  stmt.ID = "us.credential.hosted_link"
	usPasswordReplaceWrite  stmt.ID = "us.credential.password_replace"
	usCredentialIssueWrite  stmt.ID = "us.credential.issue"
	usCredentialRevokeRead  stmt.ID = "us.credential.revoke_current"
	usCredentialRevokeWrite stmt.ID = "us.credential.revoke"
)

func usCredentialEffectDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	pin := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, column := range columns {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
		}
		return b
	}
	point := func(table, index, column string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{column + "=?"}, AutoIndexes: []stmt.AutoIndexBinding{pin(table, index, "pk", column)}}
	}
	principalPlan := point("accounts", "sqlite_autoindex_accounts_1", "id")
	principalPlan.Indexes = append(principalPlan.Indexes, "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_direct_profiles_2")
	principalPlan.Seeks = append(principalPlan.Seeks, "account_id=?", "account_id=? AND id=?")
	principalPlan.AutoIndexes = append(principalPlan.AutoIndexes,
		pin("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
		pin("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"))
	// Full exact source-row comparison; no unbounded variable source read.
	const credentialWhere = ` WHERE id=? AND kind=? AND account_id=? AND profile_id=? AND name=? AND scopes=?
AND secret_hash=? AND hint=? AND family_id=? AND device_id=? AND created_ms=? AND expires_ms=? AND revoked_ms=? AND ended_reason=? AND parent_id IS NULL`
	const credentialColumns = `id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,revoked_ms,ended_reason`
	credentialParams := []stmt.Param{text("id"), text("kind"), text("account"), text("profile"), text("name"), text("scopes"), text("hash"), text("hint"), text("family"), text("device"), integer("created"), integer("expires"), integer("revoked"), text("ended_reason")}
	credentialBindings := []any{"us-credential", "token", "us-credential-account", "us-credential-profile", "Automation", "read,playback", usCredentialFixtureDigest, "abcdef", "us-credential-family", "us-credential-device", int64(1000), int64(0), int64(0), ""}
	defs := []stmt.Definition{
		{ID: usCredentialTargetRead, Kind: stmt.Read, SQL: `SELECT 1 FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=a.id
WHERE a.id=? AND a.epoch=? AND a.username=? AND a.must_change=?
AND m.incarnation=? AND m.revision=? AND m.disabled=?
AND p.id=? AND p.lifetime=? AND p.revision=? AND p.name=? AND p.deleted=?`,
			Params:  []stmt.Param{text("account"), integer("epoch"), text("username"), boolean("must_change"), text("member"), integer("member_revision"), boolean("disabled"), text("profile"), text("lifetime"), integer("profile_revision"), text("profile_name"), boolean("deleted")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"us-credential-account", int64(1), "credential-user", false, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", int64(1), false, "us-credential-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(1), "Credential profile", false}}}, Plan: principalPlan},
		{ID: usCredentialHostedRead, Kind: stmt.Read, SQL: `SELECT 1 FROM account_portico_links INDEXED BY sqlite_autoindex_account_portico_links_1 WHERE account_id=?`,
			Params: []stmt.Param{text("account")}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"us-credential-account"}}}, Plan: point("account_portico_links", "sqlite_autoindex_account_portico_links_1", "account_id")},
		{ID: usPasswordReplaceWrite, Kind: stmt.Write, SQL: `UPDATE accounts INDEXED BY sqlite_autoindex_accounts_1 SET password_hash=?,epoch=epoch+1
WHERE id=? AND epoch=? AND password_hash=? AND profile_id=? AND must_change=0 AND epoch>=1 AND epoch<9007199254740991
RETURNING id,profile_id,epoch,must_change,password_hash`,
			Params:  []stmt.Param{{Name: "next_hash", Type: stmt.Blob}, text("account"), integer("epoch"), {Name: "previous_hash", Type: stmt.Blob}, text("profile")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{[]byte(usCredentialFixturePassword), "us-credential-account", int64(1), []byte(usCredentialFixturePassword), "us-credential-profile"}}}, Plan: point("accounts", "sqlite_autoindex_accounts_1", "id")},
		// Deliberately stores no secret plaintext and creates no family/device.
		// This separable native row is unusable as a complete issuance command
		// until root supplies all preconditions listed below.
		{ID: usCredentialIssueWrite, Kind: stmt.Write, SQL: `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING ` + credentialColumns,
			Params: append([]stmt.Param(nil), credentialParams[:12]...), Cohorts: []stmt.Cohort{{Name: "top_level", Bindings: append([]any(nil), credentialBindings[:12]...)}}, Plan: stmt.Plan{Hot: true}},
		{ID: usCredentialRevokeRead, Kind: stmt.Read, SQL: `SELECT ` + credentialColumns + ` FROM api_credentials INDEXED BY sqlite_autoindex_api_credentials_1` + credentialWhere,
			Params: append([]stmt.Param(nil), credentialParams...), Cohorts: []stmt.Cohort{{Name: "exact_current", Bindings: append([]any(nil), credentialBindings...)}}, Plan: point("api_credentials", "sqlite_autoindex_api_credentials_1", "id")},
		{ID: usCredentialRevokeWrite, Kind: stmt.Write, SQL: `UPDATE api_credentials INDEXED BY sqlite_autoindex_api_credentials_1 SET revoked_ms=?` + credentialWhere + ` AND revoked_ms=0 RETURNING ` + credentialColumns,
			Params: append([]stmt.Param{integer("now")}, credentialParams...), Cohorts: []stmt.Cohort{{Name: "current", Bindings: append([]any{int64(2000)}, credentialBindings...)}}, Plan: point("api_credentials", "sqlite_autoindex_api_credentials_1", "id")},
	}
	for n := range defs {
		defs[n].Owner = "store/writer US credential source effects"
		defs[n].CallSites = []string{"store/writer/us_credential_statements.go"}
	}
	return defs
}

// Synthetic encoding/digest cohorts, never an actual credential or password.
const usCredentialFixturePassword = "$2a$10$....................................................."
const usCredentialFixtureDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type usPasswordPostResult struct {
	account, profile, memberIncarnation, profileLifetime string
	epoch, memberRevision, profileRevision               int64
}

// Complete actual one-row source result, never Applied or a public DTO. Raw
// times and endedReason are retained, not inferred from a later live read.
// target names and lifetimes were compared in this SAME writer turn. The
// digest/family/device are private binding facts, never event/public fields.
// Root additionally needs current family revocation+epoch validity and the
// api_credential_use.last_used_ms point fact to freeze the entire Credential
// DTO (id,kind,name,scopes,hint,createdAt,expiresAt,lastUsedAt,state,endedReason,
// member{accountId,username,profileId,profile}). AppPassword uses its existing
// id,name,username,accountId,profileId,scopes,state projection and no hint.
type usCredentialPostResult struct {
	row     usCredentialRevokeRequest
	changed bool
}

// EXACT ROOT PRECONDITIONS for composing these private effects:
//   - independently authenticate current issuer, installation, family/token,
//     authority kind/generation, member/profile lifetimes, all narrow grants,
//     PIN reentry/manage windows and request/context lifetime BEFORE disclosing
//     any saved result; original identity/digest/replay precedes fresh CAS;
//   - bind the COMPLETE frozen request, generated identities/hash/hint, secret
//     protected artifact and every proof witness to the original digest;
//   - password: independently verify current-password possession outside writer,
//     match its account/hash here, verify local sign-in policy, current second
//     factor, new-password policy and inequality. This ordinary path does not
//     authorize resets, turnOffTwoStep or cross-account/password management;
//   - issue: key is current owner self; token is current selected-profile self
//     with access_tokens enabled; app_password is explicit owner authority over
//     the chosen live account/profile. Caller role strings grant nothing. Apply
//     existing product capacity rules under separately bounded admission; these
//     one-row pieces invent no count limit or count-scan recipe;
//   - issue: atomically create/prove exactly one new family/device/token and
//     correct PIN lineage/security witnesses, horizon/expiry, current source
//     role, account/profile incarnations and device approval. Bind actual secret
//     to protectedManifest; retain owner recovery for key/app_password without
//     plaintext here. API credentials survive account password/epoch changes:
//     root must design that independent lifetime (no family epoch fan-out);
//   - revoke: prove owner/self scope for this kind, and atomically deny this
//     credential/family/device. An app-password's children must consult its
//     current parent deny/expiry; no child update/delete fan-out is supplied.
//     Repeated source revoke returns exact existing scalar time with no write;
//   - password epoch change must also fence feed grants and every dependent
//     delivery surface; it does not implement WC family revoke. Root supplies
//     any credential-state/reset cleanup with an independently bounded recipe;
//   - freeze immutable PER-PURPOSE complete native DTO/protected output in the
//     same US-001 packet; append scoped event/accounting, prove FULL admission,
//     COMMIT and current disclosure. No source reread on replay, no namespace
//     reservation used as permission to invent a receipt result schema.
//
// No function here admits/commits/ACKs a command or returns accepting authority.
func applyUSPasswordReplace(ctx context.Context, turn *commandTurn, p *usPasswordReplacePrepared) (out usPasswordPostResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if p == nil {
		return out, ErrInvalid
	}
	if err = usCredentialFrame(ctx, turn, usLocalPasswordReplace); err != nil {
		return out, err
	}
	in := p.request
	if err = usCredentialCheckTarget(ctx, turn, in.target); err != nil {
		return out, err
	}
	row, e := usCredentialQuery(ctx, turn, usCredentialHostedRead, in.target.account)
	if e != nil {
		return out, e
	}
	var present int
	if e = row.Scan(&present); e == nil {
		return out, ErrConflict
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	row, e = usCredentialQuery(ctx, turn, usPasswordReplaceWrite, in.nextHash, in.target.account, in.target.epoch, in.previousHash, in.target.profile)
	if e != nil {
		return out, e
	}
	var hash []byte
	var mustChange bool
	if e = row.Scan(&out.account, &out.profile, &out.epoch, &mustChange, &hash); e != nil {
		return out, usCredentialSourceError(e)
	}
	if out.account != in.target.account || out.profile != in.target.profile || out.epoch != in.target.epoch+1 || mustChange || !bytes.Equal(hash, in.nextHash) {
		return out, ErrResolveRequired
	}
	out.memberIncarnation, out.profileLifetime = in.target.memberIncarnation, in.target.profileLifetime
	out.memberRevision, out.profileRevision = in.target.memberRevision, in.target.profileRevision
	return out, turn.checkLifetimes(ctx)
}

func applyUSCredentialIssue(ctx context.Context, turn *commandTurn, p *usCredentialIssuePrepared) (out usCredentialPostResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if p == nil {
		return out, ErrInvalid
	}
	if err = usCredentialFrame(ctx, turn, usAPICredentialIssue); err != nil {
		return out, err
	}
	if err = usCredentialCheckTarget(ctx, turn, p.target); err != nil {
		return out, err
	}
	clock, e := usCredentialClock(turn)
	if e != nil {
		return out, e
	}
	now := clock.UnixMilli()
	// Repeat the exact requested (pre-truncation) expiry comparisons under
	// trusted SAME-TURN time. Preparation is not a lease or a fresh timestamp.
	if p.expiresMS != 0 {
		expiry := time.Unix(p.expirySeconds, int64(p.expiryNanos))
		if !expiry.After(clock.Add(time.Minute)) || expiry.After(clock.Add(10*365*24*time.Hour)) {
			return out, ErrConflict
		}
	}
	want := usCredentialRevokeRequest{target: p.target, id: p.id, kind: p.kind, name: p.name, scopes: p.scopes, secretHash: p.secretHash, hint: p.hint, family: p.family, device: p.device, createdMS: now, expiresMS: p.expiresMS}
	args := usCredentialRowBindings(want)
	row, e := usCredentialQuery(ctx, turn, usCredentialIssueWrite, args[:12]...)
	if e != nil {
		return out, e
	}
	out.row.target = p.target
	if e = scanUSCredentialPost(row, &out.row); e != nil {
		return out, e
	}
	if out.row != want {
		return out, ErrResolveRequired
	}
	out.changed = true
	return out, turn.checkLifetimes(ctx)
}

func applyUSCredentialRevoke(ctx context.Context, turn *commandTurn, p *usCredentialRevokePrepared) (out usCredentialPostResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if p == nil {
		return out, ErrInvalid
	}
	if err = usCredentialFrame(ctx, turn, usAPICredentialRevoke); err != nil {
		return out, err
	}
	in := p.request
	if err = usCredentialCheckTarget(ctx, turn, in.target); err != nil {
		return out, err
	}
	now, e := usCredentialNowMS(turn)
	if e != nil {
		return out, e
	}
	if now < in.createdMS || now < in.revokedMS {
		return out, ErrConflict
	}
	id, args := usCredentialRevokeRead, usCredentialRowBindings(in)
	want := in
	if in.revokedMS == 0 {
		id = usCredentialRevokeWrite
		args = append([]any{now}, args...)
		want.revokedMS = now
		out.changed = true
	}
	row, e := usCredentialQuery(ctx, turn, id, args...)
	if e != nil {
		return out, e
	}
	out.row.target = in.target
	if e = scanUSCredentialPost(row, &out.row); e != nil {
		return out, usCredentialSourceError(e)
	}
	if out.row != want {
		return out, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}

func usCredentialFrame(ctx context.Context, t *commandTurn, id CommandID) error {
	d, ok := describeUSCredentialCommand(id)
	if !ok || t == nil || t.transaction == nil {
		return ErrInvalid
	}
	tx := t.transaction
	if tx.descriptor != d || !tx.begun || tx.done || !tx.policyVerified || tx.settings.Synchronous != int(sqliteconn.Full) {
		return ErrInvalid
	}
	return t.checkLifetimes(ctx)
}

func usCredentialClock(t *commandTurn) (time.Time, error) {
	if t.transaction.writer == nil || t.transaction.writer.now == nil {
		return time.Time{}, ErrInvalid
	}
	now, err := t.transaction.clockTime()
	if err != nil {
		return time.Time{}, err
	}
	ms := now.UnixMilli()
	if !usCredentialTime(ms) {
		return time.Time{}, ErrInvalid
	}
	return now, nil
}

func usCredentialNowMS(t *commandTurn) (int64, error) {
	now, err := usCredentialClock(t)
	if err != nil {
		return 0, err
	}
	return now.UnixMilli(), nil
}

func usCredentialCheckTarget(ctx context.Context, t *commandTurn, in usCredentialTarget) error {
	row, err := usCredentialQuery(ctx, t, usCredentialTargetRead, in.account, in.epoch, in.username, in.mustChange, in.memberIncarnation, in.memberRevision, in.disabled, in.profile, in.profileLifetime, in.profileRevision, in.profileName, in.deleted)
	if err != nil {
		return err
	}
	var one int
	if err = row.Scan(&one); err != nil {
		return usCredentialSourceError(err)
	}
	if one != 1 {
		return ErrResolveRequired
	}
	return t.checkLifetimes(ctx)
}

func usCredentialQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, ok := t.steps[id]
	if !ok {
		return commandRow{}, invalid("us_credential_step")
	}
	return t.queryRow(ctx, step.statementStep, args...)
}

func usCredentialRowBindings(q usCredentialRevokeRequest) []any {
	return []any{q.id, q.kind, q.target.account, q.target.profile, q.name, q.scopes, q.secretHash, q.hint, q.family, q.device, q.createdMS, q.expiresMS, q.revokedMS, q.endedReason}
}

func scanUSCredentialPost(row interface{ Scan(...any) error }, q *usCredentialRevokeRequest) error {
	return row.Scan(&q.id, &q.kind, &q.target.account, &q.target.profile, &q.name, &q.scopes, &q.secretHash, &q.hint, &q.family, &q.device, &q.createdMS, &q.expiresMS, &q.revokedMS, &q.endedReason)
}

func usCredentialSourceError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
