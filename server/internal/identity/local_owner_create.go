package identity

import (
	"context"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// LocalOwnerCreate creates a genuinely new local account, never a replacement
// password for an existing person. No role, libraries or Hosted identity may be supplied.
type LocalOwnerCreate struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// TransferDirectOwnershipToNewAccount creates the local account and transfers
// ownership in one security-fenced transaction. A refusal, collision or failed
// owner-change hook leaves neither an orphan member nor a partial owner change.
func (s *Service) TransferDirectOwnershipToNewAccount(ctx context.Context, bearer string, input LocalOwnerCreate, password string, expected int64) error {
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Name = strings.TrimSpace(input.Name)
	if password == "" || len(password) > 72 || expected < 1 ||
		len(input.Username) < 3 || len(input.Username) > 64 || strings.ContainsAny(input.Username, " \t\r\n@") ||
		!directName(input.Name) || !directPassword(input.Password) {
		return ErrDirectInput
	}
	// The original owner must prove their actual current password before we spend
	// any work hashing a new password. Reset-scoped empty-current proofs never apply.
	proof, err := s.proveCurrentPassword(ctx, bearer, password)
	if err != nil {
		return err
	}
	if err = s.takeHashSlot(ctx); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), PasswordCost)
	<-s.hashSlots
	if err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	caller, err := s.checkCurrentPasswordProofTx(ctx, tx, bearer, proof)
	if err != nil {
		return err
	}
	if caller.account.Role != TierOwner {
		return ErrForbidden
	}
	if caller.account.Revision != expected {
		return ErrProfileChanged
	}
	if err = ValidateNewLocalPasswordTx(ctx, tx, input.Password); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE username=?)`, input.Username).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrConflict
	}
	accountID, profileID := Token(), Token()
	// Existing account triggers install the normal membership, profile and defaults.
	// This is the same local account model as CreateDirectMember, within the transfer.
	if _, err = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,?,?)`, accountID, input.Username, hash, profileID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE direct_profiles SET name=? WHERE id=? AND account_id=?`, input.Name, profileID, accountID); err != nil {
		return err
	}
	// Reuses exactly the existing demotion, family retiering and transactional
	// OnOwnerChangedTx boundary; the former owner becomes Member.
	if err = s.transferOwnershipTx(ctx, tx, caller, accountID, expected); err != nil {
		return err
	}
	return gated.Commit()
}
