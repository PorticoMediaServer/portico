package hosted

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Reconcile against the signed policy already stored in this transaction. A
// later regrant must require a new attachment; it cannot un-revoke a family.
// Library-only changes keep the family and are enforced at resource access.
func (s *Service) revokeFamiliesOutsidePolicy(tx *sql.Tx, policy Policy) error {
	members, err := json.Marshal(policy.Members)
	if err != nil {
		return err
	}
	err = identity.RevokeFamiliesMatchingTx(context.Background(), tx, identity.RevokedMembershipRemoved, `server_id=? AND authority='hosted' AND NOT EXISTS (
  SELECT 1 FROM json_each(?) m
  WHERE json_extract(m.value,'$.accountId')=authorization_session_families.account_id
   AND json_extract(m.value,'$.profileId')=authorization_session_families.profile_id
   AND json_extract(m.value,'$.role')=authorization_session_families.role
 )`, s.identity.ID(), string(members))
	return err
}

func (s *Service) revokeRestrictedFamilies(tx *sql.Tx, account, profile string) error {
	err := identity.RevokeFamiliesMatchingTx(context.Background(), tx, identity.RevokedMembershipRemoved, `server_id=? AND authority='hosted' AND account_id=? AND profile_id=?`, s.identity.ID(), account, profile)
	return err
}
