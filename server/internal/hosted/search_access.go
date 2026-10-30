package hosted

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// SearchAccess resolves policy once, avoiding one policy parse/query per library.
// Local owner access stays unbounded by a materialized library-ID list.
func (s *Service) SearchAccess(p identity.Principal) (bool, []string, int64, int64, error) {
	if p.Authority == "local" {
		return identity.DirectAccess(s.db, p)
	}
	policy, e := s.loadFrom(s.db)
	if e != nil {
		return false, nil, 0, 0, e
	}
	var revoked int
	var raw string
	var revision int64
	e = s.db.QueryRow(`SELECT revoked,allowed_libraries,revision FROM restrictions WHERE profile_id=?`, p.ProfileID).Scan(&revoked, &raw, &revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, nil, 0, 0, e
	}
	if revoked != 0 {
		return false, nil, 0, 0, identity.ErrUnauthorized
	}
	var restricted []string
	if raw != "" && json.Unmarshal([]byte(raw), &restricted) != nil {
		return false, nil, 0, 0, identity.ErrUnauthorized
	}
	for _, m := range policy.Members {
		if m.AccountID == p.AccountID && m.ProfileID == p.ProfileID {
			all := (m.Role == "owner" || m.AllLibraries) && !m.LibraryRestricted
			ids := m.AllowedLibraries
			if raw != "" {
				if all {
					ids = restricted
				} else {
					ids = []string{}
					for _, id := range m.AllowedLibraries {
						if contains(restricted, id) {
							ids = append(ids, id)
						}
					}
				}
				all = false
			}
			return all, ids, policy.Revision, revision, nil
		}
	}
	return false, nil, 0, 0, identity.ErrUnauthorized
}
