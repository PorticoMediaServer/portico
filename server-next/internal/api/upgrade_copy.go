package api

import "net/http"

// The copy of the database kept before an upgrade is the way back to the version that was
// installed before. The next upgrade removes it by itself; the owner may remove it sooner, to
// free its space once the new version works well, and then there is no way back.

func (s *Server) upgradeCopyRoutes() {
	Handle(s, "DELETE /v1/admin/upgrade-copy", Owner, func(r *http.Request, _ None) (None, error) {
		removed, err := s.db.RemoveUpgradeCopy()
		if err != nil || !removed {
			return None{}, err
		}
		return None{}, s.db.Record(r.Context(), CallerFrom(r.Context()).UserID, "upgrade_copy.delete", "", "", s.now())
	})
}
