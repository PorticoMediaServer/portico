package identity

import "github.com/PorticoMediaServer/portico/server/internal/persistence"

// PersonalKey is the lossless authority-qualified owner tuple inside this one
// server's database. Keep Principal.ProfileID unchanged for authorization.
func PersonalKey(v Viewer) string {
	return persistence.PersonalOwnerKey(v.Authority, v.AccountID, v.ProfileID)
}

// CanUsePersonalState identifies a normal selected profile. It is eligibility
// only: every operation must still re-admit the live principal and target.
func CanUsePersonalState(p Principal) bool {
	return p.Grant == "" && p.Authority != "" && p.AccountID != "" && p.ProfileID != "" && (p.Role == "owner" || p.Role == "admin" || p.Role == "member")
}
