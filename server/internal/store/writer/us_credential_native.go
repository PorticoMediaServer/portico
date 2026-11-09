package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The COMPLETE private 521/522 composition: top-level API-credential issue and
// revoke with an immutable protected-result receipt and retained quota. It is a
// private test/harness composition: no public Writer method, no product caller,
// no exported constructor and no acceptance flow. The harness supplies an
// already begun FULL private turn; this file owns original/replay/ACK
// classification, the receipt, the retained quota, the exact source effect and
// the physical terminal.
//
// Copied from US-07's dispatcher/receipt skeleton and US-10's owner custody, but
// NOT US-10's receipt-first order: 521/522 do not advance accounts.epoch, so the
// US-01 current-source stamp stays valid across the effect and the release runs
// US-07's POST-EFFECT recheck. Only the receipt-first exception for a command
// that advances the US-01 epoch is absent here.
//
// Credential semantics settled here:
//   - api_credentials keeps its own hash/PK/family UNIQUE source shape. The
//     issued row is TOP LEVEL only: parent_id stays NULL and this component never
//     scans or updates descendants.
//   - The credential family is born with security_kind=1 through the merged
//     family-birth helper and inherits the issuer lineage. That marker is what
//     keeps a credential-born family out of ordinary US-01 session authority,
//     even after a revoked source marker is drained.
//   - Scopes are the four bounded existing scopes in canonical order, validated
//     by the merged preparation; no range-based permission is invented.
//   - AEAD, key generation and entropy stay OUTSIDE the writer turn. Only the
//     sealed artifact (key id, nonce, body, AAD digest) and the one-way hash
//     enter; key/app-password keep current owner-recovery custody in
//     security_credential_secrets, while a token has no recoverable source
//     secret. The sealed artifact is a FIRST-RESPONSE-ONLY value in the dispatch
//     result: replay and revoke never project it again.
//
// Password material, plaintext credentials and raw entropy never enter any
// output, receipt, event, error or log from this file.

const (
	// Retained quota of ONE security owner: at most 64 originals and 1 MiB of
	// variable receipt bytes. A fresh result that would pass either bound is
	// refused with RefusalCapacity BEFORE any source effect. These are exact
	// counters, not a product user cap; US-26 reclaim refunds actual bytes.
	usCredentialOwnerReceipts     int64 = 64
	usCredentialOwnerReceiptBytes int64 = 1 << 20

	// Three dispatcher-owned rechecks: the entry recheck before any owner or
	// payload read, the scoped owner re-read that settles retirement/quota, and
	// the final current disclosure before the terminal.
	usCredentialDispatchRechecks int64 = 3
	usCredentialRecheckBytes     int64 = 64 << 10
)

const (
	usCredentialTargetRead   stmt.ID = "us.credential.target"
	usCredentialHostedRead   stmt.ID = "us.credential.hosted_link"
	usCredentialSourceInsert stmt.ID = "us.credential.source.insert"
	usCredentialRevokeRead   stmt.ID = "us.credential.source.revoke_read"
	usCredentialRevokeWrite  stmt.ID = "us.credential.source.revoke_write"
	usCredentialFamilyRead   stmt.ID = "us.credential.family.read"
	usCredentialSecretInsert stmt.ID = "us.credential.secret.insert"
)

const (
	usCredentialOwner              stmt.ID = "us.credential.owner"
	usCredentialReceiptOperation   stmt.ID = "us.credential.receipt.operation"
	usCredentialReceiptBinding     stmt.ID = "us.credential.receipt.binding"
	usCredentialReceiptHeader      stmt.ID = "us.credential.receipt.header"
	usCredentialResultOctets       stmt.ID = "us.credential.receipt.result_octets"
	usCredentialScopeOctets        stmt.ID = "us.credential.receipt.scope_octets"
	usCredentialResultRead         stmt.ID = "us.credential.receipt.result"
	usCredentialScopeRead          stmt.ID = "us.credential.receipt.scope"
	usCredentialReceiptInsert      stmt.ID = "us.credential.receipt.insert"
	usCredentialResultInsert       stmt.ID = "us.credential.receipt.result_insert"
	usCredentialScopeInsert        stmt.ID = "us.credential.receipt.scope_insert"
	usCredentialOwnerAdvance       stmt.ID = "us.credential.receipt.advance"
	usCredentialOwnerAck           stmt.ID = "us.credential.receipt.ack"
	usCredentialCaptureSecretKeyID         = "credential-key-id"
)

// Synthetic capture cohorts, never an actual credential or secret.
const (
	usCredentialCaptureHash     = "usH7UQVwFRaZZhPvF0OTB2ixxUX6nBUUo6aoJ9dWG4s"
	usCredentialCaptureSecretID = "us-credential"
)

