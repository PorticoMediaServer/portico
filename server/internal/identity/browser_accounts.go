package identity

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"time"
)

var ErrBrowserAccountInput = requestinput.New("invalid remembered account")

// A browser cannot keep a list of signed-in accounts the way a native app can, so
// the server keeps the *descriptors* — who has used this installation, what to call
// them, which picture to draw — while the browser keeps the credentials. Reading
// the list therefore reveals only what someone sitting at that browser could
// already see on its account-switcher, and the server can offer the switcher
// before any token is presented.
//
// Nothing here is a credential and nothing here signs anyone in: choosing an entry
// tells the client which stored token to present, and that token is verified
// normally.

// BrowserAccount is one remembered account on one installation.
type BrowserAccount struct {
	AccountID       string `json:"accountId"`
	Username        string `json:"username"`
	DisplayName     string `json:"displayName"`
	AvatarVersion   int64  `json:"avatarVersion"`
	AvatarURL       string `json:"avatarUrl,omitempty"`
	AutomaticSignIn bool   `json:"automaticSignIn"`
	LastUsed        string `json:"lastUsed"`
}

// RememberBrowserAccount adds or refreshes the caller's entry on an installation.
// It is refused when the device says the account may not be remembered on it.
func (s *Service) RememberBrowserAccount(ctx context.Context, bearer, installation string, automatic bool) ([]BrowserAccount, error) {
	if !validInstallationID(installation) {
		return nil, ErrBrowserAccountInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return nil, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.accountManagerTx(ctx, tx, bearer)
	if e != nil {
		return nil, e
	}
	var remember bool
	if e = tx.QueryRowContext(ctx, `SELECT remember_account FROM identity_devices WHERE account_id=? AND installation_id=?`, c.account.ID, installation).Scan(&remember); e != nil {
		return nil, ErrDeviceUnknown
	}
	allowed, e := rememberAccountsTx(ctx, tx)
	if e != nil {
		return nil, e
	}
	if !remember || !allowed {
		return nil, ErrUnauthorized
	}
	name := c.account.Username
	var avatar int64
	_ = tx.QueryRowContext(ctx, `SELECT p.name,COALESCE(a.version,0) FROM direct_profiles p LEFT JOIN profile_avatars a ON a.profile_id=p.id WHERE p.id=?`, c.account.PrimaryProfileID).Scan(&name, &avatar)
	if _, e = tx.ExecContext(ctx, `INSERT INTO identity_browser_accounts(installation_id,account_id,username,display_name,avatar_version,automatic_sign_in,last_used) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(installation_id,account_id) DO UPDATE SET username=excluded.username,display_name=excluded.display_name,avatar_version=excluded.avatar_version,automatic_sign_in=excluded.automatic_sign_in,last_used=excluded.last_used`,
		installation, c.account.ID, c.account.Username, name, avatar, automatic, time.Now().UTC().Format(time.RFC3339)); e != nil {
		return nil, e
	}
	// Exactly one account on an installation may sign in automatically.
	if automatic {
		if _, e = tx.ExecContext(ctx, `UPDATE identity_browser_accounts SET automatic_sign_in=0 WHERE installation_id=? AND account_id<>?`, installation, c.account.ID); e != nil {
			return nil, e
		}
	}
	// Ten is what an account-switcher can show without becoming a list of
	// everyone who has ever used a shared computer.
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_browser_accounts WHERE installation_id=? AND account_id NOT IN(SELECT account_id FROM identity_browser_accounts WHERE installation_id=? ORDER BY last_used DESC LIMIT 10)`, installation, installation); e != nil {
		return nil, e
	}
	out, e := s.browserAccountsTx(ctx, tx, installation)
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}

func (s *Service) browserAccountsTx(ctx context.Context, tx *sql.Tx, installation string) ([]BrowserAccount, error) {
	allowed, e := rememberAccountsTx(ctx, tx)
	if e != nil {
		return nil, e
	}
	if !allowed {
		return []BrowserAccount{}, nil
	}
	rows, e := tx.QueryContext(ctx, `SELECT account_id,username,display_name,avatar_version,automatic_sign_in,last_used FROM identity_browser_accounts WHERE installation_id=? ORDER BY last_used DESC LIMIT 10`, installation)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []BrowserAccount{}
	for rows.Next() {
		var a BrowserAccount
		if e = rows.Scan(&a.AccountID, &a.Username, &a.DisplayName, &a.AvatarVersion, &a.AutomaticSignIn, &a.LastUsed); e != nil {
			return nil, e
		}
		a.AvatarURL = ""
		out = append(out, a)
	}
	return out, rows.Err()
}

// BrowserAccounts lists the accounts remembered on an installation. It is
// deliberately unauthenticated: a browser that has not signed in yet is exactly
// the caller that needs it, and the list contains no credential. Guessing an
// installation id requires knowing the opaque identifier generated by that client.
// The server validates its grammar and length, not its entropy.
func (s *Service) BrowserAccounts(ctx context.Context, installation string) ([]BrowserAccount, error) {
	if !validInstallationID(installation) {
		return nil, ErrBrowserAccountInput
	}
	g, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, e
	}
	defer g.Rollback()
	return s.browserAccountsTx(ctx, g.Tx(), installation)
}

// ForgetBrowserAccount removes one entry. The caller must hold a session for the
// account being removed, or the installation's own device record.
func (s *Service) ForgetBrowserAccount(ctx context.Context, bearer, installation, account string) error {
	if !validInstallationID(installation) || !validFamilyID(account) {
		return ErrBrowserAccountInput
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	c, e := s.accountManagerTx(ctx, tx, bearer)
	if e != nil {
		return e
	}
	if c.account.ID != account {
		var owns bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_devices WHERE account_id=? AND installation_id=?)`, c.account.ID, installation).Scan(&owns); e != nil {
			return e
		}
		if !owns {
			return ErrForbidden
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_browser_accounts WHERE installation_id=? AND account_id=?`, installation, account); e != nil {
		return e
	}
	return gated2.Commit()
}
