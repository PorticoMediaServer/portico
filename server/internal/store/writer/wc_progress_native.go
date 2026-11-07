package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"slices"
	"time"
)

const (
	wcProgressNativeEvent        stmt.ID = "wc.progress.current_event"
	wcProgressSnapshotOctets     stmt.ID = "wc.progress.snapshot_octets"
	wcProgressNativeScope        stmt.ID = "wc.progress.current_scope"
	wcProgressNativeProfileScope stmt.ID = "wc.progress.current_profile_scope"
	wcProgressNativeToken        stmt.ID = "wc.progress.current_token"
	wcProgressNativeOwner        stmt.ID = "wc.progress.current_owner"
	wcProgressNativeSnapshot     stmt.ID = "wc.progress.current_snapshot"
)

func wcProgressCommandDefinitions() []stmt.Definition {
	defs := append(wcProgressDefinitions(), wcProgressAckDefinitions()...)
	defs = append(defs, wcAPIEventDefinitions()...)
	defs = append(defs, append(wcProgressProtocolDefinitions(), wcProgressReceiptDefinition())...)
	return defs
}
func wcProgressProtocolDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	common := `SELECT a.epoch,p.is_primary FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1 INNER JOIN direct_memberships AS m INDEXED BY direct_memberships_people_role ON m.account_id=a.id INNER JOIN direct_profiles AS p INDEXED BY direct_profiles_broadcast ON p.account_id=a.id WHERE a.id=? AND p.id=? AND m.incarnation=? AND p.lifetime=? AND m.disabled=0 AND p.deleted=0 AND m.allowed_libraries=? AND m.revision=? AND p.revision=? AND m.role=? AND p.is_primary=? AND a.profile_id=? AND `
	params := []stmt.Param{text("account"), text("profile"), text("member_birth"), text("profile_birth"), text("member_libraries"), integer("member_revision"), integer("profile_revision"), text("membership_role"), integer("primary"), text("primary_profile")}
	bindings := []any{"account-one", "profile-one", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", `["library-one"]`, int64(1), int64(1), "member", int64(1), "profile-one"}
	defs := []stmt.Definition{
		{ID: wcProgressNativeEvent, Owner: "store/writer WC snapshot", CallSites: []string{"store/writer/wc_progress_native.go"}, Kind: stmt.Read, SQL: `SELECT id FROM api_events INDEXED BY api_events_audience WHERE audience=? AND id>=? ORDER BY id DESC LIMIT 1`, Params: []stmt.Param{text("audience"), integer("first")}, Cohorts: []stmt.Cohort{{Name: "latest_own_hint", Bindings: []any{"profile:local:account-one:profile-one", int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"api_events_audience"}}},
		{ID: wcProgressSnapshotOctets, Owner: "store/writer WC progress", CallSites: []string{"store/writer/wc_progress_native.go"}, Kind: stmt.Read, SQL: `SELECT octet_length(s.state) FROM personal_items AS i INDEXED BY personal_current_live INNER JOIN personal_snapshots AS s ON s.profile_id=i.profile_id AND s.item_id=i.item_id AND s.revision=i.revision WHERE i.profile_id=? AND i.item_id=?`, Params: []stmt.Param{text("profile"), integer("subject")}, Cohorts: []stmt.Cohort{{Name: "current_snapshot_bytes", Bindings: []any{"viewer-one", int64(11)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live", "sqlite_autoindex_personal_snapshots_1"}}},
		{ID: wcProgressNativeScope, Owner: "store/writer WC disclosure", CallSites: []string{"store/writer/wc_progress_native.go"}, Kind: stmt.Read, SQL: common + `p.allowed_libraries IS NULL`, Params: params, Cohorts: []stmt.Cohort{{Name: "inherited_profile", Bindings: bindings}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_accounts_1", "direct_memberships_people_role", "direct_profiles_broadcast"}}},
		{ID: wcProgressNativeProfileScope, Owner: "store/writer WC disclosure", CallSites: []string{"store/writer/wc_progress_native.go"}, Kind: stmt.Read, SQL: common + `p.allowed_libraries=?`, Params: append(slices.Clone(params), text("profile_libraries")), Cohorts: []stmt.Cohort{{Name: "restricted_profile", Bindings: append(slices.Clone(bindings), `["library-one"]`)}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_accounts_1", "direct_memberships_people_role", "direct_profiles_broadcast"}}},
		{ID: wcProgressNativeToken, Owner: "store/writer WC disclosure", CallSites: []string{"store/writer/wc_progress_native.go"}, Kind: stmt.Read,
			SQL:    `SELECT f.current_generation FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 INNER JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id LEFT JOIN authorization_family_grants AS g INDEXED BY sqlite_autoindex_authorization_family_grants_1 ON g.family_id=f.id WHERE t.token_hash=? AND f.id=? AND f.server_id=? AND f.account_id=? AND f.profile_id=? AND f.authority='local' AND f.role=? AND f.epoch=? AND f.revoked=0 AND f.pin_gate=0 AND t.retired=0 AND t.generation=? AND f.current_generation=t.generation AND f.command_member_incarnation=? AND f.command_profile_lifetime=? AND g.family_id IS NULL AND f.authorization_horizon=? AND t.expires_at=?`,
			Params: []stmt.Param{text("token"), text("family"), text("server"), text("account"), text("profile"), text("role"), integer("epoch"), integer("generation"), text("member_birth"), text("profile_birth"), text("horizon"), text("expiry")}, Cohorts: []stmt.Cohort{{Name: "ordinary_current_family", Bindings: []any{"wc-token", "wc-family", "server", "account-one", "profile-one", "member", int64(1), int64(1), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "2099-01-01T00:00:00Z", "2099-01-01T00:00:00Z"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_authorization_family_grants_1"}}},
		{ID: wcProgressNativeOwner, Owner: "store/writer WC progress owner", CallSites: []string{"store/writer/wc_progress_native.go"}, Kind: stmt.Read,
			SQL:    `SELECT command_last_sequence,command_acknowledged_sequence,accepted_ordinal,last_accepted_sequence,pending_time_sequence,source_occurrence_generation,occurrence_start_event_id,pending_time_from_ms,pending_time_until_ms,pending_time_cursor_ms,pending_time_activity_generation,pending_time_remember_generation,pending_time_clock_incarnation,receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms,control_variable_bytes FROM playback_personal_claims INDEXED BY sqlite_autoindex_playback_personal_claims_2 WHERE playback_id=? AND command_incarnation=? AND profile_key=? AND item_id=? AND command_authority='local' AND command_account_id=? AND command_profile_id=? AND command_member_incarnation=? AND command_profile_lifetime=? AND command_library_id=?`,
			Params: []stmt.Param{text("playback"), blob("incarnation"), text("profile_key"), integer("subject"), text("account"), text("profile"), text("member_birth"), text("profile_birth"), integer("library")}, Cohorts: []stmt.Cohort{{Name: "retained_owner", Bindings: []any{"playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "viewer-one", int64(11), "account-one", "profile-one", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(7)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}}},
		{ID: wcProgressNativeSnapshot, Owner: "store/writer WC snapshot", CallSites: []string{"store/writer/wc_progress_native.go"}, Kind: stmt.Read,
			SQL:    `SELECT h.activity_generation,h.history_policy_generation,h.revision,v.revision,v.observation_revision,i.revision,i.played_ms FROM personal_activity_revisions AS h INDEXED BY sqlite_autoindex_personal_activity_revisions_1 INNER JOIN viewer_revisions AS v INDEXED BY sqlite_autoindex_viewer_revisions_1 ON v.profile_id=h.profile_id INNER JOIN personal_items AS i INDEXED BY personal_current_live ON i.profile_id=h.profile_id INNER JOIN catalog_subjects AS s ON s.id=i.item_id INNER JOIN catalog_libraries AS l ON l.id=s.library_id WHERE h.profile_id=? AND v.library_id=? AND i.item_id=? AND s.public_id=? AND l.id=? AND l.library_id=? AND h.policy_authority='local' AND h.policy_account_id=? AND h.policy_profile_id=? AND h.policy_member_incarnation=? AND h.policy_profile_lifetime=?`,
			Params: []stmt.Param{text("profile_key"), text("library_key"), integer("subject"), blob("public_id"), integer("library"), text("library_key_echo"), text("account"), text("profile"), text("member_birth"), text("profile_birth")}, Cohorts: []stmt.Cohort{{Name: "current_own_data", Bindings: []any{"viewer-one", "library-one", int64(11), []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(7), "library-one", "account-one", "profile-one", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_personal_activity_revisions_1", "sqlite_autoindex_viewer_revisions_1", "personal_current_live"}, PrimaryKeys: []string{"s", "l"}}},
	}
	pk := func(table, name string, columns ...string) stmt.AutoIndexBinding {
		fields := make([]stmt.IndexColumn, len(columns))
		for i, c := range columns {
			fields[i] = stmt.IndexColumn{Name: c, Collation: "BINARY"}
		}
		return stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: fields}
	}
	for i := range defs {
		switch defs[i].ID {
		case wcProgressNativeScope, wcProgressNativeProfileScope:
			defs[i].Plan.AutoIndexes = []stmt.AutoIndexBinding{pk("accounts", "sqlite_autoindex_accounts_1", "id")}
		case wcProgressNativeToken:
			defs[i].Plan.AutoIndexes = []stmt.AutoIndexBinding{pk("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash"), pk("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id"), pk("authorization_family_grants", "sqlite_autoindex_authorization_family_grants_1", "family_id")}
		case wcProgressNativeOwner:
			binding := pk("playback_personal_claims", "sqlite_autoindex_playback_personal_claims_2", "playback_id", "command_incarnation")
			binding.Origin = "u"
			defs[i].Plan.AutoIndexes = []stmt.AutoIndexBinding{binding}
		case wcProgressNativeSnapshot:
			defs[i].Plan.AutoIndexes = []stmt.AutoIndexBinding{pk("personal_activity_revisions", "sqlite_autoindex_personal_activity_revisions_1", "profile_id"), pk("viewer_revisions", "sqlite_autoindex_viewer_revisions_1", "profile_id", "library_id")}
		}
	}
	return defs
}

func wcProgressLibraryAllowed(s wcProgressScope) bool {
	var member []string
	if json.Unmarshal([]byte(s.memberLibraries), &member) != nil {
		return false
	}
	all := s.membershipRole == "owner" || s.membershipRole == "admin"
	if !all && !slices.Contains(member, s.libraryKey) {
		return false
	}
	if s.profileLibraries.Valid {
		var profile []string
		if json.Unmarshal([]byte(s.profileLibraries.String), &profile) != nil || !slices.Contains(profile, s.libraryKey) {
			return false
		}
	}
	return true
}
func checkWCProgressNativeDisclosure(ctx context.Context, t *commandTurn, v *wcProgressInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = checkWCProgressDisclosure(v.disclosure.driver.writer, v); err != nil {
		return err
	}
	if v.birth != nil {
		if v.birth.driver != v.disclosure.driver || v.birth.used {
			return ErrDisclosureDenied
		}
		return nil
	}
	if v.retained != nil && !v.currentDrain {
		return nil
	} // exact pending proof is checked by the drain body
	s := v.disclosure.driver.scope
	if !wcProgressLibraryAllowed(s) {
		return ErrDisclosureDenied
	}
	statement := wcProgressNativeScope
	args := []any{s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.memberLibraries, s.memberRevision, s.profileRevision, s.membershipRole, s.primary, s.primaryProfile}
	if s.profileLibraries.Valid {
		statement = wcProgressNativeProfileScope
		args = append(args, s.profileLibraries.String)
	}
	row, err := wc008DraftQuery(ctx, t, statement, args...)
	if err != nil {
		return err
	}
	var epoch, primary int64
	if err = row.Scan(&epoch, &primary); err != nil {
		return errors.Join(ErrDisclosureDenied, err)
	}
	if epoch != s.epoch || s.role != "member" && (primary == 0 || s.membershipRole != s.role) {
		return ErrDisclosureDenied
	}
	row, err = wc008DraftQuery(ctx, t, wcProgressNativeToken, s.tokenHash, s.familyID, s.serverID, s.accountID, s.profileID, s.role, s.epoch, s.tokenGeneration, s.memberBirth, s.profileBirth, s.horizon, s.expires)
	if err != nil {
		return err
	}
	var generation int64
	if err = row.Scan(&generation); err != nil {
		return errors.Join(ErrDisclosureDenied, err)
	}
	horizon, e := time.Parse(time.RFC3339Nano, s.horizon)
	if e != nil {
		return ErrDisclosureDenied
	}
	expires, e := time.Parse(time.RFC3339Nano, s.expires)
	if e != nil {
		return ErrDisclosureDenied
	}
	deadline, ok := t.transaction.ctx.Deadline()
	if !ok || generation != s.tokenGeneration || deadline.After(horizon) || deadline.After(expires) {
		return ErrDisclosureDenied
	}
	return t.checkLifetimes(ctx)
}
func readWCProgressOwner(ctx context.Context, t *commandTurn, s wcProgressScope) (out ProgressSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := wc008DraftQuery(ctx, t, wcProgressNativeOwner, s.playbackID, s.incarnation[:], s.profileKey, s.subjectID, s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.libraryID)
	if err != nil {
		return out, err
	}
	var activity, policy sql.NullInt64
	var clock []byte
	err = row.Scan(&out.Last, &out.Acknowledged, &out.AcceptedOrdinal, &out.LastAcceptedSequence, &out.ReportSequence, &out.Occurrence, &out.StartEvent, &out.From, &out.Until, &out.Cursor, &activity, &policy, &clock, &out.ReceiptRows, &out.ReceiptBytes, &out.AfterSequence, &out.Due, &out.ControlBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	out.Pending = out.From.Valid
	if out.Pending {
		copy(out.Clock[:], clock)
		out.Activity, out.Policy = activity.Int64, policy.Int64
	} else {
		out.Clock = s.clock
	}
	return out, nil
}
func readWCProgressSnapshot(ctx context.Context, t *commandTurn, s wcProgressScope, previous ProgressSnapshot) (out ProgressSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out = previous
	row, err := wc008DraftQuery(ctx, t, wcProgressNativeSnapshot, s.profileKey, s.libraryKey, s.subjectID, s.subjectPublicID[:], s.libraryID, s.libraryKey, s.accountID, s.profileID, s.memberBirth, s.profileBirth)
	if err != nil {
		return ProgressSnapshot{}, err
	}
	var activity, policy int64
	if err = row.Scan(&activity, &policy, &out.History, &out.Viewer, &out.Observation, &out.Personal, &out.PlayedMS); err != nil {
		return ProgressSnapshot{}, err
	}
	if out.Last < 0 || out.Acknowledged < 0 || out.Last < out.Acknowledged || out.Last-out.Acknowledged > 1 || out.ControlBytes <= 0 || out.AcceptedOrdinal < 0 || out.LastAcceptedSequence < -1 || out.Occurrence <= 0 || out.ReceiptRows < 0 || out.ReceiptBytes < 0 || (out.ReceiptRows == 0) != (out.ReceiptBytes == 0) || out.AfterSequence < 0 || out.AfterSequence > out.Acknowledged {
		return ProgressSnapshot{}, ErrResolveRequired
	}
	if out.Pending && (!out.Until.Valid || !out.Cursor.Valid || out.From.Int64 < 0 || out.Until.Int64 <= out.From.Int64 || out.Cursor.Int64 < out.From.Int64 || out.Cursor.Int64 >= out.Until.Int64 || out.Clock == [16]byte{}) {
		return ProgressSnapshot{}, ErrResolveRequired
	}
	if activity <= 0 || policy <= 0 || out.Pending && (activity != out.Activity || policy != out.Policy) {
		return ProgressSnapshot{}, ErrConflict
	}
	out.Activity, out.Policy = activity, policy
	row, err = wc008DraftQuery(ctx, t, wcProgressNativeEvent, apievents.ProfileAudience("local", s.accountID, s.profileID), int64(1))
	if err != nil {
		return ProgressSnapshot{}, err
	}
	var id int64
	err = row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		if id <= 0 {
			return ProgressSnapshot{}, ErrResolveRequired
		}
		out.LastEvent = wcPresent(id)
	}
	if err != nil {
		return ProgressSnapshot{}, err
	}
	return out, nil
}
