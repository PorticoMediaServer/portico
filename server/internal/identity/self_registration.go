package identity

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const (
	// SelfRegistrationOff is the default: only the owner creates accounts.
	SelfRegistrationOff = "off"
	// SelfRegistrationInvite accepts a new account only against an invitation the
	// owner already issued. Invitations themselves are owned by the admin surface;
	// this server only refuses to create an account without one.
	SelfRegistrationInvite = "invite-only"
	// SelfRegistrationOpen lets anyone who can reach the server create a member
	// account. It never creates an owner and never widens library access.
	SelfRegistrationOpen = "open"
)

const selfRegistrationKey = "identity.selfRegistration"

type RegistrationPolicy struct {
	SelfRegistration string `json:"selfRegistration"`
}

// RegistrationPolicySnapshot reads the two owner choices from one database
// snapshot, so a concurrent save cannot present a mixed policy.
func (s *Service) RegistrationPolicySnapshot(ctx context.Context) (RegistrationPolicy, error) {
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return RegistrationPolicy{}, err
	}
	defer gated.Rollback()
	value := func(key string) (string, error) {
		var out string
		err := gated.Tx().QueryRowContext(ctx, `SELECT value FROM configuration WHERE key=?`, key).Scan(&out)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return out, err
	}
	self, err := value(selfRegistrationKey)
	if err != nil {
		return RegistrationPolicy{}, err
	}
	if !validSelfRegistration(self) {
		self = SelfRegistrationOff
	}
	return RegistrationPolicy{SelfRegistration: self}, nil
}

// SaveRegistrationPolicy validates both choices before writing either one.
func (s *Service) SaveRegistrationPolicy(ctx context.Context, policy RegistrationPolicy) (RegistrationPolicy, error) {
	if !validSelfRegistration(policy.SelfRegistration) {
		return RegistrationPolicy{}, ErrDirectInput
	}
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		for key, value := range map[string]string{selfRegistrationKey: policy.SelfRegistration} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	return policy, err
}

var ErrRegistrationClosed = errors.New("This server is not accepting new accounts.")
var ErrRegistrationTaken = errors.New("That username is already in use.")
var ErrRegistrationInvite = errors.New("A valid invitation is required to create an account here.")

func validSelfRegistration(v string) bool {
	switch v {
	case SelfRegistrationOff, SelfRegistrationInvite, SelfRegistrationOpen:
		return true
	}
	return false
}

// SelfRegistration reads the owner's current policy. An unset or corrupt value is
// read as off: a policy the owner never chose must not open the server.
func (s *Service) SelfRegistration() string {
	value := persistence.Get(s.db, selfRegistrationKey)
	if !validSelfRegistration(value) {
		return SelfRegistrationOff
	}
	return value
}

// SetSelfRegistration records the owner's policy. The caller must already have
// proven owner authority; this function does not re-prove it.
func (s *Service) SetSelfRegistration(mode string) error {
	if !validSelfRegistration(mode) {
		return ErrDirectInput
	}
	return persistence.Set(s.db, selfRegistrationKey, mode)
}

// SelfRegistrationRequest is what an unauthenticated client posts. InvitationCode
// is carried through to the admin invitation surface when the policy is
// invite-only; this package never mints or reads invitations itself.
type SelfRegistrationRequest struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	InvitationCode string `json:"invitationCode,omitempty"`
}

// InvitationRedeemer is installed by whoever owns invitations. It spends the
// code for the account being created, inside the registration's transaction,
// and returns the libraries the invitation grants: the invitation is consumed
// if and only if the account is created. When it is absent, invite-only
// registration refuses every attempt rather than silently degrading to open
// registration.
type InvitationRedeemer func(ctx context.Context, tx *sql.Tx, code, account string) ([]string, error)

// selfRegistrationPlan bounds signing up: the account, its membership, primary
// profile and any invitation limits, and its first sign-in, in one commit.
var selfRegistrationPlan = dbwork.WritePlan{Name: "self-registration", Rows: 160, Pages: 128, VMOps: 250_000,
	Why: "signing up creates the account, its membership, primary profile and limits and signs it in together, so nobody is ever half-registered"}

