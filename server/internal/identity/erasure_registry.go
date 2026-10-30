package identity

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// ErasureRule describes current handlers, including what they retain. It is
// not a new background purge handler or permission to report physical erasure.
// Group/table identifiers are compiled constants, never job or request input.
type ErasureRule struct {
	Table, Group, Owner, Effect, Handler, Blocker string
}

var profilePersonalErasure = []string{"progress", "personal_items", "personal_receipts", "personal_history", "personal_music_radios", "personal_chapter_completions", "personal_playback_runs", "personal_snapshots", "personal_explicit_events", "personal_conflicts", "personal_activity_receipts", "personal_device_sequences", "personal_evidence_fences", "personal_play_counts"}
var profileSavedErasure = []string{"saved_pins", "saved_resource_receipts", "saved_operation_fences"}
var profileLiveErasure = []string{"live_channel_preferences", "live_preference_receipts"}
var profileReadingErasure = []string{"reading_annotations", "reading_annotation_sets", "reading_saved_groups", "reading_saved_group_receipts", "reading_saved_group_owners"}
var profileRuntimeErasure = []string{"personal_jobs", "personal_completed_plays", "container_personal_state", "container_personal_undo", "personal_watched_intents", "queue_v1_saves"}
var profilePositionErasure = []string{"search_history", "book_resume", "playback_personal_claims", "playback_personal_fences"}

