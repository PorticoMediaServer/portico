package identity

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A profile PIN separates viewing profiles. Only an account-management session
// can clear it; a selected child profile's viewer session cannot.

// PINRecoveryMethods is the pre-flight descriptor a client renders before asking
// for anything. It never reveals whether a specific profile has a PIN. A PIN
// reset itself needs only the account-management session (PINReset); the
// fields report the account's sign-in factors. There is no emailed token: the
// email policy sends mail only for account setup, invitations and password
// reset (C59).
type PINRecoveryMethods struct {
	Password      bool `json:"password"`
	Authenticator bool `json:"authenticator"`
	RecoveryCode  bool `json:"recoveryCode"`
}

// PINRecovery reports which confirmations this account can satisfy.
func (s *Service) PINRecovery(ctx context.Context, bearer string) (PINRecoveryMethods, error) {
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return PINRecoveryMethods{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return PINRecoveryMethods{}, e
	}
	out := PINRecoveryMethods{Password: true}
	confirmed, e := s.accountFactorRequiredTx(ctx, tx, c.account.ID)
	if e != nil {
		return out, e
	}
	out.Authenticator = confirmed
	if confirmed {
		var remaining int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM identity_recovery_codes WHERE account_id=? AND used=0`, c.account.ID).Scan(&remaining); e != nil {
			return out, e
		}
		out.RecoveryCode = remaining > 0
	}
	return out, nil
}

// Hosted reports the canonical selected sign-in kind. Claim, route and Services
// availability are separate authorities; none can introduce a password fallback
// or turn a Local server into a Portico sign-in producer.
func (s *Service) Hosted() bool {
	kind, err := s.SignInKind(context.Background())
	return err == nil && kind == SignInPortico
}

// PINReset clears or replaces a profile PIN for an account manager's current
// session. A selected child profile has viewing authority only.
//
// Resetting bumps pin_revision, which retires every remembered device trust and
// every viewing family for the profile through the existing family hook.
func (s *Service) PINReset(ctx context.Context, bearer, profile, pin string) (DirectSnapshot, error) {
	if !validFamilyID(profile) || pin != "" && !validPIN(pin) {
		return DirectSnapshot{}, ErrDirectInput
	}
	var hash []byte
	var e error
	if pin != "" {
		if slotErr := s.takeHashSlot(ctx); slotErr != nil {
			return DirectSnapshot{}, slotErr
		}
		defer func() { <-s.hashSlots }()
		if hash, e = bcrypt.GenerateFromPassword([]byte(pin), PINCost); e != nil {
			return DirectSnapshot{}, e
		}
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectSnapshot{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return DirectSnapshot{}, e
	}
	var deleted int
	if e = tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, c.account.ID, profile).Scan(&deleted); e != nil || deleted != 0 {
		return DirectSnapshot{}, ErrNotVisible
	}
	var value any
	if len(hash) > 0 {
		value = hash
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET pin_hash=?,pin_revision=pin_revision+1,revision=revision+1 WHERE id=?`, value, profile); e != nil {
		return DirectSnapshot{}, e
	}
	if e = s.retireProfilePINGrantsTx(ctx, tx, c.account.ID, profile); e != nil {
		return DirectSnapshot{}, e
	}
	if len(hash) > 0 {
		if e = gateProfileFamiliesTx(ctx, tx, c.account.ID, profile, c.familyID, false); e != nil {
			return DirectSnapshot{}, e
		}
	}
	out, e := s.directSnapshotTx(ctx, tx, c)
	if e != nil {
		return out, e
	}
	return out, gated2.Commit()
}

// Every PIN change retires both collected credentials and approvals that have
// not yet minted a viewing family. Other profiles and ordinary logout keep
// their own grant lifetimes.
func (s *Service) retireProfilePINGrantsTx(ctx context.Context, tx *sql.Tx, account, profile string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM direct_profile_pin_attempts WHERE profile_id=?`, profile); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM topshelf_tokens WHERE account_id=? AND profile_id=?`, account, profile); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE quick_connect_v1 SET status='cancelled',grant_cipher=NULL,user_hash=NULL WHERE approver_id=? AND status='approved' AND json_extract(CAST(grant_cipher AS TEXT),'$.profileId')=?`, account, profile); err != nil {
		return err
	}
	return s.revokeDirectTx(ctx, tx, account, profile, "")
}

// AuthCapabilities is the public, pre-authentication descriptor. A client reads it
// before showing any sign-in affordance so it never offers a method this server
// does not have. It answers only about the server, never about an account: asking
// for a username first would turn it into an account-existence oracle.
type AuthCapabilities struct {
	ServerID         string   `json:"serverId"`
	ServerName       string   `json:"serverName"`
	SetupRequired    bool     `json:"setupRequired"`
	Methods          []string `json:"methods"`
	SelfRegistration string   `json:"selfRegistration"`
	QuickConnect     bool     `json:"quickConnect"`
	TwoFactor        bool     `json:"twoFactor"`
	HostedAttach     bool     `json:"hostedAttach"`
}

// Capabilities describes the sign-in methods this server exposes.
func (s *Service) Capabilities(ctx context.Context) (AuthCapabilities, error) {
	out := AuthCapabilities{ServerID: s.ID(), ServerName: s.Name(), SetupRequired: s.SetupRequired(), TwoFactor: true, SelfRegistration: SelfRegistrationOff, Methods: []string{}}
	out.HostedAttach = s.Hosted()
	policyRegistration := s.SelfRegistration()
	read, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer read.Rollback()
	kind, e := SignInKindTx(ctx, read.Tx())
	if e != nil {
		return out, e
	}
	var ready bool
	if e = read.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts)`).Scan(&ready); e != nil {
		return out, e
	}
	out.TwoFactor = kind == SignInLocal
	out.QuickConnect = ready && kind == SignInLocal
	if kind == SignInLocal {
		out.Methods = append(out.Methods, "password")
		if out.QuickConnect {
			out.Methods = append(out.Methods, "quick-connect")
		}
		if ready {
			out.SelfRegistration = policyRegistration
			if policyRegistration != SelfRegistrationOff {
				out.Methods = append(out.Methods, "self-registration")
			}
		}
	} else if ready {
		out.Methods = append(out.Methods, "hosted")
	}

	return out, nil
}

// PruneIdentityProofs reclaims expired sign-in challenges and Top Shelf tokens.
// Expiry is authoritative on every read, so this
// only reclaims rows; skipping it would never admit anything.
func (s *Service) PruneIdentityProofs(ctx context.Context) error {
	return persistence.PruneIdentityProofs(ctx, s.db, time.Now().UTC().Format(time.RFC3339))
}
