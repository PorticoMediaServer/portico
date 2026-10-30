package identity

import (
	"context"
	"database/sql"
	"errors"
)

// The owner's choice of who may sign in from outside the home networks.
const (
	RemoteSignInAllow     = "allow"
	RemoteSignInOwnerOnly = "owner-only"
	RemoteSignInOff       = "off"
)

// ErrRemoteSignIn is the connectivity policy's refusal. It is separate from a
// bad password: the credentials may be right, the place the request came from
// is not. It is deliberately not an ErrUnauthorized, which clients read as "your
// sign-in ended".
var ErrRemoteSignIn = errors.New("This server does not accept sign-in from outside its home network.")

// SignInAdmission is what that choice means for one request: whether the
// request came from outside the home networks, and the policy in force. The
// HTTP edge decides both before any transaction opens and carries them here.
type SignInAdmission struct {
	Remote bool
	Policy string
}

type signInAdmissionKey struct{}
type continuedSignInKey struct{}

// WithSignInAdmission attaches the policy to a request. Every session is
// issued by IssueTx, which reads it, so a route that issues a session cannot
// forget the policy: there is no per-route call to leave out.
func WithSignInAdmission(ctx context.Context, admission SignInAdmission) context.Context {
	return context.WithValue(ctx, signInAdmissionKey{}, admission)
}

// continuedSignIn marks an issuance that continues a sign-in this device
// already holds (choosing or switching a profile from its own session). The
// policy governs new sign-ins; the owner's setting promises that people who
// are already signed in are left alone.
func continuedSignIn(ctx context.Context) context.Context {
	return context.WithValue(ctx, continuedSignInKey{}, true)
}

func signInAdmission(ctx context.Context) (SignInAdmission, bool) {
	admission, ok := ctx.Value(signInAdmissionKey{}).(SignInAdmission)
	return admission, ok
}

// RemoteSignInClosed reports that no account at all may start a sign-in from
// where this request came from. Credential routes ask it before they look at a
// password, a code or an assertion, so a closed server does no credential work
// for a remote caller and tells it nothing about what it sent.
func RemoteSignInClosed(ctx context.Context) bool {
	admission, ok := signInAdmission(ctx)
	if !ok || !admission.Remote {
		return false
	}
	switch admission.Policy {
	case "", RemoteSignInAllow, RemoteSignInOwnerOnly:
		return false
	}
	return true
}

// admitSignInTx applies the policy to one new session for one account, inside
// the transaction that would create it. A refusal therefore leaves nothing
// behind to revoke. "owner-only" judges the account's stored tier rather than
// the session's role: an owner's first session after the password is an
// account session, and that is still the owner signing in.
func (s *Service) admitSignInTx(ctx context.Context, tx *sql.Tx, account string) error {
	admission, ok := signInAdmission(ctx)
	if !ok || !admission.Remote || ctx.Value(continuedSignInKey{}) == true {
		return nil
	}
	switch admission.Policy {
	case "", RemoteSignInAllow:
		return nil
	case RemoteSignInOwnerOnly:
		var role string
		err := tx.QueryRowContext(ctx, `SELECT role FROM direct_memberships WHERE account_id=? AND disabled=0`, account).Scan(&role)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && role == TierOwner {
			return nil
		}
	}
	// "off", and any value this build does not know, admit nobody.
	return ErrRemoteSignIn
}

// AccountAdmitter is a rule about one account that every session created or
// renewed in a request must pass: today, the member's access schedule. It runs
// inside the issuing or renewing transaction and must not open another
// connection.
type AccountAdmitter func(ctx context.Context, tx *sql.Tx, account string) error

type accountAdmitterKey struct{}

// WithAccountAdmitter attaches the rule to a request. IssueTx and
// RefreshSession both apply it before they write anything, so a refusal
// neither creates a session nor ends one: a device outside its allowed hours
// keeps its sign-in and is admitted again when the hours return.
func WithAccountAdmitter(ctx context.Context, admit AccountAdmitter) context.Context {
	return context.WithValue(ctx, accountAdmitterKey{}, admit)
}

func admitAccountTx(ctx context.Context, tx *sql.Tx, account string) error {
	admit, _ := ctx.Value(accountAdmitterKey{}).(AccountAdmitter)
	if admit == nil {
		return nil
	}
	return admit(ctx, tx, account)
}
