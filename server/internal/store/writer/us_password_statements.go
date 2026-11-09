package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Password-only 520 source and retained-receipt statements. The complete
// credential purpose set (521/522 and their protected Secret) stays in US-11's
// us_credential_* files; this task owns only the ordinary LOCAL password
// replacement and its void public result.
//
// Nothing here hashes, verifies, generates or stores a password/secret. The
// current and next bcrypt images are prepared OUTSIDE the writer turn (proof of
// possession, new-password policy, bcrypt and entropy stay with the caller).
// Both images are comparison witnesses only: the next image is written to
// accounts.password_hash and never copied into a result, receipt, event, error
// or log. The request digest binds them one-way.
const (
	usPasswordTargetRead   stmt.ID = "us.password.target"
	usPasswordHostedRead   stmt.ID = "us.password.hosted_link"
	usPasswordPostRead     stmt.ID = "us.password.post"
	usPasswordReplaceWrite stmt.ID = "us.password.replace"

	usPasswordOwner              stmt.ID = "us.password.owner"
	usPasswordReceiptBinding     stmt.ID = "us.password.receipt.binding"
	usPasswordReceiptOperation   stmt.ID = "us.password.receipt.operation"
	usPasswordReceiptHeader      stmt.ID = "us.password.receipt.header"
	usPasswordReceiptChildOctets stmt.ID = "us.password.receipt.child_octets"
	usPasswordReceiptChild       stmt.ID = "us.password.receipt.child"
	usPasswordReceiptInsert      stmt.ID = "us.password.receipt.insert"
	usPasswordChildInsert        stmt.ID = "us.password.receipt.child_insert"
	usPasswordOwnerAdvance       stmt.ID = "us.password.receipt.advance"
	usPasswordOwnerAck           stmt.ID = "us.password.receipt.ack"
)

// Synthetic, non-secret bcrypt-shaped images used only as statement capture
// cohorts. Neither is a password, a real hash or proof of any credential.
const (
	usPasswordCapturePreviousHash = "$2a$10$....................................................."
	usPasswordCaptureNextHash     = "$2b$10$....................................................."
)

// usPasswordPrincipalPlan is the same closed accounts PK plus membership PK and
// profile unique points the merged US-01 principal check uses. No string leaves
// SQLite: every compared TEXT column is an exact owned witness in the statement.
func usPasswordPrincipalPlan() stmt.Plan {
	pin := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, column := range columns {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
		}
		return b
	}
	return stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_direct_profiles_2"},
		Seeks:   []string{"id=?", "account_id=?", "account_id=? AND id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			pin("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			pin("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
			pin("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
		}}
}

