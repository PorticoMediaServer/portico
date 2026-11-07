package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type uoPersonalHistoryHead struct{ revision, activity, recommendation, cutoff, action int64 }

// Current policy/lifetime must be supplied by the parent authorized read. This
// numeric root is compared AGAIN at publication so clear/history-policy drift
// discards all old rows. It never derives privacy from an old receipt body.
func readUOPersonalHistoryHead(q browseQuery, profile, authority, account, profileID, member, birth string, policy int64) (uoPersonalHistoryHead, error) {
	var out uoPersonalHistoryHead
	rows, err := q("uo.personal.history.head", profile, authority, account, profileID, member, birth, policy)
	if err != nil {
		return out, err
	}
	if len(rows) != 1 || len(rows[0]) != 5 {
		return out, ErrDenied
	}
	r := rows[0]
	out = uoPersonalHistoryHead{r[0].(int64), r[1].(int64), r[2].(int64), r[3].(int64), r[4].(int64)}
	if out.revision < 0 || out.activity <= 0 || out.recommendation <= 0 || out.cutoff < 0 || out.action < 0 {
		return uoPersonalHistoryHead{}, ErrUnqualified
	}
	return out, nil
}

type uoPersonalHistoryCursor struct {
	at      string
	id      int64
	head    uoPersonalHistoryHead
	binding [4]string
}
type uoPersonalHistoryRow struct{ id, subject, position, playedMS, generation int64 }
type uoPersonalHistoryPage struct {
	rows      []uoPersonalHistoryRow
	next      *uoPersonalHistoryCursor
	rawRows   int
	exhausted bool
}

