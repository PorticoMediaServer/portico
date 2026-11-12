package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/schedule"
)

// The way back for an owner who cannot sign in to a server that uses Portico Accounts: the
// link has ended, Portico Services is gone, or the Portico Account is lost. Whoever can put a
// file in the server's data folder is its owner, as at first-run setup. The owner creates an
// empty file named reset-sign-in there and restarts the server; the server then writes a
// setup code to setup-code.txt and its log, and that code, with a new username and password,
// switches the server to local sign-in. Nothing is deleted and Portico Services is not needed.

// resetSignInFile is the file the owner creates in the data folder.
const resetSignInFile = "reset-sign-in"

// LocalSignInReset switches a server to local sign-in with the code from its data folder.
type LocalSignInReset struct {
	// SetupCode is the code the server wrote to its log and to setup-code.txt in its data
	// folder after the owner created reset-sign-in there and restarted it.
	SetupCode string `json:"setupCode"`
	// Username and Password are the owner's from now on.
	Username string     `json:"username"`
	Password string     `json:"password"`
	Device   DeviceInfo `json:"device"`
}

// loadSignInReset makes the reset's code at start when the owner has asked for one. On a
// server with local sign-in the file asks for nothing and is removed.
func (s *Server) loadSignInReset(ctx context.Context) error {
	asked := filepath.Join(s.dataDir, resetSignInFile)
	if _, err := os.Stat(asked); err != nil {
		return nil
	}
	if _, linked, err := s.db.Link(ctx); err != nil {
		return err
	} else if !linked {
		s.log.Warn("the reset-sign-in file in the data folder was ignored and removed: this server already uses local sign-in")
		return os.Remove(asked)
	}
	code, file := auth.NewCode(4), filepath.Join(s.dataDir, "setup-code.txt")
	if err := os.WriteFile(file, []byte(code+"\n"), 0o600); err != nil {
		return err
	}
	s.link.resetCode = code
	s.log.Warn("the reset-sign-in file in the data folder asks to switch this server to local sign-in: use this setup code", "setupCode", code, "file", file)
	return nil
}

func (s *Server) localSignInReset(r *http.Request, req LocalSignInReset) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	username, ok := auth.Username(req.Username)
	if !ok {
		return SignInResponse{}, ErrField("username", "Choose a username of 3 to 64 characters with no spaces or @.")
	}
	if problem := auth.PasswordProblem(req.Password); problem != "" {
		return SignInResponse{}, ErrField("password", problem)
	}
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	s.link.mu.Lock()
	code := s.link.resetCode
	s.link.mu.Unlock()
	if code == "" {
		return SignInResponse{}, ErrConflict("reset_not_asked",
			"To switch this server to local sign-in without signing in, create an empty file named reset-sign-in in the server's data folder and restart it.")
	}
	// Wrong codes count against the network they came from and are slowed, as wrong
	// passwords are. The right code always gets through: a stranger sending wrong ones must
	// not be able to keep the owner out.
	if subtle.ConstantTimeCompare([]byte(auth.CodeKey(req.SetupCode)), []byte(auth.CodeKey(code))) != 1 {
		source := ""
		if addr, ok := s.networks().Client(r); ok {
			source = auth.SourceKey(addr)
		}
		limits := []db.Limit{{Bucket: "setup", Key: source, Delay: auth.StandardPasswords.Delay}}
		if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
			return SignInResponse{}, err
		} else if wait > 0 {
			return SignInResponse{}, errTooManyAttempts(wait)
		}
		if err := s.db.RecordFailure(ctx, now, limits...); err != nil {
			return SignInResponse{}, err
		}
		return SignInResponse{}, refusal(http.StatusForbidden, "setup_code_invalid",
			"That setup code is not correct. It is in the server's log and in setup-code.txt in its data folder.", false)
	}
	hash, err := s.passwords.Hash(ctx, req.Password)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.ResetToLocalSignIn(ctx, db.LocalReset{Username: username, PasswordHash: hash, GiveUp: now.Add(schedule.LeaveGiveUp), Session: session, Now: now})
	switch {
	case errors.Is(err, db.ErrConflict):
		return SignInResponse{}, ErrConflict("username_taken", "That username is already in use.")
	case errors.Is(err, db.ErrNoLink):
		return SignInResponse{}, errLocalSignIn()
	case err != nil:
		return SignInResponse{}, err
	}
	// The code has done its work: it and the file that asked for it go.
	s.link.mu.Lock()
	s.link.resetCode = ""
	s.link.mu.Unlock()
	os.Remove(filepath.Join(s.dataDir, resetSignInFile))
	os.Remove(filepath.Join(s.dataDir, "setup-code.txt"))
	s.log.Warn("this server was switched to local sign-in with the setup code from its data folder")
	// The one "this server has left" goes out now, and the certificate client is told to stop.
	s.porticoWake()
	return s.signedIn(ctx, st, now, refresh)
}
