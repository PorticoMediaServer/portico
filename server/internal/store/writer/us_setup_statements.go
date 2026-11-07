package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	usSetupCodeFacts        stmt.ID = "us.setup.code.facts"
	usSetupCodeInsert       stmt.ID = "us.setup.code.insert"
	usSetupCodeRotate       stmt.ID = "us.setup.code.rotate"
	usSetupAdmissionFacts   stmt.ID = "us.setup.admission.facts"
	usSetupAdmissionIssue   stmt.ID = "us.setup.admission.issue"
	usSetupAdmissionConsume stmt.ID = "us.setup.admission.consume"
	usSetupPendingFacts     stmt.ID = "us.setup.pending.facts"
	usSetupKindFacts        stmt.ID = "us.setup.kind.facts"
)

// Root preconditions in the SAME writer turn: current anonymous capability
// authority and original receipt classification; authoritative no-account/setup
// gate (never a caller bool); current identity_state kind/revision and, for a
// pending request, onboarding operation/revision/setup_hash/auth_mode/ready and
// setupflow phase/expiry. Compare native witnesses, not supplied phase strings.
// Code-proof rotation must compare the verified keyed digest. Admission issuance
// additionally requires proof that THIS token was issued through that accepted
// code to THIS peer/origin. No address-only admission. Neither helper establishes
// completion/owner/claim authority. Root composes event, custody and FULL COMMIT.
// Canonical source has no admission consumed column: consume is a point DELETE.
// Existing reissue refreshes expiry to trusted now+30m and replaces peer/origin;
// a live admission check alone neither consumes nor refreshes it.
func usSetupEffectDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	pk := func(table, index, seek string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "token_hash", Collation: "BINARY"}}}}}
	}
	const codePost = " RETURNING singleton,code_hash,created_ms"
	const admissionPost = " RETURNING token_hash,peer_hash,origin_hash,expires_ms"
	defs := []stmt.Definition{
		{ID: usSetupCodeFacts, Kind: stmt.Read, SQL: "SELECT code_hash,created_ms FROM identity_setup_code WHERE singleton=1", Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_setup_code"}, Seeks: []string{"rowid=?"}}},
		{ID: usSetupCodeInsert, Kind: stmt.Write, SQL: "INSERT INTO identity_setup_code(singleton,code_hash,created_ms) VALUES(?,?,?)" + codePost, Params: []stmt.Param{i("singleton"), t("code"), i("now")}, Plan: stmt.Plan{Hot: true}},
		{ID: usSetupCodeRotate, Kind: stmt.Write, SQL: "UPDATE identity_setup_code SET code_hash=?,created_ms=? WHERE singleton=1 AND code_hash=?" + codePost, Params: []stmt.Param{t("code"), i("now"), t("old_code")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_setup_code"}, Seeks: []string{"rowid=?"}}},
		{ID: usSetupAdmissionFacts, Kind: stmt.Read, SQL: "SELECT token_hash,peer_hash,origin_hash,expires_ms FROM identity_setup_outside_admissions INDEXED BY sqlite_autoindex_identity_setup_outside_admissions_1 WHERE token_hash=?", Params: []stmt.Param{t("token")}, Plan: pk("identity_setup_outside_admissions", "sqlite_autoindex_identity_setup_outside_admissions_1", "token_hash=?")},
		{ID: usSetupAdmissionIssue, Kind: stmt.Write, SQL: "INSERT INTO identity_setup_outside_admissions(token_hash,peer_hash,origin_hash,expires_ms) VALUES(?,?,?,?) ON CONFLICT(token_hash) DO UPDATE SET peer_hash=excluded.peer_hash,origin_hash=excluded.origin_hash,expires_ms=excluded.expires_ms" + admissionPost, Params: []stmt.Param{t("token"), t("peer"), t("origin"), i("expiry")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_identity_setup_outside_admissions_1"}}},
		{ID: usSetupAdmissionConsume, Kind: stmt.Write, SQL: "DELETE FROM identity_setup_outside_admissions INDEXED BY sqlite_autoindex_identity_setup_outside_admissions_1 WHERE token_hash=? AND peer_hash=? AND origin_hash=? AND expires_ms=? AND expires_ms>?" + admissionPost, Params: []stmt.Param{t("token"), t("peer"), t("origin"), i("expected_expiry"), i("now")}, Plan: pk("identity_setup_outside_admissions", "sqlite_autoindex_identity_setup_outside_admissions_1", "token_hash=?")},
		{ID: usSetupPendingFacts, Kind: stmt.Read, SQL: "SELECT operation_id,revision,setup_hash,auth_mode,ready,receipt_until FROM onboarding_state_v1 WHERE singleton=1", Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"onboarding_state_v1"}, Seeks: []string{"rowid=?"}}},
		{ID: usSetupKindFacts, Kind: stmt.Read, SQL: "SELECT sign_in_kind,revision,authority_generation,transition_id FROM identity_state WHERE singleton=1", Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_state"}, Seeks: []string{"rowid=?"}}},
	}
	// Fixed nonsecret keyed-digest-shaped fixtures; no capabilities.
	a := "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE"
	b := "YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmI"
	bindings := map[stmt.ID][]any{
		usSetupCodeFacts: {}, usSetupCodeInsert: {int64(1), a, int64(1000)},
		usSetupCodeRotate:       {b, int64(2000), a},
		usSetupAdmissionFacts:   {a},
		usSetupAdmissionIssue:   {a, b, a, int64(1801000)},
		usSetupAdmissionConsume: {a, b, a, int64(1801000), int64(1000)},
		usSetupPendingFacts:     {}, usSetupKindFacts: {},
	}
	for n := range defs {
		defs[n].Cohorts = []stmt.Cohort{{Name: "point", Bindings: bindings[defs[n].ID]}}
		defs[n].Owner = "store/writer US setup"
		defs[n].CallSites = []string{"store/writer/us_setup_statements.go"}
	}
	return defs
}
func (p *usSetupPrepared) effectBindings(now int64) (stmt.ID, []any, error) {
	if p == nil || !validUSSetupIdentity(p.identity) || now <= 0 || now > usMaxRevision-usSetupAdmissionSpanMS {
		return "", nil, ErrInvalid
	}
	switch p.kind {
	case usSetupCode:
		if !usSetupDigest(p.code) || (p.oldCode != "" && !usSetupDigest(p.oldCode)) || p.code == p.oldCode {
			return "", nil, ErrInvalid
		}
		if p.oldCode == "" {
			return usSetupCodeInsert, []any{int64(1), p.code, now}, nil
		}
		return usSetupCodeRotate, []any{p.code, now, p.oldCode}, nil
	case usSetupOutsideAdmission:
		if !usSetupDigest(p.token) || !usSetupDigest(p.peer) || !usSetupDigest(p.origin) {
			return "", nil, ErrInvalid
		}
		if p.consume {
			if p.expectedExpiry <= now {
				return "", nil, ErrInvalid
			}
			return usSetupAdmissionConsume, []any{p.token, p.peer, p.origin, p.expectedExpiry, now}, nil
		}
		if p.expectedExpiry != 0 {
			return "", nil, ErrInvalid
		}
		return usSetupAdmissionIssue, []any{p.token, p.peer, p.origin, now + usSetupAdmissionSpanMS}, nil
	}
	return "", nil, ErrInvalid
}