// CurrentErasureRegistry covers the compiled local-profile plan and the separate
// legacy member/Hosted handlers named in Handler.
// Owner explains the exact key encoding: a personal key is not interchangeable
// with the JSON viewer tuple used by download/console owners.
func CurrentErasureRegistry() []ErasureRule {
	var out []ErasureRule
	add := func(tables []string, group, owner, effect, handler, blocker string) {
		for _, table := range tables {
			out = append(out, ErasureRule{table, group, owner, effect, handler, blocker})
		}
	}
	add(profilePersonalErasure, "personal", "profile_id=PersonalKey(viewer)", "delete", "EraseProfileSavedDataTx", "current delete is unbounded; no phased purge enabled")
	add(profileSavedErasure, "saved-owner", "owner_key=PersonalKey(viewer)", "delete", "EraseProfileSavedDataTx", "current delete is unbounded; no phased purge enabled")
	add(profileLiveErasure, "live", "authority/account_id/profile_id tuple", "delete if installed", "eraseProfileRuntimeDataTx", "")
	add(profileReadingErasure, "reading", "owner_key=PersonalKey(viewer)", "delete if installed", "eraseProfileRuntimeDataTx", "")
	add(profileRuntimeErasure, "runtime", "profile_id=PersonalKey(viewer)", "delete if installed", "eraseProfileRuntimeDataTx", "")
	add(profilePositionErasure, "position", "profile_key=PersonalKey(viewer); book_resume uses profile_id", "delete if installed", "eraseProfileRuntimeDataTx", "")
	add([]string{"playlist_shares", "saved_resource_shares", "playlist_receipts"}, "share", "authority/account_id/profile_id tuple", "delete", "EraseProfileSavedDataTx", "")
	add([]string{"catalog_playlists", "saved_resources"}, "owned-resource", "owner_authority/owner_account/owner_profile tuple", "delete with FK children", "EraseProfileSavedDataTx", "child-first indexed phases required before bounded purge")
	add([]string{"reading_progress"}, "reading-position", "owner_key=PersonalKey(viewer)", "delete", "eraseProfileRuntimeDataTx", "")
	add([]string{"retained_title_state"}, "retained-title", "profile_id=PersonalKey(viewer)", "delete when reading_progress installed", "eraseProfileRuntimeDataTx", "")
	add([]string{"playback_personal_profile_fences"}, "late-callback-fence", "profile_key=PersonalKey(viewer)", "retain monotone through_ordinal", "eraseProfileRuntimeDataTx", "owner-key fence remains; lawful retention and true lifetime mapping require review for new erase/root handlers")
	add([]string{"download_requests"}, "download-request", "profile_key=JSON authority/account/profile tuple", "delete with FK children", "eraseProfileRuntimeDataTx", "")
	add([]string{"download_preparations"}, "download-preparation", "JSON viewer tuple plus authority/account/profile", "erase publication_json; cancel active states", "eraseProfileRuntimeDataTx", "preparation/audit identity remains; generic privacy completion not qualified")
	add([]string{"download_preparation_authority"}, "download-authority", "preparation_id of exact JSON viewer owner", "delete", "eraseProfileRuntimeDataTx", "")
	add([]string{"console_documents", "console_receipts", "console_notifications", "console_reports"}, "console", "exact viewer/profile/device/report scopes; documents/receipts include owned session token scopes", "delete", "eraseProfileRuntimeDataTx", "")
	add([]string{"console_exports"}, "console-export", "actor=JSON viewer tuple", "delete", "eraseProfileRuntimeDataTx", "")
	add([]string{"dvr_rule_work", "live_source_dependencies", "dvr_receipts"}, "dvr-work", "rules/recordings owned by authority/account/profile tuple", "delete exact work/dependencies/receipts", "eraseProfileRuntimeDataTx", "")
	add([]string{"dvr_owner_revisions", "dvr_rules", "live_reservations", "dvr_recordings"}, "dvr-retention", "rules/recordings owned by authority/account/profile tuple", "advance owner revision; disable/delete rules; cancel unfinished recording; retain completed files", "eraseProfileRuntimeDataTx", "completed recording ownership/media remain for explicit server-owner disposition; no new audit exception approved")
	add([]string{"preference_scopes"}, "preferences", "authority/account_id/profile_id tuple or exact authority/account", "delete with value/language FK children", "preferenceownership.EraseProfile/EraseAccount", "")
	add([]string{"api_credentials"}, "member-credential", "account_id", "revoke before family erasure", "Service.EraseMemberTx", "current per-account update is unbounded")
	add([]string{"authorization_session_families", "authorization_family_tokens", "authorization_family_renewals", "authorization_family_grants"}, "family", "exact account/family predicates and hooks", "revoke through family hooks; replay facts follow existing family retention", "Service.EraseMemberTx; RevokeFamilyTx", "full root/lifetime/result cleanup absent")
	add([]string{"playback_sessions"}, "member-playback", "account_id", "stop; family hooks retire playback/queues/readers", "Service.EraseMemberTx", "")
	add([]string{"quick_connect_v1"}, "member-approval", "approver_id with status=approved", "cancel and clear grant_cipher/user_hash", "Service.EraseMemberTx", "")
	add([]string{"identity_device_families", "identity_refresh_credentials", "identity_devices"}, "member-device", "device_id in authority=local/account_id devices; devices use tuple", "delete children before devices", "Service.EraseMemberTx", "")
	add([]string{"accounts"}, "member-root", "id=account, refuse owner", "delete with current FK children", "Service.EraseMemberTx", "no new background authority-retirement/purge job is enabled")
	add([]string{"direct_profiles"}, "profile-root", "id/account owner and expected revision", "deleted=1; clear PIN/art/policy; retain final history label; bounded purge", "Service.RetireDirectProfile; Service.PurgeProfileStep", "")
	add([]string{"play_history"}, "lawful-play-history", "authority/account_id and optional profile_id", "retained-by-policy for removed profiles; owner-main history/activity only with final display name", "RetireDirectProfile; owner history projection", "member/hosted erasure remain separate; profile purge never touches play_history")
	add([]string{"hosted_profile_erasure_receipts", "hosted_claim_cleanup_receipts", "identity_move_waiting"}, "hosted-move", "existing hosted owner/claim/move keys", "owned by hosted erasure/claim/move handlers, never by local purge", "hosted erasure/claim and account-move owners", "local handler must not claim hosted physical erasure")

	// The profile route uses the compiled, bounded registry. Preserve the other
	// erasure owners' descriptions and blockers: this slice cannot certify a
	// member/Hosted erase merely because its profile counterpart is complete.
	byTable := map[string][]string{}
	actions := map[string]string{}
	for _, st := range profilePurgeRegistry {
		byTable[st.table] = append(byTable[st.table], st.name)
		action := "delete"
		if st.action == purgeScrub {
			action = "scrub profile attribution"
		}
		if st.action == purgeRevokeFamily {
			action = "revoke"
		}
		if prior := actions[st.table]; prior != "" && prior != action {
			action = "compiled child-first delete/scrub"
		}
		actions[st.table] = action
	}
	seen := map[string]bool{}
	for i := range out {
		r := &out[i]
		seen[r.Table] = true
		if len(byTable[r.Table]) == 0 {
			continue
		}
		r.Handler = "Service.PurgeProfileStep; legacy: " + r.Handler
		r.Effect = "local profile: " + actions[r.Table] + "; legacy: " + r.Effect
		if r.Blocker != "" {
			r.Blocker = "legacy member/Hosted owner only: " + r.Blocker
		}
	}
	for _, st := range profilePurgeRegistry {
		if seen[st.table] {
			continue
		}
		seen[st.table] = true
		out = append(out, ErasureRule{Table: st.table, Group: "compiled-profile-purge", Owner: "exact job actor/profile lifetime; compiled indexed scope", Effect: actions[st.table], Handler: "Service.PurgeProfileStep"})
	}
	// These tables mix erasable runtime metadata with retained library media.
	// Describe their actual conditional effect rather than implying a table-wide
	// DELETE from the presence of one compiled unit.
	for i := range out {
		r := &out[i]
		switch r.Table {
		case "live_allocations":
			r.Effect = "delete kind=live viewer allocations only; retain kind=recording rows unchanged"
		case "dvr_owner_revisions":
			r.Effect = "local profile: retain media-list counter while any dvr_recordings row has owner_key; delete otherwise; legacy: " + r.Effect
		case "dvr_recordings", "live_reservations":
			r.Effect = "local profile: retained-by-policy unchanged, including creator attribution; legacy: " + r.Effect
			r.Handler = "Service.RetireDirectProfile retained-media policy; legacy: " + r.Handler
			if r.Blocker != "" {
				r.Blocker = "legacy member/Hosted owner only: " + r.Blocker
			}
		}
	}
	out = append(out, ErasureRule{Table: "playback_start_preparations", Group: "compiled-profile-purge", Owner: "actor operation_id mapper and exact retired owner", Effect: "atomic actor/root delete after child drains", Handler: "Service.PurgeProfileStep"})
	return out
}