func usCredentialPoint(table, index, origin string, columns ...string) stmt.AutoIndexBinding {
	b := stmt.AutoIndexBinding{Table: table, Name: index, Origin: origin}
	for _, column := range columns {
		b.Columns = append(b.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return b
}

func usCredentialPrincipalPlan() stmt.Plan {
	return stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_direct_profiles_2"},
		Seeks:   []string{"id=?", "account_id=?", "account_id=? AND id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usCredentialPoint("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			usCredentialPoint("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
			usCredentialPoint("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
		}}
}

// usCredentialDefinitions is the closed 521/522 source-effect set.
func usCredentialDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	principalPlan := usCredentialPrincipalPlan()
	credentialColumns := `id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,revoked_ms,ended_reason,last_used_ms`
	credentialWhere := ` WHERE id=? AND kind=? AND account_id=? AND profile_id=? AND name=? AND scopes=?
AND secret_hash=? AND hint=? AND family_id=? AND device_id=? AND created_ms=? AND expires_ms=? AND revoked_ms=? AND ended_reason=? AND parent_id IS NULL`
	credentialParams := []stmt.Param{text("id"), text("kind"), text("account"), text("profile"), text("name"), text("scopes"), text("hash"), text("hint"), text("family"), text("device"), integer("created"), integer("expires"), integer("revoked"), text("ended_reason")}
	credentialBindings := []any{usCredentialCaptureSecretID, "token", "us-credential-account", "us-credential-profile", "Automation", "read,playback", usCredentialCaptureHash, "abcdef", "us-credential-family", "us-credential-device", int64(1000), int64(0), int64(0), ""}
	defs := []stmt.Definition{
		// The exact prepared target is compared to current source in SQL. The
		// statement returns only an integer, so no variable source is copied
		// before admission and there is no partial octet preflight to under-count.
		{ID: usCredentialTargetRead, Kind: stmt.Read, SQL: `SELECT 1 FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=a.id
WHERE a.id=? AND a.epoch=? AND a.username=? AND a.must_change=?
AND m.incarnation=? AND m.revision=? AND m.disabled=?
AND p.id=? AND p.lifetime=? AND p.revision=? AND p.name=? AND p.deleted=?`,
			Params:  []stmt.Param{text("account"), integer("epoch"), text("username"), boolean("must_change"), text("member"), integer("member_revision"), boolean("disabled"), text("profile"), text("lifetime"), integer("profile_revision"), text("profile_name"), boolean("deleted")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"us-credential-account", int64(1), "credential-user", false, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", int64(1), false, "us-credential-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(1), "Credential profile", false}}}, Plan: principalPlan},
		// A Hosted-linked account signs in through Portico, so its local API
		// credential is not this operation's target.
		{ID: usCredentialHostedRead, Kind: stmt.Read, SQL: `SELECT 1 FROM account_portico_links INDEXED BY sqlite_autoindex_account_portico_links_1 WHERE account_id=?`,
			Params: []stmt.Param{text("account")}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"us-credential-account"}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_account_portico_links_1"}, Seeks: []string{"account_id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{usCredentialPoint("account_portico_links", "sqlite_autoindex_account_portico_links_1", "pk", "account_id")}}},
		// One TOP-LEVEL source row. parent_id is deliberately absent (NULL) and
		// secret stays the empty local-password sentinel: no plaintext is stored
		// and no descendant is created.
		{ID: usCredentialSourceInsert, Kind: stmt.Write, SQL: `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING ` + credentialColumns,
			Params: append([]stmt.Param(nil), credentialParams[:12]...), Cohorts: []stmt.Cohort{{Name: "top_level", Bindings: append([]any(nil), credentialBindings[:12]...)}}, Plan: stmt.Plan{Hot: true}},
		// Already-revoked exact current row: repeated source revoke observes the
		// captured scalar time and writes nothing.
		{ID: usCredentialRevokeRead, Kind: stmt.Read, SQL: `SELECT ` + credentialColumns + ` FROM api_credentials INDEXED BY sqlite_autoindex_api_credentials_1` + credentialWhere,
			Params: append([]stmt.Param(nil), credentialParams...), Cohorts: []stmt.Cohort{{Name: "exact_current", Bindings: append([]any(nil), credentialBindings...)}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_api_credentials_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{usCredentialPoint("api_credentials", "sqlite_autoindex_api_credentials_1", "pk", "id")}}},
		// Top-level revoke only: the row must still be live and an exact current
		// match. There is no child/descendant UPDATE and no family fan-out.
		{ID: usCredentialRevokeWrite, Kind: stmt.Write, SQL: `UPDATE api_credentials INDEXED BY sqlite_autoindex_api_credentials_1 SET revoked_ms=?` + credentialWhere + ` AND revoked_ms=0 RETURNING ` + credentialColumns,
			Params:  append([]stmt.Param{integer("now")}, credentialParams...),
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: append([]any{int64(2000)}, credentialBindings...)}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_api_credentials_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{usCredentialPoint("api_credentials", "sqlite_autoindex_api_credentials_1", "pk", "id")}}},
		// The captured family incarnation is current source evidence for the
		// immutable result. The credential family MUST stay security_kind=1.
		{ID: usCredentialFamilyRead, Kind: stmt.Read, SQL: `SELECT command_incarnation FROM authorization_session_families INDEXED BY sqlite_autoindex_authorization_session_families_1 WHERE id=? AND account_id=? AND profile_id=? AND security_kind=1`,
			Params:  []stmt.Param{text("family"), text("account"), text("profile")},
			Cohorts: []stmt.Cohort{{Name: "credential_family", Bindings: []any{"us-credential-family", "us-credential-account", "us-credential-profile"}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}, Seeks: []string{"id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{usCredentialPoint("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id")}}},
		// Current owner-recovery custody for key/app-password. A token has no
		// recoverable source secret and never reaches this statement.
		{ID: usCredentialSecretInsert, Kind: stmt.Write, SQL: `INSERT INTO security_credential_secrets(credential_id,kind,account_id,member_incarnation,profile_id,profile_lifetime,family_id,family_incarnation,secret_hash,key_id,nonce,body,aad_digest)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{text("credential"), text("kind"), text("account"), text("member"), text("profile"), text("lifetime"), text("family"), {Name: "family_incarnation", Type: stmt.Blob}, text("hash"), text("key_id"), {Name: "nonce", Type: stmt.Blob}, {Name: "body", Type: stmt.Blob}, {Name: "aad", Type: stmt.Blob}},
			Cohorts: []stmt.Cohort{{Name: "sealed_key", Bindings: []any{usCredentialCaptureSecretID, "key", "us-credential-account", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "us-credential-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "us-credential-family", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, usCredentialCaptureHash, "key-id", make([]byte, 12), make([]byte, 62), make([]byte, 32)}}},
			Plan:    stmt.Plan{Hot: true}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer US credential source effects"
		defs[n].CallSites = []string{"store/writer/us_credential_native.go"}
	}
	return defs
}

// usCredentialReceiptDefinitions is the closed retained-result set: the W
// receipt header, the typed security_credential_results child, its ordered
// scope cohort and the owner count/byte debt. None of these opens, commits,
// authenticates, registers an accepting command or mints a grant.
func usCredentialReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nullableText := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	nullableBlob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	bind := []any{"security-owner", inc, int64(1)}
	ownerPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_owners_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usCredentialPoint("security_command_owners", "sqlite_autoindex_security_command_owners_1", "pk", "id")}}
	operationPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_receipts_2"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND operation_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usCredentialPoint("security_command_receipts", "sqlite_autoindex_security_command_receipts_2", "u", "owner_id", "command_incarnation", "operation_id")}}
	headerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_command_receipts"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	resultPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_credential_results"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	scopePlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_credential_result_scopes"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=?"}}
	defs := []stmt.Definition{
		{ID: usCredentialOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,original_family_id,last_sequence,acknowledged_sequence,reclaimed_sequence,retained_count,retained_variable_bytes,retired_ms
FROM security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1
WHERE id=? AND ingress='authenticated' AND server_id=? AND issuer_account_id=? AND issuer_member_incarnation=? AND issuer_profile_id=? AND issuer_profile_lifetime=? AND issuer_installation_incarnation=?`,
			Params:  []stmt.Param{t("owner"), t("server"), t("account"), t("member"), t("profile"), t("lifetime"), t("installation")},
			Cohorts: []stmt.Cohort{{Name: "original_scope", Bindings: []any{"security-owner", "server", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"}}}, Plan: ownerPlan},
		{ID: usCredentialReceiptOperation, Kind: stmt.Read, SQL: `SELECT command_sequence FROM security_command_receipts INDEXED BY sqlite_autoindex_security_command_receipts_2 WHERE owner_id=? AND command_incarnation=? AND operation_id=? LIMIT 2`,
			Params: []stmt.Param{t("owner"), b("incarnation"), b("operation")}, Cohorts: []stmt.Cohort{{Name: "original_operation", Bindings: []any{"security-owner", inc, op}}}, Plan: operationPlan},
		{ID: usCredentialReceiptBinding, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original_binding", Bindings: bind}}, Plan: headerPlan},
		// The credential result has no receipt target/revision/body columns; the
		// immutable payload lives in the typed child. Every other nullable field
		// must stay NULL.
		{ID: usCredentialReceiptHeader, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,
 target_account_id,target_member_incarnation,target_profile_id,target_profile_lifetime,result_revision,policy_inherits,result_changed,result_pin_revision,result_pin_required,result_count,result_key_id,result_nonce,result_body,variable_bytes
FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: headerPlan},
		{ID: usCredentialResultOctets, Kind: stmt.Read, SQL: `SELECT scope_count,variable_bytes FROM security_credential_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original_result", Bindings: bind}}, Plan: resultPlan},
		{ID: usCredentialScopeOctets, Kind: stmt.Read, SQL: `SELECT ordinal,variable_bytes FROM security_credential_result_scopes WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param(nil), key...), i("limit")), Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(5))}}, Plan: scopePlan},
		{ID: usCredentialResultRead, Kind: stmt.Read, SQL: `SELECT credential_id,kind,name,hint,state,ended_reason,account_id,member_incarnation,username,profile_id,profile_lifetime,profile_name,family_id,family_incarnation,device_id,secret_hash,created_ms,expires_ms,last_used_ms,revoked_ms,secret_key_id,secret_nonce,secret_body,secret_aad_digest,scope_count,variable_bytes
FROM security_credential_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original_result", Bindings: bind}}, Plan: resultPlan},
		{ID: usCredentialScopeRead, Kind: stmt.Read, SQL: `SELECT ordinal,scope FROM security_credential_result_scopes WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param(nil), key...), i("limit")), Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(5))}}, Plan: scopePlan},
		{ID: usCredentialReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count)
