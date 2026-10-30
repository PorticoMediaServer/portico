package catalog

import (
	"crypto/sha256"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
)

type ShareCandidate struct {
	ResourceActor
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}
type PlaylistCandidates struct {
	ServerID    string           `json:"serverId"`
	ViewerFence string           `json:"viewerFence"`
	PlaylistID  string           `json:"playlistId"`
	Revision    int64            `json:"revision"`
	Candidates  []ShareCandidate `json:"candidates"`
	NextCursor  string           `json:"nextCursor"`
}

func (s *Service) PlaylistCandidates(server, fence, id string, a ResourceActor, cursor string, limit int) (PlaylistCandidates, error) {
	out := PlaylistCandidates{ServerID: server, ViewerFence: fence, PlaylistID: id, Candidates: []ShareCandidate{}}
	resource, e := s.Playlist(server, fence, id, a)
	if e != nil {
		return out, e
	}
	if resource.Role != "owner" {
		return out, identity.ErrUnauthorized
	}
	if limit < 1 || limit > 100 {
		return out, ErrCursor
	}
	out.Revision = resource.Revision
	rev := ContentRevision{Catalog: resource.Revision}
	scope := cursorScope{View: "share_candidates", Entity: id, Profile: a.ProfileID, Viewer: fence, Limit: limit}
	afterKey := ""
	if cursor != "" {
		c, err := s.decodeRevisionCursor(cursor, scope, rev)
		if err != nil {
			return out, err
		}
		afterKey = c.Value
	}
	rows, e := s.read().Query(`SELECT authority,account_id,profile_id,display_name,json_array(authority,account_id,profile_id) k FROM (SELECT 'local' authority,p.account_id,p.id profile_id,p.name display_name FROM direct_profiles p JOIN direct_memberships dm ON dm.account_id=p.account_id WHERE p.deleted=0 AND dm.disabled=0 UNION SELECT 'hosted',json_extract(m.value,'$.accountId'),json_extract(m.value,'$.profileId'),COALESCE(json_extract(m.value,'$.profileName'),'Profile') FROM policy p,json_each(p.payload,'$.members') m WHERE p.server_id=? AND p.expires_at>? AND NOT EXISTS(SELECT 1 FROM restrictions r WHERE r.profile_id=json_extract(m.value,'$.profileId') AND r.revoked=1)) WHERE NOT(authority=? AND account_id=? AND profile_id=?) AND json_array(authority,account_id,profile_id)>? ORDER BY k LIMIT ?`, server, time.Now().UTC().Format(time.RFC3339), a.Authority, a.AccountID, a.ProfileID, afterKey, limit+1)
	if e != nil {
		return out, e
	}
	keys := []string{}
	for rows.Next() {
		var v ShareCandidate
		var key string
		if e = rows.Scan(&v.Authority, &v.AccountID, &v.ProfileID, &v.DisplayName, &key); e != nil {
			rows.Close()
			return out, e
		}
		v.ID = fmt.Sprintf("%x", sha256.Sum256([]byte(server+"\x00"+v.Authority+"\x00"+v.AccountID+"\x00"+v.ProfileID)))
		out.Candidates = append(out.Candidates, v)
		keys = append(keys, key)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Candidates) > limit {
		out.Candidates = out.Candidates[:limit]
		out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: keys[limit-1], Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return out, e
		}
	}
	_, after, deleted, e := playlistRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if deleted || after != resource.Revision {
		return out, ErrStaleContinuation
	}
	return out, nil
}
