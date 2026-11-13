package api

import (
	"net/http"
	"strconv"
)

// The owner sees ended keys too, so revoking one never erases what happened to it.
func (s *Server) ownerAPIKeys(r *http.Request) (Page[Key], error) {
	before, err := cursorID(r)
	if err != nil {
		return Page[Key]{}, err
	}
	limit, err := pageLimit(r, 100, 100)
	if err != nil {
		return Page[Key]{}, err
	}
	keys, err := s.db.OwnerAPIKeyHistory(r.Context(), CallerFrom(r.Context()).UserID, before, limit+1)
	out := Page[Key]{Items: make([]Key, 0, len(keys))}
	if len(keys) > limit {
		out.NextCursor = strconv.FormatInt(keys[limit-1].Key.ID, 10)
		keys = keys[:limit]
	}
	for _, row := range keys {
		key := keyOut(row.Key)
		key.State = "active"
		if !row.RevokedAt.IsZero() {
			at := row.RevokedAt.UTC()
			key.State, key.RevokedAt = "revoked", &at
		} else if !row.Key.ExpiresAt.IsZero() && !row.Key.ExpiresAt.After(s.now()) {
			key.State = "expired"
		}
		out.Items = append(out.Items, key)
	}
	return out, err
}
