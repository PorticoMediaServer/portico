package catalog

import (
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
)

type SavedResourceCandidates struct {
	ServerID    string           `json:"serverId"`
	ViewerFence string           `json:"viewerFence"`
	ResourceID  string           `json:"resourceId"`
	Revision    int64            `json:"revision"`
	Candidates  []ShareCandidate `json:"candidates"`
	NextCursor  string           `json:"nextCursor"`
}

func (s *Service) ResourceCandidates(server, fence, id string, a ResourceActor, cursor string, limit int) (SavedResourceCandidates, error) {
	out := SavedResourceCandidates{ServerID: server, ViewerFence: fence, ResourceID: id, Candidates: []ShareCandidate{}}
	role, kind, rev, deleted, e := savedResourceRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if deleted {
		return out, sql.ErrNoRows
	}
	if role != "owner" || kind != "collection" {
		return out, identity.ErrUnauthorized
	}
	out.Revision = rev
	if limit < 1 || limit > 100 {
		return out, ErrCursor
	}
	scope := cursorScope{Profile: actorKey(a), View: "resource_candidates", Entity: id, Viewer: fence, Limit: limit}
	revision := ContentRevision{Catalog: rev}
	after := ""
	if cursor != "" {
		c, e := s.decodeRevisionCursor(cursor, scope, revision)
		if e != nil {
			return out, e
		}
		after = c.Value
	}
	rows, e := s.read().Query(`SELECT authority,account_id,profile_id,display_name,json_array(authority,account_id,profile_id) k FROM (SELECT 'local' authority,p.account_id,p.id profile_id,p.name display_name FROM direct_profiles p JOIN direct_memberships dm ON dm.account_id=p.account_id WHERE p.deleted=0 AND dm.disabled=0 UNION SELECT 'hosted',json_extract(m.value,'$.accountId'),json_extract(m.value,'$.profileId'),COALESCE(json_extract(m.value,'$.profileName'),'Profile') FROM policy p,json_each(p.payload,'$.members') m WHERE p.server_id=? AND p.expires_at>? AND NOT EXISTS(SELECT 1 FROM restrictions r WHERE r.profile_id=json_extract(m.value,'$.profileId') AND r.revoked=1)) WHERE NOT(authority=? AND account_id=? AND profile_id=?) AND json_array(authority,account_id,profile_id)>? ORDER BY k LIMIT ?`, server, time.Now().UTC().Format(time.RFC3339), a.Authority, a.AccountID, a.ProfileID, after, limit+1)
	if e != nil {
		return out, e
	}
	keys := []string{}
	for rows.Next() {
		var c ShareCandidate
		var key string
		if e = rows.Scan(&c.Authority, &c.AccountID, &c.ProfileID, &c.DisplayName, &key); e != nil {
			rows.Close()
			return out, e
		}
		c.ID = operationHash([]string{server, c.Authority, c.AccountID, c.ProfileID})
		out.Candidates = append(out.Candidates, c)
		keys = append(keys, key)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Candidates) > limit {
		out.Candidates = out.Candidates[:limit]
		out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: keys[limit-1], Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
		if e != nil {
			return out, e
		}
	}
	_, _, current, dead, e := savedResourceRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if current != rev || dead {
		return out, ErrStaleContinuation
	}
	return out, nil
}
