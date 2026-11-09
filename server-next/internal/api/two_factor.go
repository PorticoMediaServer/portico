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
	Handle(s, "GET /v1/me/two-factor", SignedIn, s.twoFactorState)
	Handle(s, "POST /v1/me/two-factor", SignedIn, s.enrolTwoFactor)
	Handle(s, "POST /v1/me/two-factor/confirm", SignedIn, s.verifyTwoFactor)
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
	var limits []db.Limit
	if source, blockable := auth.Source(r.RemoteAddr); blockable {
		limits = []db.Limit{{Bucket: "source", Key: source, Delay: auth.SourceDelay}}
	}
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
	case err != nil:
		return SignInResponse{}, err
	}
	return s.signedIn(ctx, st, now, refresh)
}

func (s *Server) twoFactorState(r *http.Request, _ None) (TwoFactorState, error) {
	state, err := s.db.TwoFactor(r.Context(), CallerFrom(r.Context()).UserID)
	out := TwoFactorState{Enabled: state.Enabled, RecoveryCodesLeft: state.RecoveryCodesLeft}
	if state.Enabled {
		at := state.EnabledAt.UTC()
		out.EnabledAt = &at
	}
	return out, err
}

func (s *Server) enrolTwoFactor(r *http.Request, req TwoFactorEnrol) (TwoFactorSetup, error) {
	ctx := r.Context()
	proof, err := s.passwordProof(r, req.CurrentPassword)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	secret := auth.NewTOTPSecret()
	err = s.db.BeginTwoFactor(ctx, proof, secret, s.now())
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return TwoFactorSetup{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return TwoFactorSetup{}, errWrongCurrentPassword()
	case errors.Is(err, db.ErrConflict):
		return TwoFactorSetup{}, ErrConflict("two_factor_enrolled", "Two-step sign-in is already on.")
	case err != nil:
		return TwoFactorSetup{}, err
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
	now, caller := s.now(), CallerFrom(r.Context())
	codes, hashes := newRecoveryCodes(caller.UserID)
	limits := guessLimits(r, "two_factor", "user:"+strconv.FormatInt(caller.UserID, 10), auth.PasswordDelay)
	err := s.db.ConfirmTwoFactor(r.Context(), caller.UserID, func(secret []byte, lastStep int64) (int64, bool) {
		return auth.CheckTOTP(secret, req.Code, now, lastStep)
	}, hashes, limits, now)
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

// provenTwice maps the refusals shared by the two routes that need both proofs.
func provenTwice(err error) error {
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return errWrongCurrentPassword()
	case errors.Is(err, db.ErrBadSecondStep):
		return errBadSecondStep()
	case errors.Is(err, db.ErrNotFound):
		return errNoTwoFactor()
	}
	return err
}

func (s *Server) newRecoveryCodes(r *http.Request, req TwoFactorProof) (RecoveryCodes, error) {
	proof, err := s.passwordProof(r, req.CurrentPassword)
	if err != nil {
		return RecoveryCodes{}, err
	}
	now := s.now()
	codes, hashes := newRecoveryCodes(proof.UserID)
	if err := s.db.ReplaceRecoveryCodes(r.Context(), proof, secondProof(proof.UserID, req.Code, now), hashes, now); err != nil {
		return RecoveryCodes{}, provenTwice(err)
	}
	return RecoveryCodes{RecoveryCodes: codes}, nil
}

func (s *Server) disableTwoFactor(r *http.Request, req TwoFactorProof) (None, error) {
	proof, err := s.passwordProof(r, req.CurrentPassword)
	if err != nil {
		return None{}, err
	}
	now := s.now()
	return None{}, provenTwice(s.db.DisableTwoFactor(r.Context(), proof, secondProof(proof.UserID, req.Code, now), now))
}
