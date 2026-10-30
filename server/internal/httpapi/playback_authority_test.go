package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func playbackAuthorityFixture(t *testing.T) (Dependencies, identity.Principal, string, string) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "authority.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('a','A','movie','/a'),('b','B','movie','/b')`); err != nil {
		t.Fatal(err)
	}
	itemA, _ := seedHTTPAPICatalogEntity(t, db, "a", compactcatalog.Movie, "item-a", "A", 0, nil)
	itemB, _ := seedHTTPAPICatalogEntity(t, db, "b", compactcatalog.Movie, "item-b", "B", 0, nil)
	control, err := hosted.New(db, ident, "http://127.0.0.1:1", testHostedRootPin(), testHostedRootID())
	if err != nil {
		t.Fatal(err)
	}
	policy := hosted.Policy{ServerID: ident.ID(), Revision: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "account", ProfileID: "profile", Role: "member", AllowedLibraries: []string{"a"}}}}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = storeHTTPFixturePolicy(db, raw); err != nil {
		t.Fatal(err)
	}
	envelope, err := issuePorticoFixture(t, db, ident, "account", "profile", "member")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := ident.Authenticate(envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return Dependencies{DB: db, Identity: ident, Hosted: control}, principal, itemA, itemB
}

func TestPlaybackAuthorityUsesOneTransactionForSessionAndItemPolicy(t *testing.T) {
	d, principal, itemA, itemB := playbackAuthorityFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if current, err := d.playbackAuthorityTx(ctx, tx, principal, itemA); err != nil || current != principal {
		t.Fatalf("authorized: %v", err)
	}
	for _, item := range []string{itemB, "missing"} {
		if _, err := d.playbackAuthorityTx(ctx, tx, principal, item); !errors.Is(err, identity.ErrNotVisible) {
			t.Fatalf("item %s: %v", item, err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE direct_profiles SET allowed_libraries='[]',revision=revision+1 WHERE id='profile'`); err != nil {
		t.Fatal(err)
	}
	// The changed library restriction is visible to item authority in the same
	// transaction, so the command must be denied before the transaction ends.
	if _, err = d.playbackAuthorityTx(ctx, tx, principal, itemA); !errors.Is(err, identity.ErrNotVisible) {
		t.Fatalf("uncommitted item policy: %v", err)
	}
	if _, err = d.playbackAuthorityTx(ctx, tx, principal, ""); err != nil {
		t.Fatalf("account command: %v", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, principal.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err = d.playbackAuthorityTx(ctx, tx, principal, ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("uncommitted session revocation: %v", err)
	}
}

func TestPlaybackAuthorityRejectsCachedRestrictionAndPolicyChanges(t *testing.T) {
	mutations := map[string]string{
		"library restriction": `UPDATE direct_profiles SET allowed_libraries='[]',revision=revision+1 WHERE id='profile'`,
		"profile revocation":  `UPDATE direct_profiles SET deleted=1 WHERE id='profile'`,
		"session expired":     `UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z'`,
		"membership removed":  `DELETE FROM direct_memberships WHERE account_id='account'`,
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			d, principal, itemA, _ := playbackAuthorityFixture(t)
			tx, err := d.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if _, err = d.playbackAuthorityTx(context.Background(), tx, principal, itemA); !errors.Is(err, identity.ErrUnauthorized) && !(name == "library restriction" && errors.Is(err, identity.ErrNotVisible)) {
				t.Fatalf("changed authority admitted: %v", err)
			}
		})
	}
}

func TestPlaybackAuthorityFailsClosedWithoutDependenciesAndOnCancellation(t *testing.T) {
	d, principal, itemA, _ := playbackAuthorityFixture(t)
	tx, err := d.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, missing := range []Dependencies{{}} {
		if _, err = missing.playbackAuthorityTx(context.Background(), tx, principal, ""); !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatalf("missing dependency: %v", err)
		}
	}
	// A local session needs no Hosted service or second DB handle: its caller
	// owns this transaction. Identity must still validate it inside that snapshot.
	if _, err = (Dependencies{Identity: d.Identity}).playbackAuthorityTx(context.Background(), tx, principal, ""); err != nil {
		t.Fatalf("local transaction without Hosted: %v", err)
	}
	var absent *sql.Tx
	if _, err = d.playbackAuthorityTx(context.Background(), absent, principal, ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("nil tx: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = d.playbackAuthorityTx(ctx, tx, principal, itemA); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if err = d.Hosted.AllowedTxContext(ctx, principal, "a", tx); !errors.Is(err, context.Canceled) {
		t.Fatalf("policy cancellation: %v", err)
	}
}
