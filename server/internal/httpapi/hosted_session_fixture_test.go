package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Existing fixtures describe their members in policy-shaped data. Install those
// descriptions as server-local membership after a genuine completed Local setup.
// Hosted policy bytes themselves never authorize media or administration. The
// historical helper name does not select an authority or create a Portico link.
func issuePorticoFixture(t *testing.T, db *sql.DB, ident *identity.Service, account, profile, role string) (identity.Envelope, error) {
	t.Helper()
	var raw string
	if err := db.QueryRow(`SELECT payload FROM policy WHERE server_id=?`, ident.ID()).Scan(&raw); err != nil {
		return identity.Envelope{}, err
	}
	var policy hosted.Policy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return identity.Envelope{}, err
	}
	libraries := []string{}
	for _, member := range policy.Members {
		if member.AccountID != account || member.ProfileID != profile {
			continue
		}
		libraries = append(libraries, member.AllowedLibraries...)
		if member.AllLibraries {
			rows, err := db.Query(`SELECT id FROM libraries ORDER BY id`)
			if err != nil {
				return identity.Envelope{}, err
			}
			for rows.Next() {
				var library string
				if err = rows.Scan(&library); err != nil {
					rows.Close()
					return identity.Envelope{}, err
				}
				libraries = append(libraries, library)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return identity.Envelope{}, err
			}
		}
	}
	ctx := context.Background()
	if role != identity.TierMember && role != identity.TierAdmin {
		return identity.Envelope{}, identity.ErrForbidden
	}
	var accounts int
	if err := db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&accounts); err != nil {
		return identity.Envelope{}, err
	}
	if accounts == 0 {
		if ident.SetupRemoteTx == nil {
			operations.WireSetupRemote(ident)
		}
		secret, err := ident.BrowserSetupToken(ctx)
		if err != nil {
			return identity.Envelope{}, err
		}
		if _, err = ident.SetupWithOptions(ctx, identity.SetupOptions{SetupToken: secret, AuthMode: "local", Username: "fixture-owner", Password: "Local-password1!", Name: "Fixture"}, true); err != nil {
			return identity.Envelope{}, err
		}
	}
	if err := ident.RequireSignInKind(ctx, identity.SignInLocal); err != nil {
		return identity.Envelope{}, err
	}
	var ready bool
	if err := db.QueryRow(`SELECT ready FROM onboarding_state_v1 WHERE singleton=1 AND auth_mode='local'`).Scan(&ready); err != nil {
		return identity.Envelope{}, err
	}
	if !ready {
		return identity.Envelope{}, identity.ErrUnauthorized
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return identity.Envelope{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) SELECT ?,?,a.password_hash,?,1 FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE m.role='owner' AND m.disabled=0 AND length(a.password_hash)>0`, account, account, profile); err != nil {
		return identity.Envelope{}, err
	}
	encoded, _ := json.Marshal(libraries)
	if _, err = tx.Exec(`UPDATE direct_memberships SET role=?,allowed_libraries=? WHERE account_id=?`, role, string(encoded), account); err != nil {
		return identity.Envelope{}, err
	}
	out, err := ident.IssueTx(ctx, tx, account, profile, "local", role, 1, time.Time{})
	if err != nil {
		return identity.Envelope{}, err
	}
	if err = tx.Commit(); err != nil {
		return identity.Envelope{}, err
	}
	if _, err = ident.Authenticate(out.AccessToken); err != nil {
		t.Fatalf("completed Local fixture issued an unusable local session: %v", err)
	}
	return out, nil
}

// These media and owner tests exercise local membership authority, not Hosted
// signature verification. Their policy row describes fixture members only.
// The hosted package tests actual signed identity admission and key revocation.
func storeHTTPFixturePolicy(db *sql.DB, raw []byte) error {
	var policy hosted.Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO policy(server_id,revision,payload,expires_at) VALUES(?,?,?,?) ON CONFLICT(server_id) DO UPDATE SET revision=excluded.revision,payload=excluded.payload,expires_at=excluded.expires_at`, policy.ServerID, policy.Revision, string(raw), policy.ExpiresAt)
	return err
}