VALUES(?,?,?,?,?,?,?,?,?,?,0) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied_ms"), b("result")},
			Cohorts: []stmt.Cohort{{Name: "credential_result", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(521), int64(1), "credential_issue_v1", int64(1000), digest}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usCredentialResultInsert, Kind: stmt.Write, SQL: `INSERT INTO security_credential_results(owner_id,command_incarnation,command_sequence,command_kind,credential_id,kind,name,scope_count,hint,created_ms,expires_ms,last_used_ms,revoked_ms,state,ended_reason,account_id,member_incarnation,username,profile_id,profile_lifetime,profile_name,family_id,family_incarnation,device_id,secret_hash,secret_key_id,secret_nonce,secret_body,secret_aad_digest)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), i("kind"), t("credential"), t("credential_kind"), t("name"), i("scope_count"), t("hint"), i("created"), i("expires"), i("last_used"), i("revoked"), t("state"), t("ended_reason"), t("account"), t("member"), t("username"), t("profile"), t("lifetime"), t("profile_name"), t("family"), b("family_incarnation"), t("device"), t("hash"), nullableText("key_id"), nullableBlob("nonce"), nullableBlob("body"), nullableBlob("aad")},
			Cohorts: []stmt.Cohort{{Name: "issued", Bindings: []any{"security-owner", inc, int64(1), int64(521), "us-credential", "token", "Automation", int64(2), "abcdef", int64(1000), int64(0), int64(0), int64(0), "active", "", "us-credential-account", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "credential-user", "us-credential-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "Credential profile", "us-credential-family", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, "us-credential-device", usCredentialCaptureHash, "key-id", make([]byte, 12), make([]byte, 63), make([]byte, 32)}}},
			Plan:    stmt.Plan{Hot: true}},
		{ID: usCredentialScopeInsert, Kind: stmt.Write, SQL: `INSERT INTO security_credential_result_scopes(owner_id,command_incarnation,command_sequence,ordinal,scope) VALUES(?,?,?,?,?) RETURNING variable_bytes`,
			Params: append(append([]stmt.Param(nil), key...), i("ordinal"), t("scope")), Cohorts: []stmt.Cohort{{Name: "one_scope", Bindings: append(append([]any(nil), bind...), int64(0), "read")}}, Plan: stmt.Plan{Hot: true}},
		{ID: usCredentialOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET last_sequence=?,retained_count=retained_count+1,retained_variable_bytes=retained_variable_bytes+?
WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND retained_count=? AND retained_variable_bytes=? AND retired_ms=0
RETURNING last_sequence,retained_count,retained_variable_bytes`,
			Params:  []stmt.Param{i("next"), i("charged_bytes"), t("owner"), b("incarnation"), i("last"), i("ack"), i("count"), i("bytes")},
			Cohorts: []stmt.Cohort{{Name: "advance", Bindings: []any{int64(1), int64(320), "security-owner", inc, int64(0), int64(0), int64(0), int64(0)}}}, Plan: ownerPlan},
		{ID: usCredentialOwnerAck, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET acknowledged_sequence=? WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? RETURNING acknowledged_sequence`,
			Params:  []stmt.Param{i("next"), t("owner"), b("incarnation"), i("last"), i("ack")},
			Cohorts: []stmt.Cohort{{Name: "ack", Bindings: []any{int64(1), "security-owner", inc, int64(1), int64(0)}}}, Plan: ownerPlan},
	}
	for n := range defs {
		defs[n].Owner = "store/writer US credential receipts"
		defs[n].CallSites = []string{"store/writer/us_credential_native.go"}
	}
	return defs
}

// usCredentialIssueJob is one frozen top-level issue. The sealed artifact and
// the family-birth input are frozen with the merged preparation; both are
// revalidated before any source effect.
type usCredentialIssueJob struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usCredentialIssuePrepared
	birth    usFamilyBirthInput
	secret   usCredentialResultSecret
}

type usCredentialRevokeJob struct {
	ownerID  string
	issuer   usPolicyIssuer
	prepared usCredentialRevokePrepared
}

func (usCredentialIssueJob) String() string   { return "usCredentialIssueJob{redacted}" }
func (usCredentialIssueJob) GoString() string { return "usCredentialIssueJob{redacted}" }
func (usCredentialIssueJob) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialIssueJob")
}

func (usCredentialRevokeJob) String() string   { return "usCredentialRevokeJob{redacted}" }
func (usCredentialRevokeJob) GoString() string { return "usCredentialRevokeJob{redacted}" }
func (usCredentialRevokeJob) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialRevokeJob")
}

type usCredentialNativeCommand struct {
	kind     CommandID
	owner    string
	identity CommandIdentity
	digest   RequestDigest
	issue    *usCredentialIssueJob
	revoke   *usCredentialRevokeJob
}

func (usCredentialNativeCommand) String() string   { return "usCredentialNativeCommand{redacted}" }
func (usCredentialNativeCommand) GoString() string { return "usCredentialNativeCommand{redacted}" }
func (usCredentialNativeCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialNativeCommand")
}

func cloneUSCredentialSecret(v usCredentialResultSecret) usCredentialResultSecret {
	out := usCredentialResultSecret{}
	if v.keyID != nil {
		s := strings.Clone(*v.keyID)
		out.keyID = &s
	}
	out.nonce = append([]byte(nil), v.nonce...)
	out.body = append([]byte(nil), v.body...)
	out.aadDigest = append([]byte(nil), v.aadDigest...)
	return out
}

func validUSCredentialSecret(v usCredentialResultSecret, kind string) bool {
	width := 0
	switch kind {
	case "key":
		width = 62
	case "token":
		width = 63
	case "app_password":
		width = 64
	default:
		return false
	}
	return v.keyID != nil && len(*v.keyID) >= 1 && len(*v.keyID) <= 128 && len(v.nonce) == 12 && len(v.body) == width && len(v.aadDigest) == 32
}

func usCredentialIssueJobOf(c *usCredentialNativeCommand) (*usCredentialIssueJob, error) {
	if c == nil || c.kind != usAPICredentialIssue || c.issue == nil || c.revoke != nil || c.owner != c.issue.ownerID {
		return nil, ErrInvalid
	}
	return c.issue, nil
}

func usCredentialRevokeJobOf(c *usCredentialNativeCommand) (*usCredentialRevokeJob, error) {
	if c == nil || c.kind != usAPICredentialRevoke || c.revoke == nil || c.issue != nil || c.owner != c.revoke.ownerID {
		return nil, ErrInvalid
	}
	return c.revoke, nil
}

// prepareUSCredentialNativeIssue freezes one top-level issue. It hashes,
// verifies and encrypts nothing: the caller has already proven possession and
// generated/sealed the credential outside the writer turn.
func prepareUSCredentialNativeIssue(identity CommandIdentity, owner string, issuer usPolicyIssuer, in usCredentialIssueRequest, birth usFamilyBirthInput, secret usCredentialResultSecret, nowMS int64) (*usCredentialNativeCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_credential_scope")
	}
	prepared, err := prepareUSCredentialIssue(in, nowMS)
	if err != nil {
		return nil, err
	}
	if !validUSCredentialSecret(secret, prepared.kind) || !usFamilyBirthCredentialMatches(birth, prepared) {
		return nil, invalid("us_credential_protected")
	}
	if _, err = prepareUSCredentialFamilyBirth(birth, prepared); err != nil {
		return nil, err
	}
	job := usCredentialIssueJob{ownerID: owner, issuer: issuer, prepared: *prepared, birth: birth, secret: cloneUSCredentialSecret(secret)}
	digest, err := canonicalRequestDigest(usAPICredentialIssue, 1, identity, job)
	if err != nil {
		return nil, err
	}
	return &usCredentialNativeCommand{kind: usAPICredentialIssue, owner: owner, identity: identity, digest: digest, issue: &job}, nil
}

// prepareUSCredentialNativeRevoke freezes one top-level revoke. The merged
// preparation owns the exact source-row witnesses; the writer verifies them.
func prepareUSCredentialNativeRevoke(identity CommandIdentity, owner string, issuer usPolicyIssuer, in usCredentialRevokeRequest) (*usCredentialNativeCommand, error) {
	if !validUSSecurityIdentity(identity, owner, issuer) {
		return nil, invalid("us_credential_scope")
	}
	prepared, err := prepareUSCredentialRevoke(in)
	if err != nil {
		return nil, err
	}
	job := usCredentialRevokeJob{ownerID: owner, issuer: issuer, prepared: *prepared}
	digest, err := canonicalRequestDigest(usAPICredentialRevoke, 1, identity, job)
	if err != nil {
		return nil, err
	}
	return &usCredentialNativeCommand{kind: usAPICredentialRevoke, owner: owner, identity: identity, digest: digest, revoke: &job}, nil
}

// usCredentialIssueEffect is the complete actual source prefix of one issue.
type usCredentialIssueEffect struct {
	createdMS, secretBytes int64
	changed                bool
}

// usCredentialRevokeEffect is the complete actual source result of one revoke.
type usCredentialRevokeEffect struct {
	row               usCredentialSourceRow
	familyIncarnation [16]byte
	lastUsedMS        int64
	changed           bool
}

// usCredentialSourceRow is the complete exact current source row of one
// top-level credential. It is a comparison/capture value, never authority.
type usCredentialSourceRow struct {
	account, profile, id, kind, name, scopes, secretHash, hint string
	family, device, endedReason                                string
	createdMS, expiresMS, revokedMS, lastUsedMS                int64
}

// applyUSCredentialIssue inserts the one TOP-LEVEL api_credentials row, births
// the security_kind=1 credential family with its device/witnesses and keeps
// current owner-recovery custody for key/app-password. It creates no descendant
// credential and updates no ordinary session family.
func applyUSCredentialIssue(ctx context.Context, turn *commandTurn, job *usCredentialIssueJob, nowMS int64) (out usCredentialIssueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || job == nil || !usCredentialTime(nowMS) {
		return out, ErrInvalid
	}
	if err = usCredentialFrame(ctx, turn, usAPICredentialIssue); err != nil {
		return out, err
	}
	p := job.prepared
	if err = usCredentialCheckTarget(ctx, turn, p.target); err != nil {
		return out, err
	}
	// The prepared expiry is re-compared under the SAME trusted turn clock.
	if p.expiresMS != 0 {
		expiry := time.Unix(p.expirySeconds, int64(p.expiryNanos))
		now := time.UnixMilli(nowMS)
		if !expiry.After(now.Add(time.Minute)) || expiry.After(now.Add(10*365*24*time.Hour)) {
			return out, ErrConflict
		}
	}
	row, e := usCredentialQuery(ctx, turn, usCredentialSourceInsert, p.id, p.kind, p.target.account, p.target.profile, p.name, p.scopes, p.secretHash, p.hint, p.family, p.device, nowMS, p.expiresMS)
	if e != nil {
		return out, e
	}
	var got usCredentialSourceRow
	if e = scanUSCredentialSource(row, &got); e != nil {
		return out, e
	}
	want := usCredentialSourceRow{account: p.target.account, profile: p.target.profile, id: p.id, kind: p.kind, name: p.name, scopes: p.scopes, secretHash: p.secretHash, hint: p.hint, family: p.family, device: p.device, createdMS: nowMS, expiresMS: p.expiresMS}
	if got != want {
		return out, ErrResolveRequired
	}
	birth, e := prepareUSCredentialFamilyBirth(job.birth, &p)
	if e != nil {
		return out, e
	}
	if _, e = applyUSCredentialFamilyBirth(ctx, turn, birth, &p); e != nil {
		return out, e
	}
	if p.kind == "key" || p.kind == "app_password" {
		row, e = usCredentialQuery(ctx, turn, usCredentialSecretInsert, p.id, p.kind, p.target.account, p.target.memberIncarnation, p.target.profile, p.target.profileLifetime, p.family, job.birth.incarnation[:], p.secretHash, *job.secret.keyID, job.secret.nonce, job.secret.body, job.secret.aadDigest)
		if e != nil {
			return out, e
		}
		var secretBytes int64
		if e = row.Scan(&secretBytes); e != nil {
			return out, e
		}
		wantBytes := int64(len(p.id) + len(p.kind) + len(p.target.account) + len(p.target.memberIncarnation) + len(p.target.profile) + len(p.target.profileLifetime) + len(p.family) + 16 + len(p.secretHash) + len(*job.secret.keyID) + 12 + len(job.secret.body) + 32)
		if secretBytes <= 0 || secretBytes != wantBytes {
			return out, ErrResolveRequired
		}
		out.secretBytes = secretBytes
	}
	out.createdMS, out.changed = nowMS, true
	return out, turn.checkLifetimes(ctx)
}

// applyUSCredentialRevoke updates only the exact top-level source row. It never
// touches a descendant credential, another family or a user's sessions.
func applyUSCredentialRevoke(ctx context.Context, turn *commandTurn, job *usCredentialRevokeJob, nowMS int64) (out usCredentialRevokeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || job == nil || nowMS < 0 {
		return out, ErrInvalid
	}
	if err = usCredentialFrame(ctx, turn, usAPICredentialRevoke); err != nil {
		return out, err
	}
	in := job.prepared.request
	if err = usCredentialCheckTarget(ctx, turn, in.target); err != nil {
		return out, err
	}
	if nowMS < in.createdMS || nowMS < in.revokedMS {
		return out, ErrConflict
	}
	args := usCredentialSourceBindings(in)
	id := usCredentialRevokeRead
	want := usCredentialSourceRow{account: in.target.account, profile: in.target.profile, id: in.id, kind: in.kind, name: in.name, scopes: in.scopes, secretHash: in.secretHash, hint: in.hint, family: in.family, device: in.device, createdMS: in.createdMS, expiresMS: in.expiresMS, revokedMS: in.revokedMS, endedReason: in.endedReason}
	if in.revokedMS == 0 {
		id = usCredentialRevokeWrite
		args = append([]any{nowMS}, args...)
		want.revokedMS = nowMS
		out.changed = true
	}
	row, e := usCredentialQuery(ctx, turn, id, args...)
	if e != nil {
		return out, e
	}
	if e = scanUSCredentialSource(row, &out.row); e != nil {
		return out, e
	}
	if out.row != want {
		return out, ErrResolveRequired
	}
	row, e = usCredentialQuery(ctx, turn, usCredentialFamilyRead, in.family, in.target.account, in.target.profile)
	if e != nil {
		return out, e
	}
	var incarnation []byte
	if e = row.Scan(&incarnation); e != nil {
		return out, errors.Join(ErrConflict, e)
	}
	if len(incarnation) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.familyIncarnation[:], incarnation)
	out.lastUsedMS = out.row.lastUsedMS
	return out, turn.checkLifetimes(ctx)
}

// usCredentialFrame requires the caller's already admitted FULL private turn and
// the exact 521/522 descriptor. It grants no admission and opens no second ledger.
func usCredentialFrame(ctx context.Context, t *commandTurn, id CommandID) error {
	d, ok := describeUSCredentialCommand(id)
	if !ok || (id != usAPICredentialIssue && id != usAPICredentialRevoke) || t == nil || t.transaction == nil {
		return ErrInvalid
	}
	tx := t.transaction
	if tx.descriptor != d || !tx.begun || tx.done || !tx.policyVerified || tx.settings.Synchronous != int(sqliteconn.Full) {
		return ErrInvalid
	}
	return t.checkLifetimes(ctx)
}

// usCredentialCheckTarget re-reads the exact prepared source target and refuses
// a Hosted-linked account before any write. Every TEXT value is compared inside
// SQLite, so no variable source is copied before resource admission.
func usCredentialCheckTarget(ctx context.Context, t *commandTurn, in usCredentialTarget) error {
	row, err := usCredentialQuery(ctx, t, usCredentialTargetRead, in.account, in.epoch, in.username, in.mustChange, in.memberIncarnation, in.memberRevision, in.disabled, in.profile, in.profileLifetime, in.profileRevision, in.profileName, in.deleted)
	if err != nil {
		return err
	}
	var one int64
	if err = row.Scan(&one); err != nil {
		return usCredentialSourceError(err)
	}
	if one != 1 {
		return ErrResolveRequired
	}
	hosted, err := usCredentialQuery(ctx, t, usCredentialHostedRead, in.account)
	if err != nil {
		return err
	}
	var present int64
	switch scan := hosted.Scan(&present); {
	case scan == nil:
		return ErrConflict
	case errors.Is(scan, sql.ErrNoRows):
	default:
		return scan
	}
	return t.checkLifetimes(ctx)
}

func usCredentialSourceBindings(q usCredentialRevokeRequest) []any {
	return []any{q.id, q.kind, q.target.account, q.target.profile, q.name, q.scopes, q.secretHash, q.hint, q.family, q.device, q.createdMS, q.expiresMS, q.revokedMS, q.endedReason}
}

func scanUSCredentialSource(row interface{ Scan(...any) error }, q *usCredentialSourceRow) error {
	return row.Scan(&q.id, &q.kind, &q.account, &q.profile, &q.name, &q.scopes, &q.secretHash, &q.hint, &q.family, &q.device, &q.createdMS, &q.expiresMS, &q.revokedMS, &q.endedReason, &q.lastUsedMS)
}

func usCredentialSourceError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}

func usCredentialQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return commandRow{}, invalid("us_credential_statement")
	}
	return t.queryRow(ctx, step.statementStep, args...)
}

func usCredentialRows(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	if t == nil {
		return nil, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return nil, invalid("us_credential_statement")
	}
	return t.query(ctx, step.statementStep, args...)
}

// usCredentialReceiptRequest is the ORIGINAL retained identity, never a retry
// identity and never authority.
type usCredentialReceiptRequest struct {
	ownerID   string
	kind      CommandID
	reference ReceiptReference
}

type usCredentialOwnerFacts struct {
	receiptOwnerFacts
	// originalFamily is retained provenance, not an equality predicate: the
	// owner is addressed by the exact issuer scope, so a renewed family of the
	// same installation still resolves.
	originalFamily                     string
	reclaimed, count, bytes, retiredMS int64
}

type usCredentialReceiptLookup struct {
	owner    usCredentialOwnerFacts
	decision receiptDecision
	result   usCredentialSavedResult
	digest   ResultDigest
}

func usCredentialReceiptKind(kind CommandID) error {
	_, err := usCredentialResultCodec(kind)
	return err
}

func usCredentialIssuerFacts(a *usOwnerAuthorization) usPolicyIssuer {
	if a == nil {
		return usPolicyIssuer{}
	}
	return a.proof.witness.issuer
}

func usCredentialReceiptForCommand(c *usCredentialNativeCommand) (usCredentialReceiptRequest, error) {
	if c == nil || !usIdentifier(c.owner) || (c.kind != usAPICredentialIssue && c.kind != usAPICredentialRevoke) {
		return usCredentialReceiptRequest{}, ErrInvalid
	}
	return usCredentialReceiptRequest{ownerID: c.owner, kind: c.kind, reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}, nil
}

// readUSCredentialBinding reads only the fixed-width binding header before
// floor/result decoding.
func readUSCredentialBinding(ctx context.Context, turn *commandTurn, q usCredentialReceiptRequest) (out receiptFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usCredentialQuery(ctx, turn, usCredentialReceiptBinding, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, count int64
	if err = row.Scan(&op, &request, &kind, &count, &digest); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || (CommandID(kind) != usAPICredentialIssue && CommandID(kind) != usAPICredentialRevoke) || count != 0 {
		return out, ErrResolveRequired
	}
	out.exists = true
	out.identity.command = q.reference.Identity
	copy(out.identity.command.OperationID[:], op)
	copy(out.identity.digest[:], request)
	copy(out.resultDigest[:], digest)
	out.kind = CommandID(kind)
	out.resultCount = count
	return out, nil
}

// The owner is addressed by BOTH its PK and the exact issuer/scope it was born
// with, so a different current issuer cannot read or advance another cohort.
func readUSCredentialOwner(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usCredentialOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	i := usCredentialIssuerFacts(a)
	row, err := usCredentialQuery(ctx, turn, usCredentialOwner, ownerID, i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.originalFamily, &out.lastSequence, &out.acknowledgedSequence, &out.reclaimed, &out.count, &out.bytes, &out.retiredMS); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(inc) != 16 || !usIdentifier(out.originalFamily) || out.lastSequence < 0 || out.acknowledgedSequence < 0 || out.acknowledgedSequence > out.lastSequence || out.lastSequence-out.acknowledgedSequence > 1 || out.reclaimed < 0 || out.reclaimed > out.acknowledgedSequence || out.count < out.lastSequence-out.acknowledgedSequence || out.bytes < 0 || out.retiredMS < 0 || (out.count == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	out.exists = true
	copy(out.incarnation[:], inc)
	return out, nil
}

type usCredentialCustody struct {
	owner    usCredentialOwnerFacts
	decision receiptDecision
	binding  receiptFacts
}

// Current stamped authority MUST precede every native owner/receipt read.
func lookupUSCredentialCustody(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usCredentialReceiptRequest) (out usCredentialCustody, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		// A final current-source denial REPLACES the body outcome.
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usCredentialCustody{}, denial
		}
	}()
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	if !usIdentifier(q.ownerID) || usCredentialReceiptKind(q.kind) != nil || q.kind != turn.transaction.descriptor.ID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, err
	}
	out.owner, err = readUSCredentialOwner(ctx, turn, a, q.ownerID)
	if err != nil {
		return out, err
	}
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if !out.owner.exists || out.owner.incarnation != expected.command.Incarnation {
		_, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, receiptFacts{})
		return out, err
	}
	binding, err := readUSCredentialBinding(ctx, turn, q)
	if err != nil {
		return out, err
	}
	out.decision, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, binding)
	if err == nil && out.decision == receiptFresh && out.owner.retiredMS != 0 {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	out.binding = binding
	return out, nil
}

func lookupUSCredential(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usCredentialReceiptRequest) (out usCredentialReceiptLookup, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			out, err = usCredentialReceiptLookup{}, denial
		}
	}()
	if err = usCredentialReceiptKind(q.kind); err != nil {
		return out, err
	}
	custody, e := lookupUSCredentialCustody(ctx, turn, a, q)
	if e != nil {
		return out, e
	}
	out.owner, out.decision = custody.owner, custody.decision
	if out.decision != receiptReplay {
		return out, nil
	}
	binding := custody.binding
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	header, err := readUSCredentialHeader(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if err = a.budget.check(ctx, turn, header.bytes); err != nil {
		return out, err
	}
	// Integer-only preflight before any variable child text is read.
	count, resultBytes, err := readUSCredentialResultOctets(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if count < 1 || count > 4 || resultBytes <= 0 || header.bytes <= 0 {
		return out, ErrResolveRequired
	}
	if err = a.budget.check(ctx, turn, resultBytes); err != nil {
		return out, err
	}
	lengths, err := readUSCredentialScopeOctets(ctx, turn, q, count)
	if err != nil {
		return out, err
	}
	charge := header.bytes + resultBytes
	for _, n := range lengths {
		if n <= 0 || n > maxCanonicalResultBytes-charge {
			return out, &Refusal{Code: RefusalCapacity}
		}
		charge += n
	}
	if err = a.budget.check(ctx, turn, charge); err != nil {
		return out, err
	}
	v, typedBytes, scopeCount, err := readUSCredentialResult(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if typedBytes != resultBytes || scopeCount != count {
		return out, ErrResolveRequired
	}
	scopes, err := readUSCredentialScopes(ctx, turn, q, lengths)
	if err != nil {
		return out, err
	}
	v.scopes, v.appliedMS = scopes, header.appliedMS
	if typed, err := usCredentialResultVariableBytes(v); err != nil || typed != charge {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.digest, err = usCredentialResultDigest(expected.command, expected.digest, v)
	if err != nil || out.digest != ResultDigest(binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result = v
	return out, turn.checkLifetimes(ctx)
}

type usCredentialHeaderFacts struct {
	appliedMS int64
	bytes     int64
}

func readUSCredentialHeader(ctx context.Context, turn *commandTurn, q usCredentialReceiptRequest) (out usCredentialHeaderFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := usCredentialQuery(ctx, turn, usCredentialReceiptHeader, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var op, request, digest []byte
	var kind, version, count int64
	var codec string
	var targetAccount, targetMember, targetProfile, targetLifetime, revision, inherits, changed, pinRevision, pinRequired, keyID, nonce, body sql.NullString
	if err = row.Scan(&op, &request, &kind, &version, &codec, &out.appliedMS, &digest, &targetAccount, &targetMember, &targetProfile, &targetLifetime, &revision, &inherits, &changed, &pinRevision, &pinRequired, &count, &keyID, &nonce, &body, &out.bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	wantCodec, _ := usCredentialResultCodec(q.kind)
	if len(op) != 16 || len(request) != 32 || len(digest) != 32 || !equalBytes(op, q.reference.Identity.OperationID[:]) || !equalBytes(request, q.reference.RequestDigest[:]) || kind != int64(q.kind) || version != 1 || codec != wantCodec || out.appliedMS < 0 || targetAccount.Valid || targetMember.Valid || targetProfile.Valid || targetLifetime.Valid || revision.Valid || inherits.Valid || changed.Valid || pinRevision.Valid || pinRequired.Valid || count != 0 || keyID.Valid || nonce.Valid || body.Valid || out.bytes <= 0 {
		return out, ErrResolveRequired
	}
	return out, nil
}

func readUSCredentialResultOctets(ctx context.Context, turn *commandTurn, q usCredentialReceiptRequest) (count, bytes int64, err error) {
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), struct {
			count int64
			bytes int64
		}{count, bytes}, err)
		count, bytes, err = value.count, value.bytes, e
	}()
	row, err := usCredentialQuery(ctx, turn, usCredentialResultOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return 0, 0, err
	}
	if err = row.Scan(&count, &bytes); err != nil {
		return 0, 0, errors.Join(ErrResolveRequired, err)
	}
	return count, bytes, nil
}

func readUSCredentialScopeOctets(ctx context.Context, turn *commandTurn, q usCredentialReceiptRequest, count int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := usCredentialRows(ctx, turn, usCredentialScopeOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, count+1)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]int64, 0, int(count))
	for rows.Next() {
		var ordinal, n int64
		if err = rows.Scan(&ordinal, &n); err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= count || n <= 0 {
			return nil, ErrResolveRequired
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(out)) != count {
		return nil, ErrResolveRequired
	}
	return out, nil
}

func readUSCredentialResult(ctx context.Context, turn *commandTurn, q usCredentialReceiptRequest) (out usCredentialSavedResult, typedBytes, scopeCount int64, err error) {
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), struct {
			value      usCredentialSavedResult
			bytes      int64
			scopeCount int64
		}{out, typedBytes, scopeCount}, err)
		out, typedBytes, scopeCount, err = value.value, value.bytes, value.scopeCount, e
	}()
	row, err := usCredentialQuery(ctx, turn, usCredentialResultRead, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, 0, 0, err
	}
	var member, lifetime, familyIncarnation []byte
	var keyID, nonce, body, aad sql.RawBytes
	out.purpose, out.owner = q.kind, q.ownerID
	if err = row.Scan(&out.id, &out.kind, &out.name, &out.hint, &out.state, &out.endedReason, &out.account, &member, &out.username, &out.profile, &lifetime, &out.profileName, &out.family, &familyIncarnation, &out.device, &out.secretHash, &out.createdMS, &out.expiresMS, &out.lastUsedMS, &out.revokedMS, &keyID, &nonce, &body, &aad, &scopeCount, &typedBytes); err != nil {
		return out, 0, 0, errors.Join(ErrResolveRequired, err)
	}
	if len(member) != 32 || len(lifetime) != 32 || len(familyIncarnation) != 16 {
		return out, 0, 0, ErrResolveRequired
	}
	out.memberIncarnation, out.profileLifetime = string(member), string(lifetime)
	copy(out.familyIncarnation[:], familyIncarnation)
	if keyID != nil {
		value := string(keyID)
		out.secret.keyID = &value
	}
	if nonce != nil {
		out.secret.nonce = append([]byte(nil), nonce...)
	}
	if body != nil {
		out.secret.body = append([]byte(nil), body...)
	}
	if aad != nil {
		out.secret.aadDigest = append([]byte(nil), aad...)
	}
	if scopeCount < 1 || scopeCount > 4 {
		return out, 0, 0, ErrResolveRequired
	}
	return out, typedBytes, scopeCount, nil
}

func readUSCredentialScopes(ctx context.Context, turn *commandTurn, q usCredentialReceiptRequest, lengths []int64) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := usCredentialRows(ctx, turn, usCredentialScopeRead, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, int64(len(lengths)+1))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]string, 0, len(lengths))
	for rows.Next() {
		var ordinal int64
		var scope string
		if err = rows.Scan(&ordinal, &scope); err != nil {
			return nil, err
		}
		if ordinal != int64(len(out)) || ordinal >= int64(len(lengths)) || int64(len(q.ownerID)+16+len(scope)) != lengths[len(out)] {
			return nil, ErrResolveRequired
		}
		out = append(out, scope)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(lengths) {
		return nil, ErrResolveRequired
	}
	return out, nil
}

// Native immutable save: receipt header, typed credential-result child, its
// ordered scope cohort, then the exact owner count/byte debt in ONE frame. No
// event, source CAS or ACK here.
func storeUSCredentialResult(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usCredentialReceiptRequest, v usCredentialSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() {
		charged, err = finishCommand(ctx, commandLifetime(turn), charged, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			charged, err = 0, denial
		}
	}()
	if turn == nil || turn.transaction == nil || q.kind != v.purpose || turn.transaction.descriptor.ID != q.kind || v.owner != q.ownerID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) || digest == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if err = a.recheck(ctx, turn); err != nil {
		return 0, err
	}
	if err = usCredentialReceiptKind(q.kind); err != nil {
		return 0, err
	}
	custody, err := lookupUSCredentialCustody(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if custody.decision != receiptFresh {
		return 0, ErrConflict
	}
	owner := custody.owner
	if !owner.exists || owner.incarnation != q.reference.Identity.Incarnation {
		return 0, ErrResolveRequired
	}
	codec, _ := usCredentialResultCodec(q.kind)
	expectedBytes, err := usCredentialResultVariableBytes(v)
	if err != nil || expectedBytes <= 0 {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	if actual, err := usCredentialResultDigest(q.reference.Identity, q.reference.RequestDigest, v); err != nil || actual != digest {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	opRow, err := usCredentialQuery(ctx, turn, usCredentialReceiptOperation, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.OperationID[:])
	if err != nil {
		return 0, err
	}
	var opSequence int64
	switch scanErr := opRow.Scan(&opSequence); {
	case scanErr == nil:
		if opSequence == q.reference.Identity.Sequence {
			return 0, ErrResolveRequired
		}
		return 0, ErrConflict
	case errors.Is(scanErr, sql.ErrNoRows):
	default:
		return 0, scanErr
	}
	row, err := usCredentialQuery(ctx, turn, usCredentialReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:])
	if err != nil {
		return 0, err
	}
	var headerBytes int64
	if err = row.Scan(&headerBytes); err != nil {
		return 0, err
	}
	scopeArgs := make([]string, len(v.scopes))
	copy(scopeArgs, v.scopes)
	row, err = usCredentialQuery(ctx, turn, usCredentialResultInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, int64(q.kind), v.id, v.kind, v.name, int64(len(v.scopes)), v.hint, v.createdMS, v.expiresMS, v.lastUsedMS, v.revokedMS, v.state, v.endedReason, v.account, v.memberIncarnation, v.username, v.profile, v.profileLifetime, v.profileName, v.family, v.familyIncarnation[:], v.device, v.secretHash, usCredentialNullSecret(v.secret.keyID, true), usCredentialNullBlob(v.secret.nonce), usCredentialNullBlob(v.secret.body), usCredentialNullBlob(v.secret.aadDigest))
	if err != nil {
		return 0, err
	}
	var resultBytes int64
	if err = row.Scan(&resultBytes); err != nil {
		return 0, err
	}
	if headerBytes <= 0 || resultBytes <= 0 || headerBytes+resultBytes >= expectedBytes {
		return 0, ErrResolveRequired
	}
	charged = headerBytes + resultBytes
	for ordinal, scope := range scopeArgs {
		row, err = usCredentialQuery(ctx, turn, usCredentialScopeInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, int64(ordinal), scope)
		if err != nil {
			return 0, err
		}
		var n int64
		if err = row.Scan(&n); err != nil {
			return 0, err
		}
		if n <= 0 || n > expectedBytes-charged {
			return 0, ErrResolveRequired
		}
		charged += n
	}
	if charged != expectedBytes {
		return 0, ErrResolveRequired
	}
	row, err = usCredentialQuery(ctx, turn, usCredentialOwnerAdvance, q.reference.Identity.Sequence, charged, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+charged {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}

func usCredentialNullSecret(value *string, present bool) any {
	if value == nil || !present {
		return nil
	}
	return *value
}

func usCredentialNullBlob(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

// Floor-only ACK: it advances one durable sequence and refunds/deletes nothing.
func acknowledgeUSCredentialAfterAuthority(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, q usCredentialReceiptRequest) (ack int64, err error) {
	defer func() {
		ack, err = finishCommand(ctx, commandLifetime(turn), ack, err)
		if denial := a.recheck(ctx, turn); denial != nil {
			ack, err = 0, denial
		}
	}()
	lookup, err := lookupUSCredential(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if lookup.owner.retiredMS != 0 {
		return 0, ErrOwnerUnavailable
	}
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.reference.Identity.Sequence != lookup.owner.lastSequence {
		return 0, ErrConflict
	}
	row, err := usCredentialQuery(ctx, turn, usCredentialOwnerAck, q.reference.Identity.Sequence, q.ownerID, q.reference.Identity.Incarnation[:], lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&ack); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if ack != q.reference.Identity.Sequence {
		return 0, ErrResolveRequired
	}
	return ack, turn.checkLifetimes(ctx)
}

// readUSCredentialQuota is the scoped owner re-read with exact retained counters.
func readUSCredentialQuota(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usCredentialOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !usIdentifier(ownerID) || turn == nil || a == nil {
		return out, ErrInvalid
	}
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	return readUSCredentialOwner(ctx, turn, a, ownerID)
}

// usCredentialQuotaAdmit refuses a fresh result that would pass either retained
// bound BEFORE any source effect.
func usCredentialQuotaAdmit(owner usCredentialOwnerFacts, charge int64) error {
	if !owner.exists || owner.retiredMS != 0 {
		return ErrOwnerUnavailable
	}
	if charge <= 0 || charge > usCredentialOwnerReceiptBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.count >= usCredentialOwnerReceipts || owner.bytes > usCredentialOwnerReceiptBytes-charge {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.lastSequence != owner.acknowledgedSequence {
		return ErrResolveRequired
	}
	return nil
}

// usCredentialDispatchFund charges the dispatcher's declared rechecks and the
// measured body bytes against the parent's funded memory.
func usCredentialDispatchFund(a *usOwnerAuthorization, body int64) error {
	if a == nil || body < 0 || body > maxCanonicalResultBytes {
		return ErrInvalid
	}
	need := uint64(usCredentialDispatchRechecks)*uint64(usCredentialRecheckBytes) + uint64(body)*32
	if need > a.budget.memory {
		return &Refusal{Code: RefusalUnqualified}
	}
	return nil
}

// usCredentialAppliedMS samples the trusted writer clock once for the immutable
// applied_ms and the quota decision. No real clock and no invented lease.
func usCredentialAppliedMS(turn *commandTurn) (int64, error) {
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	now := tx.writer.now()
	ms := now.UnixMilli()
	if ms < 0 {
		return 0, ErrInvalid
	}
	if err := tx.observeTime(now); err != nil {
		return 0, err
	}
	if deadline, ok := tx.ctx.Deadline(); !ok || !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return ms, nil
}

type usCredentialInvocation struct {
	request usCredentialReceiptRequest
	input   *usCredentialNativeCommand
	ack     bool
}

// usCredentialDispatchResult is the closed result of one dispatch call. Secret
// is present only on a FRESH issue: the sealed protected wire is a
// first-response-only value and replay/revoke never project it again.
type usCredentialDispatchResult struct {
	Outcome ReceiptOutcome
	Value   usCredentialSavedResult
	Digest  ResultDigest
	Secret  usCredentialResultSecret

	// Exact accounting for this command, not a quota claim.
	ChargedBytes int64
	OwnerCount   int64
	OwnerBytes   int64

	// historical marks an original resolved from its receipt (replay/retired)
	// rather than composed in this frame. It is never Applied evidence.
	historical bool
}

func (usCredentialDispatchResult) String() string   { return "usCredentialDispatchResult{redacted}" }
func (usCredentialDispatchResult) GoString() string { return "usCredentialDispatchResult{redacted}" }
func (usCredentialDispatchResult) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usCredentialDispatchResult{redacted}"))
}

type usCredentialCompletion struct {
	out        usCredentialDispatchResult
	floor      AckResult
	historical bool
}

func (usCredentialCompletion) String() string   { return "usCredentialCompletion{redacted}" }
func (usCredentialCompletion) GoString() string { return "usCredentialCompletion{redacted}" }
func (usCredentialCompletion) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usCredentialCompletion{redacted}"))
}

func usCredentialRevokeState(endedReason string) string {
	if endedReason != "" {
		return "ended"
	}
	return "revoked"
}

// usCredentialFreshResult builds the complete prospective immutable result
// BEFORE any source effect so quota admission sees the exact charge. For a
// revoke only integer columns and the 16-byte family incarnation are unknown;
// the placeholder keeps the validated shape and the final charge is re-proved
// against the source capture.
func usCredentialFreshResult(kind CommandID, owner string, c *usCredentialNativeCommand, nowMS int64) (usCredentialSavedResult, error) {
	switch kind {
	case usAPICredentialIssue:
		job, err := usCredentialIssueJobOf(c)
		if err != nil {
			return usCredentialSavedResult{}, err
		}
		p := job.prepared
		return usCredentialSavedResult{purpose: kind, owner: owner, id: p.id, kind: p.kind, name: p.name, hint: p.hint, state: "active", account: p.target.account, memberIncarnation: p.target.memberIncarnation, username: p.target.username, profile: p.target.profile, profileLifetime: p.target.profileLifetime, profileName: p.target.profileName, family: p.family, device: p.device, secretHash: p.secretHash, familyIncarnation: job.birth.incarnation, scopes: strings.Split(p.scopes, ","), createdMS: nowMS, expiresMS: p.expiresMS, appliedMS: nowMS, secret: cloneUSCredentialSecret(job.secret)}, nil
	case usAPICredentialRevoke:
		job, err := usCredentialRevokeJobOf(c)
		if err != nil {
			return usCredentialSavedResult{}, err
		}
		in := job.prepared.request
		return usCredentialSavedResult{purpose: kind, owner: owner, id: in.id, kind: in.kind, name: in.name, hint: in.hint, state: usCredentialRevokeState(in.endedReason), endedReason: in.endedReason, account: in.target.account, memberIncarnation: in.target.memberIncarnation, username: in.target.username, profile: in.target.profile, profileLifetime: in.target.profileLifetime, profileName: in.target.profileName, family: in.family, device: in.device, secretHash: in.secretHash, familyIncarnation: [16]byte{1}, scopes: strings.Split(in.scopes, ","), createdMS: in.createdMS, expiresMS: in.expiresMS, lastUsedMS: 0, revokedMS: in.revokedMS, appliedMS: nowMS}, nil
	default:
		return usCredentialSavedResult{}, ErrInvalid
	}
}

// executeUSCredentialInvocation is the composition of the merged US-01 owner
// stamp, the 521/522 source effects and the retained receipt. It returns Pending
// for a fresh effect: only the physical terminal may declare Applied.
func executeUSCredentialInvocation(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usCredentialInvocation) (out usCredentialDispatchResult, floor AckResult, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), usCredentialCompletion{out, floor, historical}, err)
		out, floor, historical, err = v.out, v.floor, v.historical, e
	}()
	if turn == nil || turn.transaction == nil || a == nil || inv == nil {
		return out, floor, false, ErrInvalid
	}
	kind := turn.transaction.descriptor.ID
	q := inv.request
	if (kind != usAPICredentialIssue && kind != usAPICredentialRevoke) || q.kind != kind || usCredentialReceiptKind(kind) != nil ||
		q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.Identity.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) {
		return out, floor, false, ErrInvalid
	}
	// An earlier prefix in this same frame may have advanced a source row, so
	// current authority is rechecked before ANY owner or payload read.
	if err = a.recheck(ctx, turn); err != nil {
		return out, floor, false, err
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, floor, false, err
	}
	if err = usCredentialDispatchFund(a, 0); err != nil {
		return out, floor, false, err
	}
	if inv.ack {
		if inv.input != nil {
			return out, floor, false, ErrInvalid
		}
		floor.AcknowledgedThrough, err = acknowledgeUSCredentialAfterAuthority(ctx, turn, a, q)
		return out, floor, true, err
	}
	// A submission to apply is admitted against the CURRENT retained owner
	// before classification: retirement may not be resurrected by a write, and
	// the quota is refused before any source effect.
	if inv.input != nil {
		quota, e := readUSCredentialQuota(ctx, turn, a, q.ownerID)
		if e != nil {
			return out, floor, false, e
		}
		if quota.exists && quota.retiredMS != 0 && q.reference.Identity.Sequence > quota.lastSequence {
			return out, floor, false, ErrOwnerUnavailable
		}
	}
	lookup, err := lookupUSCredential(ctx, turn, a, q)
	if err != nil {
		return out, floor, false, err
	}
	out.Outcome.Reference = q.reference
	out.OwnerCount, out.OwnerBytes = lookup.owner.count, lookup.owner.bytes
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		out.historical = true
		return out, floor, true, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		out.historical = true
		return out, floor, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, false, ErrResolveRequired
	}
	if inv.input == nil {
		// Preparation/status only: no effect, no receipt.
		return out, floor, false, nil
	}
	input := inv.input
	if input.kind != q.kind || input.owner != q.ownerID || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest {
		return out, floor, false, ErrConflict
	}
	// The fresh submission must be the exact frozen preparation. The historical
	// lookup above is deliberately independent of a now-stale preparation.
	digest, err := usCredentialCommandDigest(input)
	if err != nil || digest != input.digest {
		return out, floor, false, errors.Join(ErrConflict, err)
	}
	// Ordinary OWN-ACCOUNT, top-level only. The US-01 stamp proves a current
	// direct-owner main-profile session; a different target account, profile
	// lifetime, member incarnation or epoch is a different operation.
	issuer := a.proof.witness.issuer
	var target usCredentialTarget
	if input.kind == usAPICredentialIssue {
		if input.issue.issuer != issuer {
			return out, floor, false, ErrConflict
		}
		target = input.issue.prepared.target
	} else {
		if input.revoke.issuer != issuer {
			return out, floor, false, ErrConflict
		}
		target = input.revoke.prepared.request.target
	}
	if target.account != issuer.account || target.profile != issuer.profile || target.memberIncarnation != issuer.memberIncarnation || target.profileLifetime != issuer.profileLifetime {
		return out, floor, false, ErrDisclosureDenied
	}
	if target.epoch != issuer.epoch {
		return out, floor, false, ErrConflict
	}
	nowMS, err := usCredentialAppliedMS(turn)
	if err != nil {
		return out, floor, false, err
	}
	v, err := usCredentialFreshResult(q.kind, q.ownerID, input, nowMS)
	if err != nil {
		return out, floor, false, err
	}
	// Reserve the complete prospective immutable result BEFORE any source effect.
	charge, err := usCredentialResultVariableBytes(v)
	if err != nil {
		return out, floor, false, err
	}
	if err = usCredentialQuotaAdmit(lookup.owner, charge); err != nil {
		return out, floor, false, err
	}
	if err = usCredentialDispatchFund(a, charge); err != nil {
		return out, floor, false, err
	}
	// 521/522 do NOT advance accounts.epoch, so US-07's post-effect US-01
	// recheck stays valid and runs at release. The effect comes FIRST; the
	// receipt follows within the same frame, so a receipt failure rolls the
	// whole source prefix back.
	if q.kind == usAPICredentialIssue {
		effect, e := applyUSCredentialIssue(ctx, turn, input.issue, nowMS)
		if e != nil {
			return out, floor, false, e
		}
		if !effect.changed || effect.createdMS != nowMS || v.createdMS != nowMS {
			return out, floor, false, ErrResolveRequired
		}
	} else {
		effect, e := applyUSCredentialRevoke(ctx, turn, input.revoke, nowMS)
		if e != nil {
			return out, floor, false, e
		}
		if !effect.changed && input.revoke.prepared.request.revokedMS == 0 {
			return out, floor, false, ErrResolveRequired
		}
		v.familyIncarnation = effect.familyIncarnation
		v.lastUsedMS = effect.lastUsedMS
		v.revokedMS = effect.row.revokedMS
		v.state = usCredentialRevokeState(v.endedReason)
		finalCharge, e := usCredentialResultVariableBytes(v)
		if e != nil || finalCharge != charge {
			return out, floor, false, errors.Join(ErrResolveRequired, e)
		}
	}
	out.Digest, err = usCredentialResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, floor, false, err
	}
	charged, err := storeUSCredentialResult(ctx, turn, a, q, v, out.Digest)
	if err != nil || charged != charge {
		return out, floor, false, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.ChargedBytes = v, charged
	out.OwnerCount, out.OwnerBytes = lookup.owner.count+1, lookup.owner.bytes+charged
	if q.kind == usAPICredentialIssue {
		// FIRST-RESPONSE-ONLY protected wire. Replay returns the saved result
		// without this projection.
		out.Secret = cloneUSCredentialSecret(input.issue.secret)
	}
	// 521/522 append no api_event and raise no notice in this component.
	return out, floor, false, turn.checkLifetimes(ctx)
}

func usCredentialCommandDigest(c *usCredentialNativeCommand) (RequestDigest, error) {
	if c == nil {
		return RequestDigest{}, ErrInvalid
	}
	switch c.kind {
	case usAPICredentialIssue:
		job, err := usCredentialIssueJobOf(c)
		if err != nil {
			return RequestDigest{}, err
		}
		return canonicalRequestDigest(c.kind, 1, c.identity, *job)
	case usAPICredentialRevoke:
		job, err := usCredentialRevokeJobOf(c)
		if err != nil {
			return RequestDigest{}, err
		}
		return canonicalRequestDigest(c.kind, 1, c.identity, *job)
	default:
		return RequestDigest{}, ErrInvalid
	}
}

// runUSCredentialDispatch is the closed test/harness entry. It owns the final
// current disclosure and the FULL physical terminal; the caller supplies only
// the already admitted turn.
func runUSCredentialDispatch(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usCredentialInvocation) (out usCredentialDispatchResult, floor AckResult, err error) {
	var historical bool
	defer func() {
		v, e := finishCommand(ctx, ctx, usCredentialCompletion{out, floor, historical}, err)
		out, floor, err = v.out, v.floor, e
	}()
	out, floor, historical, err = executeUSCredentialInvocation(ctx, turn, a, inv)
	if err != nil {
		return withdrawUSCredential(turn, err)
	}
	// POST-EFFECT current disclosure before the terminal: 521/522 do not advance
	// accounts.epoch, so the US-01 stamp must still be current. A release-time
	// denial REPLACES the body outcome and rolls the whole prefix back.
	if denial := a.recheck(ctx, turn); denial != nil {
		return withdrawUSCredential(turn, denial)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUSCredential(turn, ctxErr)
	}
	// The release rule runs BEFORE the terminal: the transaction cancels its
	// own lifetime at commit/rollback, so this release uses the live frame
	// lifetime. The deferred final publication rechecks the CALLER's context
	// after physical return, exactly like the shared dispatcher.
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), usCredentialCompletion{out, floor, historical}, nil)
	if releaseErr != nil {
		return withdrawUSCredential(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUSCredential(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		// finishCommand's release rule: no error, including a late cleanup
		// failure or a committed safety overrun, may carry output. Retry must
		// resolve the ORIGINAL.
		if commitErr != nil {
			return usCredentialDispatchResult{}, AckResult{}, commitErr
		}
		if inv.ack {
			return usCredentialDispatchResult{}, floor, nil
		}
		if historical {
			return out, floor, nil
		}
		// Only a submission that actually composed effects is Applied. A bare
		// Resolve of a never-applied command stays Pending.
		if inv.input != nil && out.Outcome.State == Pending {
			out.Outcome.State = Applied
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return usCredentialDispatchResult{}, AckResult{}, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return usCredentialDispatchResult{}, AckResult{}, commitErr
	}
}

// withdrawUSCredential rolls back and returns the zero closed result: no partial
// output, receipt or source effect survives any failure.
func withdrawUSCredential(turn *commandTurn, err error) (usCredentialDispatchResult, AckResult, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return usCredentialDispatchResult{}, AckResult{}, err
}
