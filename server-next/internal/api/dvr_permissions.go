package api

import (
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// DvrPermission is one profile's recording choice and its account's ceiling.
type DvrPermission struct {
	AccountID       string `json:"accountId"`
	AccountName     string `json:"accountName"`
	ProfileID       string `json:"profileId"`
	ProfileName     string `json:"profileName"`
	Authority       string `json:"authority"`
	AccountAllowed  bool   `json:"accountAllowed"`
	ProfileAllowed  bool   `json:"profileAllowed"`
	LiveAllowed     bool   `json:"liveAllowed"`
	CanRecord       bool   `json:"canRecord"`
	OwnerAccount    bool   `json:"ownerAccount"`
	AccountRevision int64  `json:"accountRevision"`
	Revision        int64  `json:"revision"`
}

// DvrPermissionChange changes the account when profileId is absent, otherwise one profile.
// Revision is that accountRevision or profile revision from the displayed read.
type DvrPermissionChange struct {
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId,omitempty"`
	Enabled   bool   `json:"enabled"`
	Revision  int64  `json:"revision"`
}

func (s *Server) dvrPermissionRoutes() {
	Handle(s, "GET /v1/admin/dvr/permissions", Owner, s.readDvrPermissions)
	Handle(s, "PATCH /v1/admin/dvr/permissions", Owner, s.changeDvrPermission)
}
func (s *Server) readDvrPermissions(r *http.Request, _ None) (Page[DvrPermission], error) {
	rows, err := s.db.DvrPermissions(r.Context())
	out := Page[DvrPermission]{Items: []DvrPermission{}}
	for _, row := range rows {
		out.Items = append(out.Items, DvrPermission{
			AccountID: row.AccountID, AccountName: row.AccountName, ProfileID: row.ProfileID, ProfileName: row.ProfileName,
			Authority: row.Authority, AccountAllowed: row.AccountAllowed, ProfileAllowed: row.ProfileAllowed,
			LiveAllowed: row.LiveAllowed, CanRecord: row.CanRecord, OwnerAccount: row.OwnerAccount,
			AccountRevision: row.AccountRevision, Revision: row.Revision,
		})
	}
	return out, err
}
func (s *Server) changeDvrPermission(r *http.Request, req DvrPermissionChange) (Page[DvrPermission], error) {
	if req.AccountID == "" || req.Revision < 1 {
		return Page[DvrPermission]{}, ErrInvalid("Choose an account or profile from the current recording permissions.")
	}
	err := s.db.SetDvrPermission(r.Context(), CallerFrom(r.Context()).UserID, req.AccountID, req.ProfileID, req.Enabled, req.Revision, s.now())
	if errors.Is(err, db.ErrStale) {
		return Page[DvrPermission]{}, refusal(http.StatusPreconditionFailed, "changed_elsewhere", "These recording permissions changed somewhere else. Read them again.", false)
	}
	if err != nil {
		return Page[DvrPermission]{}, memberError(err)
	}
	return s.readDvrPermissions(r, None{})
}
