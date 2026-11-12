package api

import "net/http"

// porticoOwnerApproved is how the owner of a Portico Accounts server, who has no password
// here, proves it is them, now, before something that cannot be undone (a restore): a fresh
// approval by the Portico Account that owns the server, started with "Link again"
// (POST /v1/admin/portico/link). The server links again with it, exactly as the finish of
// "Link again" does, because that is what Portico Services takes the approval to mean; the
// link's own rules then make the proof good once, for the owning account only, and never for
// an approval as old as the link.
func (s *Server) porticoOwnerApproved(r *http.Request, approval string) error {
	if approval == "" {
		return refusal(http.StatusForbidden, "portico_approval_required",
			"Approve this with the Portico Account that owns this server.", false)
	}
	return s.porticoRelink(r, approval)
}
