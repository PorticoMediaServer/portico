package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// challengeLife is how long a password sign-in waits for its second step.
const challengeLife = 5 * time.Minute

// recoveryCodeCount is how many recovery codes an account holds.
const recoveryCodeCount = 10

// TwoFactorFinish is the second step of a sign-in.
type TwoFactorFinish struct {
	// Challenge is the token the password step answered with.
	Challenge string `json:"challenge"`
	// Code is the six digits from the authenticator app, or a recovery code.
	Code string `json:"code"`
	// RememberAccount is as in the password step.
	RememberAccount bool `json:"rememberAccount,omitempty"`
}

// TwoFactorState says whether the caller's account uses two-step sign-in.
type TwoFactorState struct {
	Enabled           bool       `json:"enabled"`
	EnabledAt         *time.Time `json:"enabledAt,omitempty"`
	RecoveryCodesLeft int        `json:"recoveryCodesLeft"`
}

// TwoFactorEnrol starts setting up an authenticator app.
type TwoFactorEnrol struct {
	CurrentPassword string `json:"currentPassword"`
}

// TwoFactorSetup is what to put into the authenticator app.
type TwoFactorSetup struct {
	// Secret is the key to type in by hand; URI is the same as a QR code's content.
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// TwoFactorVerify confirms the authenticator app works by showing a code from it.
type TwoFactorVerify struct {
	Code string `json:"code"`
}

// TwoFactorProof proves the caller twice over: their password, and an authenticator or recovery code.
type TwoFactorProof struct {
	CurrentPassword string `json:"currentPassword"`
	Code            string `json:"code"`
}

// RecoveryCodes are shown once. Each signs in once in place of an authenticator code.
type RecoveryCodes struct {
	RecoveryCodes []string `json:"recoveryCodes"`
}

func errBadSecondStep() *Error {
	return refusal(http.StatusForbidden, "two_factor_invalid", "That code is not correct.", false)
}

func (s *Server) twoFactorRoutes() {
	Handle(s, "POST /v1/auth/two-factor", Public, s.finishTwoFactor)
	// A sign-in the owner's settings hold back until two-step sign-in is set up can do
	// exactly that, and nothing else.
	Handle(s, "GET /v1/me/two-factor", SignedIn, s.twoFactorState).Enrolling = true
	Handle(s, "POST /v1/me/two-factor", SignedIn, s.enrolTwoFactor).Enrolling = true
	Handle(s, "POST /v1/me/two-factor/confirm", SignedIn, s.verifyTwoFactor).Enrolling = true
	Handle(s, "POST /v1/me/two-factor/recovery-codes", SignedIn, s.newRecoveryCodes)
	Handle(s, "DELETE /v1/me/two-factor", SignedIn, s.disableTwoFactor)
}

func recoveryHash(userID int64, typed string) string {
	return auth.Hash("recovery|" + strconv.FormatInt(userID, 10) + "|" + auth.CodeKey(typed))
}

// secondProof turns a typed code into the check made inside the transaction.
func secondProof(userID int64, code string, now time.Time) db.SecondProof {
	return db.SecondProof{
		TOTP:         func(secret []byte, lastStep int64) (int64, bool) { return auth.CheckTOTP(secret, code, now, lastStep) },
		RecoveryHash: recoveryHash(userID, code),
	}
}

func newRecoveryCodes(userID int64) (codes, hashes []string) {
	for range recoveryCodeCount {
		code := auth.NewCode(3)
		codes, hashes = append(codes, code), append(hashes, recoveryHash(userID, code))
	}
	return codes, hashes
}

func (s *Server) finishTwoFactor(r *http.Request, req TwoFactorFinish) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	session, refresh := s.mintSession(r, now)
	limits := func(username string) []db.Limit { return s.passwordLimits(r, username) }
	st, err := s.db.FinishSignIn(ctx, db.SecondStep{ChallengeHash: auth.Hash(req.Challenge), Limits: limits, Session: session, Now: now,
		Proof: func(userID int64) db.SecondProof { return secondProof(userID, req.Code, now) }})
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return SignInResponse{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadSecondStep):
		return SignInResponse{}, errBadSecondStep()
	case errors.Is(err, db.ErrBadCredentials):
		return SignInResponse{}, refusal(http.StatusUnauthorized, "unauthorized", "This sign-in took too long. Sign in again.", false)
	case errors.Is(err, db.ErrOutsideHours):
		return SignInResponse{}, errOutsideHours()
	case errors.Is(err, db.ErrAwayFromHome):
		return SignInResponse{}, errAwayFromHome()
	case err != nil:
		return SignInResponse{}, err
	}
	return s.signedInRemembered(ctx, st, now, refresh, req.RememberAccount)
}

// Everything about two-step sign-in says or changes how the account is secured, so like
// every other part of managing the account it needs the main profile: reading whether it is
// on, turning it on or off, and making recovery codes.