// usPasswordDefinitions is the closed 520 source-effect set.
func usPasswordDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	principalPlan := usPasswordPrincipalPlan()
	defs := []stmt.Definition{
		// The exact prepared target is compared to current source in SQL. The
		// statement returns only an integer, so no unbounded TEXT/BLOB source is
		// materialized and there is no partial octet preflight to under-count.
		{ID: usPasswordTargetRead, Kind: stmt.Read, SQL: `SELECT 1 FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=a.id
WHERE a.id=? AND a.epoch=? AND a.username=? AND a.must_change=?
AND m.incarnation=? AND m.revision=? AND m.disabled=?
AND p.id=? AND p.lifetime=? AND p.revision=? AND p.name=? AND p.deleted=?`,
			Params:  []stmt.Param{text("account"), integer("epoch"), text("username"), boolean("must_change"), text("member"), integer("member_revision"), boolean("disabled"), text("profile"), text("lifetime"), integer("profile_revision"), text("profile_name"), boolean("deleted")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"issuer", int64(1), "issuer", false, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", int64(1), false, "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(1), "Issuer", false}}}, Plan: principalPlan},
		// A Hosted-linked account signs in through Portico, so its local password
		// is not this operation's target (direct_accounts.go passwordless check).
		{ID: usPasswordHostedRead, Kind: stmt.Read, SQL: `SELECT 1 FROM account_portico_links INDEXED BY sqlite_autoindex_account_portico_links_1 WHERE account_id=?`,
			Params: []stmt.Param{text("account")}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"issuer"}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_account_portico_links_1"}, Seeks: []string{"account_id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "account_portico_links", Name: "sqlite_autoindex_account_portico_links_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}}}},
		// Accounts PK epoch CAS. The previous hash is an independent proof
		// witness; the new image is written once. No family, token, device or
		// credential row is touched here: WC owns explicit session revocation.
		// The epoch predicate is a real barrier on its own, not just a shadow of
		// usPasswordTargetRead's a.epoch=?: TestUSPasswordReplaceCASRequiresStampedEpoch
		// runs this statement directly with a stale epoch and pins the zero rows.
		{ID: usPasswordReplaceWrite, Kind: stmt.Write, SQL: `UPDATE accounts INDEXED BY sqlite_autoindex_accounts_1 SET password_hash=?,epoch=epoch+1
WHERE id=? AND epoch=? AND password_hash=? AND profile_id=? AND must_change=0 AND epoch>=1 AND epoch<9007199254740991
RETURNING id,profile_id,epoch,must_change,password_hash`,
			Params:  []stmt.Param{{Name: "next_hash", Type: stmt.Blob}, text("account"), integer("epoch"), {Name: "previous_hash", Type: stmt.Blob}, text("profile")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{[]byte(usPasswordCaptureNextHash), "issuer", int64(1), []byte(usPasswordCapturePreviousHash), "issuer-profile"}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_accounts_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "accounts", Name: "sqlite_autoindex_accounts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}},
		// Post-effect release read. It proves the frame's committed scalar state
		// is exactly the expected one; it copies the hash only to compare it and
		// never formats, stores or returns it.
		{ID: usPasswordPostRead, Kind: stmt.Read, SQL: `SELECT epoch,password_hash,profile_id,must_change FROM accounts INDEXED BY sqlite_autoindex_accounts_1 WHERE id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"issuer"}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_accounts_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "accounts", Name: "sqlite_autoindex_accounts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer US password source effects"
		defs[n].CallSites = []string{"store/writer/us_password_statements.go"}
	}
	return defs
}

// usPasswordReceiptDefinitions is the closed 520 retained-result set: the W
// receipt header, the typed security_password_results child and the owner
// count/byte debt. None of these opens, commits, authenticates, registers an
// accepting command or mints a grant.
func usPasswordReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	bind := []any{"security-owner", inc, int64(1)}
	ownerPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_owners_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "security_command_owners", Name: "sqlite_autoindex_security_command_owners_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	operationPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_receipts_2"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND operation_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "security_command_receipts", Name: "sqlite_autoindex_security_command_receipts_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "owner_id", Collation: "BINARY"}, {Name: "command_incarnation", Collation: "BINARY"}, {Name: "operation_id", Collation: "BINARY"}}}}}
	headerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_command_receipts"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	childPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_password_results"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	defs := []stmt.Definition{
		{ID: usPasswordOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,original_family_id,last_sequence,acknowledged_sequence,reclaimed_sequence,retained_count,retained_variable_bytes,retired_ms
FROM security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1
WHERE id=? AND ingress='authenticated' AND server_id=? AND issuer_account_id=? AND issuer_member_incarnation=? AND issuer_profile_id=? AND issuer_profile_lifetime=? AND issuer_installation_incarnation=?`,
			Params:  []stmt.Param{t("owner"), t("server"), t("account"), t("member"), t("profile"), t("lifetime"), t("installation")},
			Cohorts: []stmt.Cohort{{Name: "original_scope", Bindings: []any{"security-owner", "server", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"}}}, Plan: ownerPlan},
		{ID: usPasswordReceiptOperation, Kind: stmt.Read, SQL: `SELECT command_sequence FROM security_command_receipts INDEXED BY sqlite_autoindex_security_command_receipts_2 WHERE owner_id=? AND command_incarnation=? AND operation_id=? LIMIT 2`,
			Params: []stmt.Param{t("owner"), b("incarnation"), b("operation")}, Cohorts: []stmt.Cohort{{Name: "original_operation", Bindings: []any{"security-owner", inc, op}}}, Plan: operationPlan},
		{ID: usPasswordReceiptBinding, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original_binding", Bindings: bind}}, Plan: headerPlan},
		// 520 has a void public response and no target/revision/body columns. The
		// NON-null fields prove that shape rather than trusting the caller.
		{ID: usPasswordReceiptHeader, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,
 target_account_id,target_member_incarnation,result_revision,result_count,variable_bytes
FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: headerPlan},
		{ID: usPasswordReceiptChildOctets, Kind: stmt.Read, SQL: `SELECT variable_bytes FROM security_password_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original_child", Bindings: bind}}, Plan: childPlan},
		{ID: usPasswordReceiptChild, Kind: stmt.Read, SQL: `SELECT account_id,member_incarnation,profile_id,profile_lifetime,member_revision,profile_revision,previous_epoch,result_epoch
FROM security_password_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original_child", Bindings: bind}}, Plan: childPlan},
		{ID: usPasswordReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count)
VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied_ms"), b("result"), i("result_count")},
			Cohorts: []stmt.Cohort{{Name: "void_password_result", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(520), int64(1), "password_replace_v1", int64(1000), digest, int64(0)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usPasswordChildInsert, Kind: stmt.Write, SQL: `INSERT INTO security_password_results(owner_id,command_incarnation,command_sequence,account_id,member_incarnation,profile_id,profile_lifetime,member_revision,profile_revision,previous_epoch,result_epoch)
VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("account"), t("member"), t("profile"), t("lifetime"), i("member_revision"), i("profile_revision"), i("previous_epoch"), i("result_epoch")},
			Cohorts: []stmt.Cohort{{Name: "void_password_child", Bindings: []any{"security-owner", inc, int64(1), "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(1), int64(1), int64(1), int64(2)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usPasswordOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET last_sequence=?,retained_count=retained_count+1,retained_variable_bytes=retained_variable_bytes+?
WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND retained_count=? AND retained_variable_bytes=? AND retired_ms=0
RETURNING last_sequence,retained_count,retained_variable_bytes`,
			Params:  []stmt.Param{i("next"), i("charged_bytes"), t("owner"), b("incarnation"), i("last"), i("ack"), i("count"), i("bytes")},
			Cohorts: []stmt.Cohort{{Name: "advance", Bindings: []any{int64(1), int64(320), "security-owner", inc, int64(0), int64(0), int64(0), int64(0)}}}, Plan: ownerPlan},
		{ID: usPasswordOwnerAck, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET acknowledged_sequence=? WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? RETURNING acknowledged_sequence`,
			Params:  []stmt.Param{i("next"), t("owner"), b("incarnation"), i("last"), i("ack")},
			Cohorts: []stmt.Cohort{{Name: "ack", Bindings: []any{int64(1), "security-owner", inc, int64(1), int64(0)}}}, Plan: ownerPlan},
	}
	for n := range defs {
		defs[n].Owner = "store/writer US password receipts"
		defs[n].CallSites = []string{"store/writer/us_password_statements.go"}
	}
	return defs
}

// usPasswordPostResult is the complete actual source post-state of one
// replacement, never Applied or a public DTO. It carries no hash image.
type usPasswordPostResult struct {
	account, profile, memberIncarnation, profileLifetime string
	epoch, memberRevision, profileRevision               int64
}

// applyUSPasswordReplace performs the ordinary LOCAL password source
// replacement for the CURRENT owner account. Password proof/possession, new
// password policy, bcrypt and entropy stay outside the writer turn: the
// prepared request supplies the already generated next image and the
// independently verified previous image as comparison witnesses only.
//
// Deliberately absent: reset-file/member-reset/factor recipes, cross-account
// management, credential issue/revoke and any F-family teardown. A password
// epoch change denies stale sessions scalar-wise (accounts PK epoch CAS and the
// US-01 current-epoch recheck); WC owns explicit family revocation.
func applyUSPasswordReplace(ctx context.Context, turn *commandTurn, c *usPasswordCommand) (out usPasswordPostResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil || c.kind != usLocalPasswordReplace {
		return out, ErrInvalid
	}
	if err = usPasswordFrame(ctx, turn, usLocalPasswordReplace); err != nil {
		return out, err
	}
	in := c.request
	if err = usPasswordCheckTarget(ctx, turn, in.target); err != nil {
		return out, err
	}
	row, e := usPasswordQuery(ctx, turn, usPasswordReplaceWrite, in.nextHash, in.target.account, in.target.epoch, in.previousHash, in.target.profile)
	if e != nil {
		return out, e
	}
	var hash []byte
	var mustChange bool
	if e = row.Scan(&out.account, &out.profile, &out.epoch, &mustChange, &hash); e != nil {
		return out, usPasswordSourceError(e)
	}
	if out.account != in.target.account || out.profile != in.target.profile || out.epoch != in.target.epoch+1 || mustChange || !bytes.Equal(hash, in.nextHash) {
		return out, ErrResolveRequired
	}
	out.memberIncarnation, out.profileLifetime = in.target.memberIncarnation, in.target.profileLifetime
	out.memberRevision, out.profileRevision = in.target.memberRevision, in.target.profileRevision
	return out, turn.checkLifetimes(ctx)
}

// usPasswordFrame requires the caller's already admitted FULL private turn and
// the exact 520 descriptor. It grants no admission and opens no second ledger.
func usPasswordFrame(ctx context.Context, t *commandTurn, id CommandID) error {
	d, ok := describeUSCredentialCommand(id)
	if !ok || id != usLocalPasswordReplace || t == nil || t.transaction == nil {
		return ErrInvalid
	}
	tx := t.transaction
	if tx.descriptor != d || !tx.begun || tx.done || !tx.policyVerified || tx.settings.Synchronous != int(sqliteconn.Full) {
		return ErrInvalid
	}
	return t.checkLifetimes(ctx)
}

// usPasswordCheckTarget re-reads the exact prepared source target and refuses a
// Hosted-linked account before any write. Every TEXT value is compared inside
// SQLite, so no variable source is copied before resource admission.
func usPasswordCheckTarget(ctx context.Context, t *commandTurn, in usCredentialTarget) error {
	row, err := usPasswordQuery(ctx, t, usPasswordTargetRead, in.account, in.epoch, in.username, in.mustChange, in.memberIncarnation, in.memberRevision, in.disabled, in.profile, in.profileLifetime, in.profileRevision, in.profileName, in.deleted)
	if err != nil {
		return err
	}
	var one int64
	if err = row.Scan(&one); err != nil {
		return usPasswordSourceError(err)
	}
	if one != 1 {
		return ErrResolveRequired
	}
	hosted, err := usPasswordQuery(ctx, t, usPasswordHostedRead, in.account)
	if err != nil {
		return err
	}
	var present int64
	switch scan := hosted.Scan(&present); {
	case scan == nil:
		// A Hosted-linked account is not the local-password target.
		return ErrConflict
	case errors.Is(scan, sql.ErrNoRows):
	default:
		return scan
	}
	return t.checkLifetimes(ctx)
}

// verifyUSPasswordPostState is the 520 release check. A fresh replacement is
// the ONE US mutation that advances the very accounts epoch US-01 binds in its
// current-source stamp, so no post-effect US-01 recheck can succeed and the
// packet forbids an F-family epoch change. The last current-source authority
// check therefore runs immediately BEFORE the scalar effect (inside the receipt
// store), and the release proves the exact expected post-effect state instead:
// the same account, the prepared profile, epoch+1, must_change=0 and the
// prepared next image. It writes nothing and discloses nothing.
func verifyUSPasswordPostState(ctx context.Context, turn *commandTurn, c *usPasswordCommand, v usPasswordSavedResult) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil || c == nil || c.kind != usLocalPasswordReplace || v.kind != usLocalPasswordReplace {
		return ErrInvalid
	}
	row, e := usPasswordQuery(ctx, turn, usPasswordPostRead, v.account)
	if e != nil {
		return e
	}
	var epoch int64
	var hash []byte
	var profile string
	var mustChange bool
	if e = row.Scan(&epoch, &hash, &profile, &mustChange); e != nil {
		return usPasswordSourceError(e)
	}
	if epoch != v.resultEpoch || profile != v.profile || mustChange || !bytes.Equal(hash, c.request.nextHash) {
		return ErrResolveRequired
	}
	return turn.checkLifetimes(ctx)
}

func usPasswordQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return commandRow{}, invalid("us_password_statement")
	}
	return t.queryRow(ctx, step.statementStep, args...)
}

func usPasswordRows(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	if t == nil {
		return nil, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return nil, invalid("us_password_statement")
	}
	return t.query(ctx, step.statementStep, args...)
}

func usPasswordSourceError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
