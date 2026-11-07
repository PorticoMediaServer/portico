package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	usHostedIssue    stmt.ID = "us.hosted.challenge.issue"
	usHostedSpend    stmt.ID = "us.hosted.challenge.spend"
	usHostedNonce    stmt.ID = "us.hosted.nonce.insert"
	usHostedRevision stmt.ID = "us.hosted.profile.accept"
)

// Inert fixed inventory. Existing cached row only; no trust revocation fanout,
// cleanup, table creation, purpose registration or new source authority.
func usHostedDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	point := func(table string, keys ...string) stmt.Plan {
		cols := make([]stmt.IndexColumn, len(keys))
		seeks := make([]string, len(keys))
		for j, k := range keys {
			cols[j] = stmt.IndexColumn{Name: k, Collation: "BINARY"}
			seeks[j] = k + "=?"
		}
		name := "sqlite_autoindex_" + table + "_1"
		return stmt.Plan{Hot: true, Indexes: []string{name}, Seeks: seeks, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: name, Origin: "pk", Columns: cols}}}
	}
	defs := []stmt.Definition{
		{ID: usHostedIssue, SQL: `INSERT INTO hosted_identity_challenges(challenge_hash,installation_id,expires_ms) VALUES(?,?,?) RETURNING challenge_hash`, Params: []stmt.Param{t("challenge"), t("installation"), i("expiry")}, Plan: stmt.Plan{Hot: true}},
		{ID: usHostedSpend, SQL: `DELETE FROM hosted_identity_challenges INDEXED BY sqlite_autoindex_hosted_identity_challenges_1 WHERE challenge_hash=? AND installation_id=? AND expires_ms=? AND expires_ms>? RETURNING challenge_hash`, Params: []stmt.Param{t("challenge"), t("installation"), i("expiry"), i("now")}, Plan: point("hosted_identity_challenges", "challenge_hash")},
		{ID: usHostedNonce, SQL: `INSERT INTO hosted_identity_nonces(nonce,expires_ms) VALUES(?,?) ON CONFLICT(nonce) DO NOTHING RETURNING nonce`, Params: []stmt.Param{t("nonce"), i("expiry")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_hosted_identity_nonces_1"}}},
		{ID: usHostedRevision, SQL: `UPDATE hosted_profile_revisions INDEXED BY sqlite_autoindex_hosted_profile_revisions_1 SET pin_revision=?,profile_revision=?,trust_revision=?,account_epoch=? WHERE server_id=? AND account_id=? AND profile_id=? AND pin_revision=? AND profile_revision=? AND trust_revision=? AND account_epoch=? RETURNING profile_id`, Params: []stmt.Param{i("pin"), i("profile_revision"), i("trust"), i("epoch"), t("server"), t("account"), t("profile"), i("old_pin"), i("old_profile"), i("old_trust"), i("old_epoch")}, Plan: point("hosted_profile_revisions", "server_id", "account_id", "profile_id")},
	}
	samples := [][]any{
		{strings.Repeat("a", 64), strings.Repeat("i", 32), int64(1800001800000)},
		{strings.Repeat("a", 64), strings.Repeat("i", 32), int64(1800001800000), int64(1800000000000)},
		{strings.Repeat("n", 22), int64(1800000600000)},
		{int64(2), int64(3), int64(4), int64(5), "server", "account", "profile", int64(1), int64(2), int64(3), int64(4)},
	}
	for j := range defs {
		defs[j].Cohorts = []stmt.Cohort{{Name: "point", Bindings: samples[j]}}
		defs[j].Owner = "store/writer US hosted components"
		defs[j].Kind = stmt.Write
		defs[j].CallSites = []string{"store/writer/us_hosted_statements.go"}
	}
	return defs
}
func (p *usHostedPreparation) effects(now int64) []struct {
	id   stmt.ID
	args []any
	want string
} {
	type effect = struct {
		id   stmt.ID
		args []any
		want string
	}
	switch p.kind {
	case usHostedChallengeIssue:
		return []effect{{usHostedIssue, []any{p.challengeHash, p.installation, p.challengeExpiresMS}, p.challengeHash}}
	case usHostedChallengeSpend:
		return []effect{{usHostedSpend, []any{p.challengeHash, p.installation, p.challengeExpiresMS, now}, p.challengeHash}, {usHostedNonce, []any{p.nonce, p.expiresMS + 300000}, p.nonce}}
	case usHostedProfileAccept:
		return []effect{{usHostedRevision, []any{p.next.pin, p.next.profile, p.next.trust, p.next.epoch, p.server, p.account, p.profile, p.source.pin, p.source.profile, p.source.trust, p.source.epoch}, p.profile}}
	}
	return nil
}

// Prefix only: root owes current authority/disclosure, signed-source comparison,
// original receipt identity, admission budgets and complete typed saved result,
// events/owner accounting and FULL COMMIT. ANY failure rolls back the turn.
func applyUSHosted(ctx context.Context, turn *commandTurn, p *usHostedPreparation, now int64) (out usHostedNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if p == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != p.kind || turn.transaction.descriptor.Durability != Full {
		return out, ErrInvalid
	}
	if now < p.nowMS || p.kind != usHostedProfileAccept && p.challengeExpiresMS <= now || p.kind == usHostedChallengeSpend && !p.fresh(now) {
		return out, ErrConflict
	}
	effects := p.effects(now)
	if len(effects) == 0 {
		return out, ErrInvalid
	}
	for _, e := range effects {
		if s, ok := turn.steps[e.id]; !ok || s.kind != stmt.Write {
			return out, ErrInvalid
		}
	}
	for _, e := range effects {
		row, e1 := turn.queryRow(ctx, turn.steps[e.id].statementStep, e.args...)
		if e1 != nil {
			return out, e1
		}
		var got string
		if err = row.Scan(&got); errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		} else if err != nil {
			return out, err
		}
		if got != e.want {
			return out, ErrResolveRequired
		}
	}
	out = p.nativeResult(now)
	return out, turn.checkLifetimes(ctx)
}