// enrolling is manager for the routes that set two-step sign-in up: they also take a sign-in
// that is being held back until it has.
func enrolling(r *http.Request) (Caller, error) {
	if c := CallerFrom(r.Context()); c.Enrol {
		return c, nil
	}
	return manager(r)
}

func (s *Server) twoFactorState(r *http.Request, _ None) (TwoFactorState, error) {
	caller, err := enrolling(r)
	if err != nil {
		return TwoFactorState{}, err
	}
	state, err := s.db.TwoFactor(r.Context(), caller.UserID)
	out := TwoFactorState{Enabled: state.Enabled, RecoveryCodesLeft: state.RecoveryCodesLeft}
	if state.Enabled {
		at := state.EnabledAt.UTC()
		out.EnabledAt = &at
	}
	return out, err
}

func (s *Server) enrolTwoFactor(r *http.Request, req TwoFactorEnrol) (TwoFactorSetup, error) {
	ctx := r.Context()
	if _, err := enrolling(r); err != nil {
		return TwoFactorSetup{}, err
	}
	proof, err := s.passwordProof(r, req.CurrentPassword, "")
	if err != nil {
		return TwoFactorSetup{}, err
	}
	secret := auth.NewTOTPSecret()
	err = s.db.BeginTwoFactor(ctx, proof, secret, s.now())
	switch {
	case errors.Is(err, db.ErrConflict):
		return TwoFactorSetup{}, ErrConflict("two_factor_enrolled", "Two-step sign-in is already on.")
	case err != nil:
		return TwoFactorSetup{}, proofError(err)
	}
	account, err := s.db.Account(ctx, proof.UserID)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	name, err := s.serverName(r)
	text, uri := auth.TOTPSetup(secret, name, account.Username)
	return TwoFactorSetup{Secret: text, URI: uri}, err
}

func (s *Server) verifyTwoFactor(r *http.Request, req TwoFactorVerify) (RecoveryCodes, error) {
	now := s.now()
	caller, err := enrolling(r)
	if err != nil {
		return RecoveryCodes{}, err
	}
	codes, hashes := newRecoveryCodes(caller.UserID)
	err = s.db.ConfirmTwoFactor(r.Context(), caller.UserID, func(secret []byte, lastStep int64) (int64, bool) {
		return auth.CheckTOTP(secret, req.Code, now, lastStep)
	}, hashes, s.sourceLimit(r), now)
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return RecoveryCodes{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadSecondStep):
		return RecoveryCodes{}, errBadSecondStep()
	case errors.Is(err, db.ErrConflict):
		return RecoveryCodes{}, ErrConflict("two_factor_enrolled", "Two-step sign-in is already on.")
	case errors.Is(err, db.ErrNotFound):
		return RecoveryCodes{}, errNoTwoFactor()
	case err != nil:
		return RecoveryCodes{}, err
	}
	return RecoveryCodes{RecoveryCodes: codes}, nil
}

func errNoTwoFactor() *Error {
	return ErrConflict("two_factor_missing", "Two-step sign-in is not set up.")
}

// provenTwice proves the caller, on the main profile, by password and code, and maps the
// refusals shared by the two routes that need both.
func (s *Server) provenTwice(r *http.Request, req TwoFactorProof, change func(db.PasswordProof) error) error {
	if _, err := manager(r); err != nil {
		return err
	}
	proof, err := s.passwordProof(r, req.CurrentPassword, req.Code)
	if err != nil {
		return err
	}
	if err = change(proof); errors.Is(err, db.ErrNotFound) {
		return errNoTwoFactor()
	}
	return proofError(err)
}

func (s *Server) newRecoveryCodes(r *http.Request, req TwoFactorProof) (RecoveryCodes, error) {
	var codes []string
	err := s.provenTwice(r, req, func(proof db.PasswordProof) error {
		var hashes []string
		codes, hashes = newRecoveryCodes(proof.UserID)
		return s.db.ReplaceRecoveryCodes(r.Context(), proof, hashes, s.now())
	})
	return RecoveryCodes{RecoveryCodes: codes}, err
}

func (s *Server) disableTwoFactor(r *http.Request, req TwoFactorProof) (None, error) {
	// Someone the owner requires two-step sign-in of cannot turn it off.
	if account, err := s.db.Account(r.Context(), CallerFrom(r.Context()).UserID); err != nil {
		return None{}, err
	} else if s.policy().TwoStepRequired(account.Role) {
		return None{}, refusal(http.StatusForbidden, "two_factor_required", "This server requires two-step sign-in for your account.", false)
	}
	return None{}, s.provenTwice(r, req, func(proof db.PasswordProof) error {
		return s.db.DisableTwoFactor(r.Context(), proof, s.now())
	})
}
