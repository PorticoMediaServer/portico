//go:build devtrust && !release

package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The development e2e owner: a devtrust build seeds it on a throwaway state
// directory (its name contains "e2e" or "test") and signs a local browser or
// simulator in as it without a password, the server-side counterpart of
// Hosted's /dev/e2e/session. Nobody types or reads a credential: the seed's
// password is random and discarded.
const devE2EOwner = "e2e-owner"

func init() {
	devE2EUsername = func(s *Service) (string, bool) {
		if !devE2EState(s) {
			return "", false
		}
		return devE2EOwner, true
	}
	devE2ESeed = func(ctx context.Context, s *Service) error {
		if !devE2EState(s) || !s.SetupRequired() {
			return nil
		}
		token, err := s.BrowserSetupToken(ctx)
		if err != nil {
			return err
		}
		_, err = s.Setup(token, devE2EOwner, Token()+"-Aa1!", "E2E Owner")
		return err
	}
	devE2ESignIn = func(ctx context.Context, s *Service, username string) (DirectSignIn, error) {
		if !devE2EState(s) || strings.ToLower(strings.TrimSpace(username)) != devE2EOwner {
			return DirectSignIn{}, ErrUnauthorized
		}
		var c directCaller
		var libs string
		err := s.db.QueryRowContext(ctx, `SELECT a.id,a.username,a.profile_id,a.epoch,m.role,m.revision,m.disabled,m.allowed_libraries FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.username=?`, devE2EOwner).Scan(&c.account.ID, &c.account.Username, &c.account.PrimaryProfileID, &c.epoch, &c.account.Role, &c.account.Revision, &c.account.Disabled, &libs)
		if errors.Is(err, sql.ErrNoRows) || err == nil && c.account.Disabled {
			return DirectSignIn{}, ErrUnauthorized
		}
		if err != nil {
			return DirectSignIn{}, err
		}
		if json.Unmarshal([]byte(libs), &c.account.AllowedLibraries) != nil {
			return DirectSignIn{}, ErrUnauthorized
		}
		c.manage = true
		gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
		if err != nil {
			return DirectSignIn{}, err
		}
		defer gated.Rollback()
		out, err := s.directSignInTx(ctx, gated.Tx(), c, true)
		if err != nil {
			return out, err
		}
		return out, gated.Commit()
	}
}

func devE2EState(s *Service) bool {
	name := strings.ToLower(filepath.Base(filepath.Dir(filepath.Dir(s.setupPath))))
	return strings.Contains(name, "e2e") || strings.Contains(name, "test")
}
