package api

import (
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// porticoForgotPIN replaces a forgotten PIN on a server that uses Portico Accounts, where the
// account has no password here to prove (area 1 rule 30: Forgot PIN asks for the account's
// password). The proof is the account's own Portico Account, with the password checked now by Services:
//   - a sign-in note Portico Services signed for this server's own new challenge, checked
//     against the root key the server was built with, as a sign-in's is: the challenge is
//     at most a few minutes old and is spent, so a note is good for one change;
//   - carrying Services’ signed passwordChecked mark, after checking the typed password
//     and the two-step code when on; a fresh challenge alone is not enough;
//   - for the Portico Account this account signs in with, and no other;
//   - for a profile of the caller's own account: as with the password, a person replaces their
//     own account's PINs this way, and nobody else's. An owner or administrator changes a
//     member's PIN as before.
//
// As with any new PIN, the account's other devices on that profile go back to the profile
// chooser, where the new PIN is asked for; this device stays on the profile it is on.
func (s *Server) porticoForgotPIN(r *http.Request, req ProfilePIN) (Profile, error) {
	ctx, now, caller := r.Context(), s.now(), CallerFrom(r.Context())
	if caller.UserID == 0 || caller.SessionID == 0 {
		return Profile{}, ErrNotPermitted()
	}
	account, _, err := s.porticoProven(r, req.Challenge, req.Identity, now)
	if err != nil {
		return Profile{}, err
	}
	if !account.PasswordChecked {
		return Profile{}, ErrNotPermitted()
	}
	if mine, err := s.db.PorticoAccountID(ctx, caller.UserID); err != nil {
		return Profile{}, err
	} else if mine != account.ID {
		return Profile{}, refusal(http.StatusForbidden, "not_your_portico_account",
			"Sign in with the Portico Account this account uses on this server.", false)
	}
	hash, err := s.passwords.HashPIN(ctx, req.PIN)
	if err != nil {
		return Profile{}, err
	}
	p, err := s.db.UpdateProfile(ctx, db.ProfileEdit{UserID: caller.UserID, PublicID: r.PathValue("id"), PINHash: &hash,
		SessionID: caller.SessionID, Now: now})
	if errors.Is(err, db.ErrNotFound) {
		return Profile{}, ErrNotFound()
	}
	return profileOut(p), err
}
