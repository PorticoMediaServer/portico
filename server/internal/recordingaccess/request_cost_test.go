package recordingaccess

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A request that filters every library asks the policy once per library. What
// the policy reads first (the caller's membership row, its grants, who owns
// each private recordings library) does not depend on the library, so inside
// a request's read snapshot it is read once: a page filtering ten libraries
// costs what one filtering one library costs, and decides exactly the same.
func TestPerLibraryChecksInOneRequestCostTheSameForOneLibraryOrMany(t *testing.T) {
	db := newBatchFixture(t)
	hostedPolicy, err := hosted.New(db, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	libraries := []string{"movies", "shows", "dvr-owner", "dvr-child", "dvr-member", "missing"}
	decide := func(ctx context.Context, p Policy, tx *sql.Tx, who identity.Principal, asked []string) map[string]error {
		out := map[string]error{}
		for _, library := range asked {
			err := p.AllowedTxContext(ctx, who, library, tx)
			if err != nil && !errors.Is(err, identity.ErrUnauthorized) {
				t.Fatalf("%s: %v", library, err)
			}
			out[library] = err
		}
		return out
	}
	inRequest := func(p Policy, who identity.Principal, asked []string) (map[string]error, int64) {
		ctx, cost := dbwork.Measure(dbwork.WithRequestMemo(context.Background()))
		var out map[string]error
		if err := dbwork.WithReadSnapshot(ctx, db, func(snapshot context.Context) error {
			out = decide(snapshot, p, dbwork.Snapshot(snapshot), who, asked)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out, cost().Statements
	}
	for _, composition := range []struct {
		name   string
		policy Policy
	}{{"direct", Policy{}}, {"hosted-cached", Policy{Cached: hostedPolicy}}} {
		for _, who := range []identity.Principal{
			localViewer("owner", "primary"), localViewer("member", "member-primary"), localViewer("owner", "child"),
			localViewer("limited", "limited-primary"), localViewer("expand", "expand-primary"),
			localViewer("revoked", "revoked-primary"), localViewer("stale", "stale-primary"),
		} {
			name := composition.name + "/" + who.AccountID + "/" + who.ProfileID
			// The same decisions as one fresh check per library, outside any request.
			tx := newBatchTx(t, db)
			want := decide(context.Background(), composition.policy, tx, who, libraries)
			tx.Rollback()
			got, many := inRequest(composition.policy, who, libraries)
			for _, library := range libraries {
				if !errors.Is(got[library], want[library]) || (got[library] == nil) != (want[library] == nil) {
					t.Errorf("%s %s: in a request %v, alone %v", name, library, got[library], want[library])
				}
			}
			// The library-dependent reads that remain are the profile's own
			// Recordings switch, asked only for a library it owns.
			_, one := inRequest(composition.policy, who, libraries[:1])
			_, owned := inRequest(composition.policy, who, []string{"dvr-owner", "dvr-child", "dvr-member"})
			if many > max(one, owned)+2 {
				t.Errorf("%s: %d statements for %d libraries, %d for one: the per-library reads are back", name, many, len(libraries), one)
			}
		}
	}
}

type snapshotPolicyProbe func(context.Context, identity.Principal, string, *sql.Tx) error

func (p snapshotPolicyProbe) AllowedTxContext(ctx context.Context, who identity.Principal, library string, tx *sql.Tx) error {
	return p(ctx, who, library, tx)
}

func TestPolicyChecksCarryTheirReadSnapshotAndSeeRevocation(t *testing.T) {
	for _, set := range []bool{false, true} {
		name := "single"
		if set {
			name = "set"
		}
		t.Run(name, func(t *testing.T) {
			db := newBatchFixture(t)
			who := localViewer("member", "member-primary")
			calls := 0
			policy := Policy{Cached: snapshotPolicyProbe(func(ctx context.Context, who identity.Principal, library string, tx *sql.Tx) error {
				calls++
				if dbwork.Snapshot(ctx) != tx {
					t.Fatal("policy lost its snapshot context; nested reads would open another transaction")
				}
				return dbwork.WithReadSnapshot(ctx, db, func(inner context.Context) error {
					if dbwork.Snapshot(inner) != tx {
						t.Fatal("nested policy read opened another snapshot")
					}
					return identity.DirectAllowed(tx, who, library)
				})
			})}
			request, trace := dbwork.TraceStatements(context.Background())
			request, cost := dbwork.Measure(request)
			request, end := dbwork.WithRequestSnapshot(request)
			defer end()
			check := func() error {
				if !set {
					return policy.Allowed(request, db, who, "movies")
				}
				allowed, err := policy.AllowedSet(request, db, who, []string{"movies", "shows", "movies"})
				if err != nil {
					return err
				}
				if !allowed["movies"] || !allowed["shows"] {
					return identity.ErrUnauthorized
				}
				return nil
			}
			if err := check(); err != nil {
				t.Fatal(err)
			}
			if got := cost().Transactions; got != 1 {
				t.Fatalf("nested policy reads opened %d transactions, want 1", got)
			}
			if calls == 0 {
				t.Fatal("cached policy was skipped")
			}
			for _, query := range trace() {
				if strings.Contains(strings.ToLower(query), "total_changes(") {
					t.Fatalf("read-only policy incurred write probes: %s", query)
				}
			}
			t.Logf("%s policy: transactions=%d statements=%d", name, cost().Transactions, cost().Statements)
			if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				_, err := tx.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='member'`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := check(); !errors.Is(err, identity.ErrUnauthorized) {
				t.Fatalf("request snapshot hid membership revocation: %v", err)
			}
			// A refused policy check inside a writer must still prevent the
			// parent committing, even when its caller ignores the error.
			err := dbwork.WithWriteTxContext(context.Background(), db, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
				if _, err := tx.Exec(`UPDATE libraries SET name='must roll back' WHERE id='movies'`); err != nil {
					return err
				}
				if err := policy.Allowed(ctx, db, who, "movies"); !errors.Is(err, identity.ErrUnauthorized) {
					t.Fatalf("writer accepted revoked member: %v", err)
				}
				return nil // deliberately ignore the denied child
			})
			if !errors.Is(err, dbwork.ErrAborted) {
				t.Fatalf("denied child did not abort its writer: %v", err)
			}
			var name string
			if err := db.QueryRow(`SELECT name FROM libraries WHERE id='movies'`).Scan(&name); err != nil || name != "Movies" {
				t.Fatalf("parent committed a refused policy check: name=%q err=%v", name, err)
			}
		})
	}
}
