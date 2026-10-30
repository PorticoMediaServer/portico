package social

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Activity privacy anonymizes a participant, rather than hiding room occupancy
// or removing someone from the readiness barrier.
func watchTogetherVisible(alias string) string {
	return `COALESCE((SELECT v.bool_value FROM preference_values v WHERE v.authority=` + alias + `.authority AND v.account_id=` + alias + `.account_id AND v.profile_id=` + alias + `.profile_id AND v.scope='profile-server' AND v.device_key='' AND v.key='privacy.includeInWatchTogether'),1)<>0`
}

// activityPrivacy is one member's name as the room sees it and whether they
// share their activity.
type activityPrivacy struct {
	public   bool
	name     string
	username string
}

// roomPrivacy reads, in one statement, how every member of a room appears, so
// a catch-up over many readiness events costs one read rather than one per event.
func roomPrivacy(ctx context.Context, tx *sql.Tx, group string) (map[string]activityPrivacy, error) {
	rows, e := tx.QueryContext(ctx, `SELECT id,`+watchTogetherVisible("m")+`,display_name,COALESCE((SELECT username FROM accounts WHERE id=m.account_id),'') FROM social_group_members m WHERE group_id=?`, group)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]activityPrivacy{}
	for rows.Next() {
		var id string
		var p activityPrivacy
		if e = rows.Scan(&id, &p.public, &p.name, &p.username); e != nil {
			return nil, e
		}
		out[id] = p
	}
	return out, rows.Err()
}

// projectActivityWith is projectActivity over privacy already read for the room.
func projectActivityWith(room map[string]activityPrivacy, payload json.RawMessage) (json.RawMessage, error) {
	var body map[string]any
	if e := json.Unmarshal(payload, &body); e != nil {
		return nil, e
	}
	member, _ := body["memberId"].(string)
	if member == "" {
		return payload, nil
	}
	p, ok := room[member]
	if !ok {
		return nil, sql.ErrNoRows
	}
	body["private"] = !p.public
	body["displayName"] = p.name
	delete(body, "username")
	if p.public && p.username != "" {
		body["username"] = p.username
	}
	if !p.public {
		body["displayName"] = "Viewer"
		body["positionUs"] = "0"
		body["reportedAt"] = nil
	}
	return json.Marshal(body)
}
