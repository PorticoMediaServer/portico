package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An owner who cannot sign in has nobody to ask for a reset code. What they do have is the
// machine: the sign-in page asks the server to write a one-time code to a file in its data
// folder. Its reader can reset a local password or enter as the Portico Account owner. The page
// is never shown the code, and it is told the same thing whatever username was typed.
//
// Nothing that reaches the server over the network alone resets the owner: a request can make
// the server write the file, and that is all it can do. Using the code takes reading a file
// only the server's own account can read, in the data folder on the machine. The code is 20
// characters from auth.CodeAlphabet (31 symbols, about 99 bits), works once, for 15 minutes, and five wrong tries cancel it;
// wrong tries also slow their network down as wrong passwords do. Writing the file and using
// it are both in the server's log and in the owner's activity record.

const (
	ownerResetFile = "reset-code.txt"
	ownerResetLife = 15 * time.Minute
	// ownerResetEvery is how soon after a code was made or ended a new file can be written.
	// While a code is live, none is.
	ownerResetEvery = time.Minute
)

// OwnerResetStart asks for the file. Username is what the person typed; it changes nothing.
type OwnerResetStart struct {
	Username string `json:"username,omitempty"`
}

// OwnerResetStarted says where to look. It is the same answer every time: it does not say
// whether a file was written just now, is already there, or cannot be written yet.
type OwnerResetStarted struct {
	// FileName is the file's name inside the server's data folder.
	FileName     string `json:"fileName"`
	RetryAfterMs int64  `json:"retryAfterMs"`
}

// OwnerResetFinish chooses the owner's new password with the code from the file.
type OwnerResetFinish struct {
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
	// TurnOffTwoStep also turns the owner's two-step sign-in off, for an owner who lost
	// their authenticator as well.
	TurnOffTwoStep bool `json:"turnOffTwoStep,omitempty"`
}

// OwnerResetSessionRequest enters a Portico Account server with disk proof alone.
type OwnerResetSessionRequest struct {
	Code   string     `json:"code"`
	Device DeviceInfo `json:"device"`
}

func (s *Server) ownerResetRoutes() {
	Handle(s, "POST /v1/auth/owner-reset", Public, s.startOwnerReset)
	Handle(s, "POST /v1/auth/owner-reset/confirm", Public, s.finishOwnerReset)
	Handle(s, "POST /v1/auth/owner-reset/session", Public, s.ownerResetSession)
}

func ownerResetHash(typed string) string { return auth.Hash("owner-reset|" + auth.CodeKey(typed)) }

// writeOwnerResetFile puts the code where only someone with access to the machine can read
// it: a file for its owner alone, in the data folder the server keeps to itself. Anything
// else already at that name (a link, a folder) is left alone and the reset is refused.
func (s *Server) writeOwnerResetFile(code string, expires time.Time) error {
	path := filepath.Join(s.dataDir, ownerResetFile)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("the reset code file's name is taken by something that is not a file")
	}
	body := fmt.Sprintf("Portico\nOne-time code to recover the server owner:\n%s\nIt works once, until %s.\nDeleting this file does not cancel a copied code.\n",
		code, expires.UTC().Format(time.RFC3339))
	return atomicfile.Write(path, []byte(body), 0o600)
}

func (s *Server) startOwnerReset(r *http.Request, _ OwnerResetStart) (OwnerResetStarted, error) {
	now := s.now()
	code := auth.NewCode(5)
	expires := now.Add(ownerResetLife)
	_, err := s.db.IssueOwnerReset(r.Context(), ownerResetHash(code), expires, ownerResetEvery, now, func() error {
		if err := s.writeOwnerResetFile(code, expires); err != nil {
			s.log.Error("owner reset: could not write the reset file")
			return refusal(http.StatusServiceUnavailable, "owner_reset_file_unavailable", "Portico couldn't write the reset file. Check that the data folder can be written to.", true)
		}
		s.log.Warn("owner reset: a code was written to " + ownerResetFile +
			" in the data folder. It works once, for 15 minutes. Deleting the file does not cancel a copied code.")
		return nil
	})
	if err != nil {
		return OwnerResetStarted{}, err
	}
	return OwnerResetStarted{FileName: ownerResetFile, RetryAfterMs: ownerResetEvery.Milliseconds()}, nil
}

func (s *Server) finishOwnerReset(r *http.Request, req OwnerResetFinish) (None, error) {
	ctx, now := r.Context(), s.now()
	if problem := auth.PasswordProblem(req.NewPassword); problem != "" {
		return None{}, ErrField("newPassword", problem)
	}
	limits := s.ownerResetLimits(r)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return None{}, err
	} else if wait > 0 {
		return None{}, errTooManyAttempts(wait)
	}
	hash, err := s.passwords.Hash(ctx, req.NewPassword)
	if err != nil {
		return None{}, err
	}
	err = s.db.RedeemOwnerReset(ctx, ownerResetHash(req.Code), hash, req.TurnOffTwoStep, limits, now)
	if err := ownerResetError(err); err != nil {
		return None{}, err
	}
	os.Remove(filepath.Join(s.dataDir, ownerResetFile))
	s.log.Warn("owner reset: the owner's password was changed with the code from " + ownerResetFile +
		" in the data folder. The code was spent and the owner's devices were signed out.")
	return None{}, nil
}

// Count wrong codes against the network, including home, without binding a correct
// disk proof to the network that asked for it.
func (s *Server) ownerResetLimits(r *http.Request) []db.Limit {
	source := ""
	if addr, ok := s.networks().Client(r); ok {
		source = auth.SourceKey(addr)
	}
	return append([]db.Limit{{Bucket: "owner_reset", Key: source, Delay: auth.StandardPasswords.Delay}}, s.sourceLimit(r)...)
}

func ownerResetError(err error) error {
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return refusal(http.StatusUnauthorized, "unauthorized", "That code is not correct, or it has expired.", false)
	default:
		return err
	}
}

func (s *Server) ownerResetSession(r *http.Request, req OwnerResetSessionRequest) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	address := "unknown"
	if addr, ok := s.networks().Client(r); ok {
		address = addr.String()
	}
	st, err := s.db.OwnerResetSession(ctx, ownerResetHash(req.Code), address, session, s.ownerResetLimits(r), now)
	if err := ownerResetError(err); err != nil {
		return SignInResponse{}, err
	}
	os.Remove(filepath.Join(s.dataDir, ownerResetFile))
	s.log.Warn("owner reset: the owner entered with the reset file; previous owner sessions were signed out")
	return s.signedIn(ctx, st, now, refresh)
}