// Register creates a member account under the owner's self-registration policy and
// signs it in. A new account is always a member with no libraries: reaching the
// server is not evidence of what its owner wants shared.
func (s *Service) Register(ctx context.Context, q SelfRegistrationRequest, private bool) (DirectSignIn, error) {
	if err := s.RequireSignInKind(ctx, SignInLocal); err != nil {
		return DirectSignIn{}, err
	}
	mode := s.SelfRegistration()
	if mode == SelfRegistrationOff || s.SetupRequired() {
		return DirectSignIn{}, ErrRegistrationClosed
	}
	if RemoteSignInClosed(ctx) {
		return DirectSignIn{}, ErrRemoteSignIn
	}
	username := strings.ToLower(strings.TrimSpace(q.Username))
	if username == "" || len(username) > 64 || !directName(username) || strings.ContainsAny(username, " /\\:@") {
		return DirectSignIn{}, ErrDirectInput
	}
	if !directPassword(q.Password) {
		return DirectSignIn{}, ErrDirectInput
	}
	libraries := []string{}
	if mode == SelfRegistrationInvite && (s.RedeemInvitation == nil || strings.TrimSpace(q.InvitationCode) == "") {
		return DirectSignIn{}, ErrRegistrationInvite
	}
	if slotErr := s.takeHashSlot(ctx); slotErr != nil {
		return DirectSignIn{}, slotErr
	}
	defer func() { <-s.hashSlots }()
	hash, e := bcrypt.GenerateFromPassword([]byte(q.Password), PasswordCost)
	if e != nil {
		return DirectSignIn{}, e
	}
	gated, e := dbwork.Begin(dbwork.WithPlan(ctx, selfRegistrationPlan), s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectSignIn{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	// The policy is re-read inside the transaction: an owner closing registration
	// must not race with a request that read the old value.
	var stored string
	_ = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key=?`, selfRegistrationKey).Scan(&stored)
	if stored != mode {
		return DirectSignIn{}, ErrRegistrationClosed
	}
	var taken bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE username=?)`, username).Scan(&taken); e != nil {
		return DirectSignIn{}, e
	}
	if taken {
		return DirectSignIn{}, ErrRegistrationTaken
	}
	if e = RequireSignInKindTx(ctx, tx, SignInLocal); e != nil {
		return DirectSignIn{}, e
	}
	if e = ValidateNewLocalPasswordTx(ctx, tx, q.Password); e != nil {
		return DirectSignIn{}, e
	}
	account, profile := Token(), Token()
	if _, e = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,?,?,1)`, account, username, hash, profile); e != nil {
		return DirectSignIn{}, e
	}
	if mode == SelfRegistrationInvite {
		// The invitation is spent here, after the username was found free and
		// in the transaction that creates the account: a refusal anywhere
		// leaves the invitation unspent.
		granted, e := s.RedeemInvitation(ctx, tx, q.InvitationCode, account)
		if e != nil {
			return DirectSignIn{}, e
		}
		if !directLibraries(granted) {
			return DirectSignIn{}, ErrRegistrationInvite
		}
		libraries = granted
	}
	// The accounts trigger installs the membership and the primary profile. An
	// account created this way is a member even if it somehow arrives first.
	if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET role='member',allowed_libraries=? WHERE account_id=? AND EXISTS(SELECT 1 FROM direct_memberships WHERE role='owner' AND account_id<>?)`, marshalLibraries(libraries), account, account); e != nil {
		return DirectSignIn{}, e
	}
	c, e := s.directAccountTx(ctx, tx, account)
	if e != nil {
		return DirectSignIn{}, e
	}
	if c.account.Role != "member" {
		return DirectSignIn{}, ErrRegistrationClosed
	}
	out, e := s.directSignInTx(ctx, tx, c, private)
	if e != nil {
		return DirectSignIn{}, e
	}
	return out, gated.Commit()
}

func marshalLibraries(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(id)
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String()
}
