package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// A shared device's sign-in screen can list the accounts that chose to be remembered on it.
// The list lives on the device: each remembered account left it a token at sign-in, and
// forgetting one is the device deleting its token. The server keeps nothing. This route
// turns the tokens a device holds into names to show; picking a name still needs that
// account's password or the device's saved sign-in.

// RememberedRequest is a device asking who its account tokens belong to.
type RememberedRequest struct {
	InstallationID string `json:"installationId"`
	// AccountTokens are the accountToken values the device kept, 16 at most.
	AccountTokens []string `json:"accountTokens"`
}

// RememberedAccount is one account to list on the sign-in screen.
type RememberedAccount struct {
	AccountID string `json:"accountId"`
	Username  string `json:"username"`
	// Name and Color are those of the account's main profile.
	Name  string `json:"name"`
	Color string `json:"color"`
	// AccountToken is the token this entry came from, so the device can drop the others.
	AccountToken string `json:"accountToken"`
}

// RememberedAccounts lists the accounts whose tokens were good. It is empty when the owner
// has turned remembered accounts off.
type RememberedAccounts struct {
	Accounts []RememberedAccount `json:"accounts"`
}

const maxRememberedAccounts = 16

func (s *Server) rememberedAccounts(r *http.Request, req RememberedRequest) (RememberedAccounts, error) {
	out := RememberedAccounts{Accounts: []RememberedAccount{}}
	if len(req.AccountTokens) > maxRememberedAccounts || !auth.ValidToken(req.InstallationID, 32) {
		return out, ErrInvalid("Send this device's id and at most 16 account tokens.")
	}
	if !s.policy().RememberAccounts {
		return out, nil
	}
	for _, token := range req.AccountTokens {
		var kept accountMemory
		if !s.tokens.Recall("account", token, &kept) || kept.Installation != req.InstallationID {
			continue
		}
		// An account that is gone or turned off is no longer listed.
		account, err := s.db.Account(r.Context(), kept.User)
		if err != nil || account.Disabled {
			continue
		}
		profiles, err := s.db.Profiles(r.Context(), kept.User)
		if err != nil || len(profiles) == 0 {
			continue
		}
		out.Accounts = append(out.Accounts, RememberedAccount{AccountID: account.PublicID, Username: account.Username,
			Name: profiles[0].Name, Color: profiles[0].Color, AccountToken: token})
	}
	return out, nil
}
