package identity

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func transactionAuthorityFixture(t *testing.T) (*sql.DB, *Service, Principal) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	envelope, err := s.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return db, s, p
}

func TestReauthorizeTxUsesOwnedTransactionAndSeesRevocation(t *testing.T) {
	db, service, principal := transactionAuthorityFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	current, err := service.ReauthorizeTx(ctx, tx, principal)
	if err != nil || current != principal {
		t.Fatalf("current principal mismatch: %v", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id IN (SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, principal.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ReauthorizeTx(ctx, tx, principal); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("uncommitted revocation must deny: %v", err)
	}
}

func TestReauthorizeTxRejectsStalePrincipalAndAccountChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	cases := map[string]string{
		"session profile":   `UPDATE authorization_session_families SET profile_id='other'`,
		"session role":      `UPDATE authorization_session_families SET role='viewer'`,
		"session account":   `UPDATE authorization_session_families SET account_id='other'`,
		"session authority": `UPDATE authorization_session_families SET authority='hosted'`,
		"session epoch":     `UPDATE authorization_session_families SET epoch=2`,
		"account epoch":     `UPDATE accounts SET epoch=2`,
		"account profile":   `UPDATE accounts SET profile_id='other'`,
		"expired":           `UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z'`,
		"invalid expiry":    `UPDATE authorization_family_tokens SET expires_at='not-a-time'`,
		"deleted session":   `DELETE FROM authorization_family_tokens`,
	}
	for name, mutation := range cases {
		t.Run(name, func(t *testing.T) {
			db, service, principal := transactionAuthorityFixture(t)
			if name == "session authority" {
				if _, err := db.Exec(mutation); err == nil {
					t.Fatal("retired authority stored")
				}
				principal.Authority = "hosted"
			} else if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = service.ReauthorizeTx(context.Background(), tx, principal); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("stale principal authorized: %v", err)
			}
		})
	}
}

func TestReauthorizeTxCancellationAndHostedIdentityBoundary(t *testing.T) {
	db, service, principal := transactionAuthorityFixture(t)
	if _, err := service.Issue("hosted-account", "hosted-profile", "hosted", "member", 1); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retired wrapper invented authority: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = service.IssueTx(context.Background(), tx, "hosted-account", "hosted-profile", "hosted", "member", 1, time.Now().Add(time.Hour)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("transaction invented retired authority: %v", err)
	}
	if _, err = service.ReauthorizeTx(context.Background(), tx, principal); err != nil {
		t.Fatalf("current local identity: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = service.ReauthorizeTx(ctx, tx, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	wrongServer := principal
	wrongServer.ServerID = "another-server"
	if _, err = service.ReauthorizeTx(context.Background(), tx, wrongServer); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong server accepted: %v", err)
	}
}

func TestReauthorizeTxRechecksDeadlineAndNarrowedGrant(t *testing.T) {
	t.Run("shortened horizon", func(t *testing.T) {
		db, service, principal := transactionAuthorityFixture(t)
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		horizon := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
		if _, err = tx.Exec(`UPDATE authorization_session_families SET authorization_horizon=?`, horizon.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		current, err := service.ReauthorizeTx(context.Background(), tx, principal)
		if err != nil || !current.ExpiresAt.Equal(horizon) {
			t.Fatalf("current deadline %s, want %s: %v", current.ExpiresAt, horizon, err)
		}
		if _, err = tx.Exec(`UPDATE authorization_session_families SET authorization_horizon='2000-01-01T00:00:00Z'`); err != nil {
			t.Fatal(err)
		}
		if _, err = service.ReauthorizeTx(context.Background(), tx, principal); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expired family admitted: %v", err)
		}
	})
	t.Run("narrowed grant", func(t *testing.T) {
		db, service, principal := transactionAuthorityFixture(t)
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`INSERT INTO authorization_family_grants(family_id,grant_kind) SELECT family_id,? FROM authorization_family_tokens WHERE token_hash=?`, GrantCastTarget, principal.Hash); err != nil {
			t.Fatal(err)
		}
		principal.Grant = GrantCastTarget
		current, err := service.ReauthorizeTx(context.Background(), tx, principal)
		if err != nil || current != principal {
			t.Fatalf("narrowed principal changed: %+v: %v", current, err)
		}
		forged := principal
		forged.Grant = ""
		if _, err = service.ReauthorizeTx(context.Background(), tx, forged); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("widened principal admitted: %v", err)
		}
	})
}