// Bounded raw time-keyset history fragment. No historical variable payload is
// selected by the window. Identity/lifetime/privacy checks run AFTER consuming
// raw IDs, so hidden history advances an empty nonterminal page. The final raw
// time anchor is loaded only after its actual point octet_length preflight.
// nativeBytes is the parent's prospective native/Go-copy grant for this point;
// this value does NOT itself issue a grant or establish a public read route.
func readUOPersonalHistory(q browseQuery, account, profileID, member, birth string, head uoPersonalHistoryHead, after *uoPersonalHistoryCursor, nativeBytes int64) (out uoPersonalHistoryPage, err error) {
	if head.cutoff < 0 || head.activity <= 0 || nativeBytes <= 0 {
		return out, ErrInput
	}
	if after != nil && (after.head != head || after.binding != [4]string{account, profileID, member, birth}) {
		return out, ErrStale
	}
	var rows [][]any
	prefix := []any{profileID, member, birth, head.cutoff, account}
	if after == nil {
		rows, err = q("uo.personal.history.first", append(prefix, int64(MaxRaw))...)
	} else {
		rows, err = q("uo.personal.history.same", append(prefix, after.at, after.id, int64(MaxRaw))...)
		if err == nil && len(rows) < MaxRaw {
			var older [][]any
			older, err = q("uo.personal.history.older", append(prefix, after.at, int64(MaxRaw-len(rows)))...)
			rows = append(rows, older...)
		}
	}
	if err != nil {
		return out, err
	}
	if len(rows) > MaxRaw {
		return out, ErrUnqualified
	}
	out.rawRows = len(rows)
	for _, r := range rows {
		if len(r) != 7 {
			return uoPersonalHistoryPage{}, ErrUnqualified
		}
		id := r[0].(int64)
		out.next = &uoPersonalHistoryCursor{id: id, head: head, binding: [4]string{account, profileID, member, birth}}
		if r[6] == nil || r[1] == nil {
			continue
		}
		out.rows = append(out.rows, uoPersonalHistoryRow{id: id, subject: r[1].(int64), position: r[2].(int64), playedMS: r[3].(int64), generation: r[4].(int64)})
	}
	if len(rows) < MaxRaw {
		out.exhausted = true
		out.next = nil
		return out, nil
	}
	n, err := q("uo.personal.history.octets", out.next.id, account)
	if err != nil {
		return uoPersonalHistoryPage{}, err
	}
	if len(n) != 1 || n[0][0].(int64) <= 0 || n[0][0].(int64) > nativeBytes {
		return uoPersonalHistoryPage{}, ErrUnqualified
	}
	times, err := q("uo.personal.history.anchor", out.next.id, account, n[0][0])
	if err != nil {
		return uoPersonalHistoryPage{}, err
	}
	if len(times) != 1 {
		return uoPersonalHistoryPage{}, ErrStale
	}
	out.next.at = times[0][0].(string)
	return out, nil
}
func UOPersonalHistoryDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, p []stmt.Param, args []any, plan stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: readStatementID("uo.personal.history." + name), Owner: "store/read UO personal draft", Kind: stmt.Read, SQL: sql, Params: p, CallSites: []string{"store/read/uo_personal_history.go"}, Cohorts: []stmt.Cohort{{Name: "bounded", Bindings: args}}, Plan: plan}
	}
	out := []stmt.Definition{add("head", `SELECT revision,activity_generation,recommendation_generation,history_visible_from_id,action_sequence FROM personal_activity_revisions INDEXED BY sqlite_autoindex_personal_activity_revisions_1 WHERE profile_id=? AND policy_authority=? AND policy_account_id=? AND policy_profile_id=? AND policy_member_incarnation=? AND policy_profile_lifetime=? AND history_policy_generation=?`, []stmt.Param{s("profile"), s("authority"), s("account"), s("profile_id"), s("member"), s("birth"), i("policy")}, []any{"profile-one", "local", "account-one", "profile-one", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(1)}, stmt.Plan{Hot: true})}
	for _, name := range []string{"first", "same", "older"} {
		where := ""
		params := []stmt.Param{s("profile"), s("member"), s("birth"), i("cutoff"), s("account")}
		args := []any{"profile-one", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(0), "account-one"}
		if name == "same" {
			where = " AND h.occurred_at=? AND h.id<?"
			params = append(params, s("time"), i("id"))
			args = append(args, "2026-11-07T00:00:00Z", int64(100))
		}
		if name == "older" {
			where = " AND h.occurred_at<?"
			params = append(params, s("time"))
			args = append(args, "2026-11-07T00:00:00Z")
		}
		params = append(params, i("limit"))
		args = append(args, int64(64))
		sql := `SELECT h.id,h.subject_id,h.position,h.played_ms,h.activity_generation,h.accepted_sequence,v.id FROM play_history h INDEXED BY play_history_admin_user LEFT JOIN play_history v ON v.id=h.id AND v.profile_id=? AND v.membership_incarnation=? AND v.profile_lifetime=? AND v.id>=? WHERE h.account_key=?` + where + ` ORDER BY h.occurred_at DESC,h.id DESC LIMIT ?`
		out = append(out, add(name, sql, params, args, stmt.Plan{Hot: true, Indexes: []string{"play_history_admin_user"}, PrimaryKeys: []string{"v"}}))
	}
	pin := stmt.AutoIndexBinding{Table: "personal_activity_revisions", Name: "sqlite_autoindex_personal_activity_revisions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}}}
	out[0].Plan = stmt.Plan{Hot: true, Indexes: []string{pin.Name}, AutoIndexes: []stmt.AutoIndexBinding{pin}}
	out = append(out, add("octets", `SELECT octet_length(occurred_at) FROM play_history WHERE id=? AND account_key=?`, []stmt.Param{i("id"), s("account")}, []any{int64(1), "account-one"}, stmt.Plan{Hot: true, PrimaryKeys: []string{"play_history"}}), add("anchor", `SELECT occurred_at FROM play_history WHERE id=? AND account_key=? AND octet_length(occurred_at)=?`, []stmt.Param{i("id"), s("account"), i("bytes")}, []any{int64(1), "account-one", int64(20)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"play_history"}}))
	return out
}
