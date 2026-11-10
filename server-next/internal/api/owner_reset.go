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
// folder, and whoever can read that file can choose a new password for the owner. The page
// is never shown the code, and it is told the same thing whatever username was typed.

const (
	ownerResetFile = "reset-code.txt"
	ownerResetLife = 15 * time.Minute
	// ownerResetEvery is how often a new file is written at most.
	ownerResetEvery = time.Minute
)

// OwnerResetStart asks for the file. Username is what the person typed; it changes nothing.
type OwnerResetStart struct {
	Username string `json:"username,omitempty"`
}

// OwnerResetStarted says where to look, and how long until another file can be asked for.
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

func (s *Server) ownerResetRoutes() {
	Handle(s, "POST /v1/auth/owner-reset", Public, s.startOwnerReset)
	Handle(s, "POST /v1/auth/owner-reset/confirm", Public, s.finishOwnerReset)
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
	body := fmt.Sprintf("Portico\nOne-time code to reset the owner's password:\n%s\nIt works once, until %s.\nDelete this file if you did not ask for it.\n",
		code, expires.UTC().Format(time.RFC3339))
	return atomicfile.Write(path, []byte(body), 0o600)
}

func (s *Server) startOwnerReset(r *http.Request, _ OwnerResetStart) (OwnerResetStarted, error) {
	now := s.now()
	code := auth.NewCode(4)
	expires := now.Add(ownerResetLife)
	wait, err := s.db.IssueOwnerReset(r.Context(), ownerResetHash(code), expires, ownerResetEvery, now, func() error {
		return s.writeOwnerResetFile(code, expires)
	})
	if err != nil {
		return OwnerResetStarted{}, err
	}
	return OwnerResetStarted{FileName: ownerResetFile, RetryAfterMs: wait.Milliseconds()}, nil
}

func (s *Server) finishOwnerReset(r *http.Request, req OwnerResetFinish) (None, error) {
	ctx, now := r.Context(), s.now()
	if problem := s.policy().PasswordProblem(req.NewPassword); problem != "" {
		return None{}, ErrField("newPassword", problem)
	}
	limits := s.sourceLimit(r)
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
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return None{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return None{}, refusal(http.StatusUnauthorized, "unauthorized", "That code is not correct, or it has expired.", false)
	case err != nil:
		return None{}, err
	}
	os.Remove(filepath.Join(s.dataDir, ownerResetFile))
	return None{}, nil
}
