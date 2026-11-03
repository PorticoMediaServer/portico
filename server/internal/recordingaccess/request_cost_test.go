package recordingaccess

import (
	"context"
	"database/sql"
	"errors"
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