// ErasureSchema records exact PK/FK/index geometry from the composed baseline.
// Missing indexes are evidence blockers, not a claim that a LIMIT bounds work.
type ErasureSchema struct {
	Rule       ErasureRule
	PrimaryKey []string
	Indexes    [][]string
	Children   []ErasureEdge
	Triggers   []ErasureTrigger
}
type ErasureTrigger struct{ Name, Table, SQL string }

type ErasureEdge struct{ Parent, Table, From, To, OnDelete string }

func InspectErasureRegistry(ctx context.Context, db *sql.DB) ([]ErasureSchema, error) {
	var out []ErasureSchema
	for _, rule := range CurrentErasureRegistry() {
		s := ErasureSchema{Rule: rule}
		rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) WHERE pk>0 ORDER BY pk`, rule.Table)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			s.PrimaryKey = append(s.PrimaryKey, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(s.PrimaryKey) == 0 {
			return nil, fmt.Errorf("erasure table %s has no typed primary key", rule.Table)
		}
		rows, err = db.QueryContext(ctx, `SELECT l.name,i.name FROM pragma_index_list(?) l JOIN pragma_index_info(l.name) i ORDER BY l.name,i.seqno`, rule.Table)
		if err != nil {
			return nil, err
		}
		last := ""
		for rows.Next() {
			var name string
			var value sql.NullString
			if err = rows.Scan(&name, &value); err != nil {
				rows.Close()
				return nil, err
			}
			if name != last {
				s.Indexes = append(s.Indexes, nil)
				last = name
			}
			column := value.String
			if !value.Valid {
				column = "(expression)"
			}
			s.Indexes[len(s.Indexes)-1] = append(s.Indexes[len(s.Indexes)-1], column)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	// Read actual reverse FK edges, including ownership children not directly
	// named by a DELETE. No table identifier is interpolated into SQL.
	rows, err := db.QueryContext(ctx, `SELECT s.name,f."table",f."from",COALESCE(f."to",'(implicit primary key)'),f.on_delete FROM sqlite_schema s JOIN pragma_foreign_key_list(s.name) f WHERE s.type='table' ORDER BY s.name,f.id,f.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	edges := map[string][]ErasureEdge{}
	for rows.Next() {
		var child, parent, from, to, action string
		if err = rows.Scan(&child, &parent, &from, &to, &action); err != nil {
			return nil, err
		}
		edges[parent] = append(edges[parent], ErasureEdge{parent, child, from, to, action})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	triggerRows, err := db.QueryContext(ctx, `SELECT name,tbl_name,sql FROM sqlite_schema WHERE type='trigger' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	triggers := map[string][]ErasureTrigger{}
	for triggerRows.Next() {
		var tr ErasureTrigger
		if err = triggerRows.Scan(&tr.Name, &tr.Table, &tr.SQL); err != nil {
			triggerRows.Close()
			return nil, err
		}
		triggers[tr.Table] = append(triggers[tr.Table], tr)
	}
	err = triggerRows.Err()
	triggerRows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		queue := []string{out[i].Rule.Table}
		seen := map[string]bool{}
		for len(queue) > 0 {
			parent := queue[0]
			queue = queue[1:]
			if seen[parent] {
				continue
			}
			seen[parent] = true
			out[i].Triggers = append(out[i].Triggers, triggers[parent]...)
			for _, edge := range edges[parent] {
				out[i].Children = append(out[i].Children, edge)
				queue = append(queue, edge.Table)
			}
		}
	}
	slices.SortFunc(out, func(a, b ErasureSchema) int {
		if a.Rule.Table < b.Rule.Table {
			return -1
		}
		if a.Rule.Table > b.Rule.Table {
			return 1
		}
		return 0
	})
	return out, nil
}
