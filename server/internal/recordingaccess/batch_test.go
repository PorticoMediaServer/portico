package recordingaccess

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The batch entry point must decide exactly what the existing per-library
// policy decides, one library at a time, for every composition it supports:
// direct local policy, a real CachedPolicy (hosted, which also offers the batch
// seam), and a custom cache that only knows the single-library method.
//
// The fixture is the installed schema, not a stub: owner and member accounts,
// a member profile narrowed by its own library list, an owner child profile, a
// disabled membership, a stale epoch, and three private DVR libraries (own,
// foreign, and own with Recordings switched off).

func newBatchFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// accounts and their primary profiles/memberships are created by the
	// direct_account_created trigger; role assignment follows insert order.
	if _, err = db.Exec(`
INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES
 ('owner','owner',x'00','primary',1),
 ('member','member',x'00','member-primary',1),
 ('limited','limited',x'00','limited-primary',1),
 ('expand','expand',x'00','expand-primary',1),
 ('revoked','revoked',x'00','revoked-primary',1),
 ('stale','stale',x'00','stale-primary',3);
INSERT INTO direct_profiles(id,account_id,name,is_primary) VALUES('child','owner','Child',0);
UPDATE direct_memberships SET allowed_libraries='["movies","shows"]' WHERE account_id IN('member','limited');
UPDATE direct_memberships SET disabled=1 WHERE account_id='revoked';
UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id='expand';
UPDATE direct_profiles SET allowed_libraries='["movies"]' WHERE account_id='limited';
UPDATE direct_profiles SET allowed_libraries='["shows"]' WHERE account_id='expand';
INSERT INTO libraries(id,name,kind,root) VALUES
 ('movies','Movies','movies','/media/movies'),
 ('shows','Shows','shows','/media/shows'),
 ('dvr-owner','Owner recordings','recordings','/media/dvr-owner'),
 ('dvr-child','Child recordings','recordings','/media/dvr-child'),
 ('dvr-member','Member recordings','recordings','/media/dvr-member');
INSERT INTO dvr_private_libraries(library_id,authority,account_id,profile_id) VALUES
 ('dvr-owner','local','owner','primary'),
 ('dvr-child','local','owner','child'),
 ('dvr-member','local','member','member-primary');
INSERT INTO profile_restrictions(profile_id,allow_dvr) VALUES('member-primary',1);`); err != nil {
		t.Fatal(err)
	}
	return db
}

func newBatchTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

func localViewer(account, profile string) identity.Principal {
	return identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: account, ProfileID: profile}}
}

// perLibraryDecisions is the baseline the batch must reproduce: ask the
// existing policy one library at a time in the same transaction. A refusal in
// the authorization family (including ErrNotVisible) is false; any other
// failure is a real error, never a quiet denial.
func perLibraryDecisions(t *testing.T, ctx context.Context, p Policy, tx *sql.Tx, who identity.Principal, libraries []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, library := range libraries {
		if _, seen := out[library]; seen {
			continue
		}
		switch err := p.AllowedTxContext(ctx, who, library, tx); {
		case err == nil:
			out[library] = true
		case errors.Is(err, identity.ErrUnauthorized):
			out[library] = false
		default:
			t.Fatalf("per-library policy for %q: %v", library, err)
		}
	}
	return out
}

func compareBatch(t *testing.T, got, want map[string]bool, libraries []string) {
	t.Helper()
	for _, library := range libraries {
		if got[library] != want[library] {
			t.Errorf("batch %q = %v, per-library %v", library, got[library], want[library])
		}
	}
	for library := range got {
		if _, requested := want[library]; !requested {
			t.Errorf("batch returned unrequested library %q", library)
		}
	}
}

func TestBatchMatchesPerLibraryPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := newBatchFixture(t)
	tx := newBatchTx(t, db)
	ctx := context.Background()
	libraries := []string{"movies", "shows", "dvr-owner", "dvr-child", "dvr-member", "missing"}

	hostedPolicy, err := hosted.New(db, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	policies := []struct {
		name   string
		policy Policy
	}{
		{name: "direct", policy: Policy{}},
		{name: "hosted-cached", policy: Policy{Cached: hostedPolicy}},
	}
	// The expectations are the current single-library meaning, so a defect
	// shared by both paths cannot hide as "parity".
	viewers := []struct {
		name string
		who  identity.Principal
		want map[string]bool
	}{
		{name: "owner-primary", who: localViewer("owner", "primary"), want: map[string]bool{
			"movies": true, "shows": true, "dvr-owner": true, "dvr-child": false, "dvr-member": false, "missing": true,
		}},
		{name: "member-with-recording", who: localViewer("member", "member-primary"), want: map[string]bool{
			"movies": true, "shows": true, "dvr-owner": false, "dvr-child": false, "dvr-member": true, "missing": false,
		}},
		{name: "owner-child-without-recording", who: localViewer("owner", "child"), want: map[string]bool{
			"movies": true, "shows": true, "dvr-owner": false, "dvr-child": false, "dvr-member": false, "missing": true,
		}},
		{name: "member-narrowed-by-profile", who: localViewer("limited", "limited-primary"), want: map[string]bool{
			"movies": true, "shows": false, "dvr-owner": false, "dvr-child": false, "dvr-member": false, "missing": false,
		}},
		{name: "member-profile-cannot-expand", who: localViewer("expand", "expand-primary"), want: map[string]bool{
			"movies": false, "shows": false, "dvr-owner": false, "dvr-child": false, "dvr-member": false, "missing": false,
		}},
		{name: "disabled-membership", who: localViewer("revoked", "revoked-primary"), want: map[string]bool{
			"movies": false, "shows": false, "dvr-owner": false, "dvr-child": false, "dvr-member": false, "missing": false,
		}},
		{name: "stale-epoch", who: identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "stale", ProfileID: "stale-primary"}, Epoch: 2}, want: map[string]bool{
			"movies": false, "shows": false, "dvr-owner": false, "dvr-child": false, "dvr-member": false, "missing": false,
		}},
		{name: "hosted-authority", who: identity.Principal{Viewer: identity.Viewer{Authority: "hosted", AccountID: "owner", ProfileID: "primary"}}, want: map[string]bool{
			"movies": false, "shows": false, "dvr-owner": false, "dvr-child": false, "dvr-member": false, "missing": false,
		}},
	}

	for _, policy := range policies {
		for _, viewer := range viewers {
			t.Run(policy.name+"/"+viewer.name, func(t *testing.T) {
				want := perLibraryDecisions(t, ctx, policy.policy, tx, viewer.who, libraries)
				got, err := policy.policy.AllowedSetTx(ctx, tx, viewer.who, libraries)
				if err != nil {
					t.Fatalf("batch: %v", err)
				}
				compareBatch(t, got, want, libraries)
				for library, allowed := range viewer.want {
					if got[library] != allowed {
						t.Errorf("batch %q = %v, want %v", library, got[library], allowed)
					}
					if want[library] != allowed {
						t.Errorf("per-library %q = %v, want %v", library, want[library], allowed)
					}
				}
			})
		}
	}
}

// plainCache is a CachedPolicy that does not offer the batch seam, so
// AllowedSetTx must fall back to the per-library path and consult it once for
// every library (plus the private-library base target).
type plainCache struct {
	allow map[string]bool
	calls []string
}

func (c *plainCache) AllowedTxContext(_ context.Context, _ identity.Principal, library string, _ *sql.Tx) error {
	c.calls = append(c.calls, library)
	if c.allow[library] {
		return nil
	}
	return identity.ErrNotVisible
}

// cachedBatch is the same cache with the optional batch seam implemented.
type cachedBatch struct {
	plainCache
	batches int
}

func (c *cachedBatch) AllowedSetTxContext(_ context.Context, _ identity.Principal, libraries []string, _ *sql.Tx) (map[string]bool, error) {
	c.batches++
	out := map[string]bool{}
	for _, library := range libraries {
		out[library] = c.allow[library]
	}
	return out, nil
}

func TestBatchCustomCachedFallback(t *testing.T) {
	db := newBatchFixture(t)
	tx := newBatchTx(t, db)
	ctx := context.Background()
	owner := localViewer("owner", "primary")
	libraries := []string{"movies", "shows", "dvr-owner"}
	allow := map[string]bool{"": true, "movies": true}

	cache := &plainCache{allow: allow}
	policy := Policy{Cached: cache}
	got, err := policy.AllowedSetTx(ctx, tx, owner, libraries)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	want := perLibraryDecisions(t, ctx, Policy{Cached: &plainCache{allow: allow}}, tx, owner, libraries)
	compareBatch(t, got, want, libraries)
	// A cache without the batch seam is asked once per library, and the DVR
	// library is asked through its base target, exactly like the per-library
	// policy.
	if calls := []string{"movies", "shows", ""}; !reflect.DeepEqual(cache.calls, calls) {
		t.Fatalf("fallback consulted the cache as %v, want %v", cache.calls, calls)
	}
	if !got["movies"] || got["shows"] || !got["dvr-owner"] {
		t.Fatalf("fallback decisions: %v", got)
	}
}

func TestBatchCustomCachedBatchSeam(t *testing.T) {
	db := newBatchFixture(t)
	tx := newBatchTx(t, db)
	ctx := context.Background()
	owner := localViewer("owner", "primary")
	libraries := []string{"movies", "shows", "dvr-owner"}
	allow := map[string]bool{"": true, "movies": true}

	cache := &cachedBatch{plainCache: plainCache{allow: allow}}
	policy := Policy{Cached: cache}
	got, err := policy.AllowedSetTx(ctx, tx, owner, libraries)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if cache.batches != 1 {
		t.Fatalf("batch seam called %d times, want 1", cache.batches)
	}
	want := perLibraryDecisions(t, ctx, Policy{Cached: &plainCache{allow: allow}}, tx, owner, libraries)
	compareBatch(t, got, want, libraries)
	// The cache left dvr-owner false, but it is the viewer's own private
	// library with Recordings on, so the batch path must restore the
	// base-target decision exactly as the per-library path does.
	if !got["dvr-owner"] {
		t.Fatalf("own private library denied by batch: %v", got)
	}
}
