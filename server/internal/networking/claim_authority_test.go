package networking

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"strings"
	"testing"
	"time"
)

func authorityFixture(t *testing.T, v Intent, state string, revision int64) ([]byte, ClaimAuthorityVerifier) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	w := authorityWire{"portico.claim.authority", "1", "https://hosted.example", v.OperationID, v.ServerID, v.AccountID, v.LocalGeneration, v.ClaimGeneration, v.CredentialGeneration, revision, state, now.Add(-time.Second).Format(time.RFC3339Nano), now.Add(time.Minute).Format(time.RFC3339Nano)}
	raw, _ := json.Marshal(w)
	proof := signedApprovalFixture(string(raw), key)
	verify, e := NewClaimAuthorityVerifier("https://hosted.example", trust.KeyID(pub), pub)
	if e != nil {
		t.Fatal(e)
	}
	return proof, verify
}
func installedAuthorityFixture(t *testing.T) (*sqlFixture, Intent, context.Context) {
	t.Helper()
	f := fixtureCurrentSQL(t)
	ctx := fixtureClaimContext(t)
	v := f.retrieving(t)
	r := resultFor(t, v)
	defer r.Credential.Clear()
	v, e := f.store.Install(ctx, v, r)
	if e != nil {
		t.Fatal(e)
	}
	f.store.installedGuard = GuardInstalledClaim
	return f, v, ctx
}
func TestClaimAuthorityCanonicalVerifier(t *testing.T) {
	v := Intent{Binding: Binding{OperationID: "op", ServerID: "server", AccountID: "account"}, ClaimGeneration: "1", CredentialGeneration: "2"}
	proof, verify := authorityFixture(t, v, "revoked", 1)
	f, e := verify(context.Background(), proof)
	if e != nil || f.State != "revoked" || f.Revision != 1 || f.CredentialGeneration != "2" {
		t.Fatalf("bad verified tuple: %v", e)
	}
	var env approvalEnvelope
	_ = json.Unmarshal(proof, &env)
	raw, _ := approvalBase64(env.Payload, -1)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	for _, candidate := range []string{strings.Replace(string(raw), `"observationRevision":"1"`, `"observationRevision":"01"`, 1), strings.Replace(string(raw), `"claimGeneration":"1"`, `"claimGeneration":"9223372036854775808"`, 1), strings.Replace(string(raw), `"version":"1"`, `"version":"1","version":"1"`, 1), strings.Replace(string(raw), `"state":"revoked"`, `"state":"unknown"`, 1)} {
		if _, e = verifyClaimAuthorityAt(context.Background(), "https://hosted.example", trust.KeyID(pub), pub, signedApprovalFixture(candidate, key), time.Now()); e == nil {
			t.Fatal("noncanonical authority accepted")
		}
	}
	if _, e = verify(context.Background(), append(proof, ' ')); e == nil {
		t.Fatal("noncanonical envelope accepted")
	}
}
func TestClaimAuthorityStickyRevisionAndDigest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, v, ctx := installedAuthorityFixture(t)
	apply := func(state string, rev int64) error {
		proof, verify := authorityFixture(t, v, state, rev)
		c, _ := NewClaimAuthorityConsumer(f.store, verify, func(context.Context, *sql.Tx, Intent, string) error { return nil })
		return c.Apply(ctx, v, proof)
	}
	if e := apply("active", 1); e != nil {
		t.Fatal(e)
	}
	proof, verify := authorityFixture(t, v, "revoked", 2)
	calls := 0
	c, _ := NewClaimAuthorityConsumer(f.store, verify, func(context.Context, *sql.Tx, Intent, string) error { calls++; return nil })
	if e := c.Apply(ctx, v, proof); e != nil {
		t.Fatal(e)
	}
	if e := c.Apply(ctx, v, proof); e != nil || calls != 1 {
		t.Fatal("exact replay was not idempotent")
	}
	for _, test := range []struct {
		state string
		rev   int64
	}{{"active", 3}, {"revoked", 1}, {"revoked", 2}, {"unclaimed", 3}} {
		if e := apply(test.state, test.rev); e == nil {
			t.Fatal("terminal fence changed")
		}
	}
	if e := apply("revoked", 3); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`UPDATE networking_claim_authority SET expires_at='2000-01-01T00:00:00Z'`); e != nil {
		t.Fatal(e)
	}
	if e := f.store.Current(ctx, v); !errors.Is(e, ErrCancelled) {
		t.Fatal("expired terminal was forgotten")
	}
}
func TestClaimAuthorityRevocationRollbackAndStaleTuple(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, v, ctx := installedAuthorityFixture(t)
	proof, verify := authorityFixture(t, v, "revoked", 1)
	if _, e := NewClaimAuthorityConsumer(f.store, verify, nil); !errors.Is(e, ErrInvalid) {
		t.Fatal("missing revoker allowed")
	}
	if _, e := f.db.Exec(`CREATE TABLE authority_revocation_test(value TEXT)`); e != nil {
		t.Fatal(e)
	}
	c, _ := NewClaimAuthorityConsumer(f.store, verify, func(ctx context.Context, tx *sql.Tx, _ Intent, _ string) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO authority_revocation_test VALUES('revoked')`)
		if e != nil {
			return e
		}
		return ErrUnavailable
	})
	if e := c.Apply(ctx, v, proof); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	for _, table := range []string{"networking_claim_authority", "authority_revocation_test"} {
		var n int
		if e := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatal("partial revocation committed")
		}
	}
	wrong := v
	wrong.CredentialGeneration = "999"
	if e := c.Apply(ctx, wrong, proof); !errors.Is(e, ErrStale) {
		t.Fatal("wrong generation accepted")
	}
	if _, _, e := f.store.BeginCancel(ctx, v.Binding, f.cancel(t, v)); e != nil {
		t.Fatal(e)
	}
	if e := c.Apply(ctx, v, proof); !errors.Is(e, ErrStale) {
		t.Fatal("late observation wrote cancelled identity")
	}
	pending, e := f.store.Load(ctx, v.OperationID)
	if e != nil {
		t.Fatal(e)
	}
	item, e := f.store.PendingCancellation(ctx, pending)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.CancelAcknowledged(ctx, pending, item.RequestID); e != nil {
		t.Fatal(e)
	}
	f.binding.OperationID = "replacement-operation"
	if e = f.db.QueryRow(`SELECT reset_generation FROM networking_claim_identity WHERE singleton=1`).Scan(&f.binding.LocalGeneration); e != nil {
		t.Fatal(e)
	}
	replacement := f.retrieving(t)
	r := resultFor(t, replacement)
	defer r.Credential.Clear()
	replacement, e = f.store.Install(ctx, replacement, r)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Apply(ctx, v, proof); !errors.Is(e, ErrStale) {
		t.Fatal("A altered replacement B")
	}
	if e = f.store.Current(ctx, replacement); e != nil {
		t.Fatal("replacement B no longer current")
	}
	var n int
	if e = f.db.QueryRow(`SELECT count(*) FROM networking_claim_authority`).Scan(&n); e != nil || n != 0 {
		t.Fatal("stale A wrote authority history")
	}

}
func TestClaimAuthorityBootstrapAndLeaseFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, v, ctx := installedAuthorityFixture(t)
	if e := f.store.Current(ctx, v); e != nil {
		t.Fatal("control-plane bootstrap unavailable")
	}
	gated, e := f.store.tx(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tx := gated.Tx()
	if e = RequireActiveClaimAuthority(ctx, tx, v); !errors.Is(e, ErrUnavailable) {
		t.Fatal("bootstrap granted attachment readiness")
	}
	gated.Rollback()
	proof, verify := authorityFixture(t, v, "active", 1)
	c, _ := NewClaimAuthorityConsumer(f.store, verify, func(context.Context, *sql.Tx, Intent, string) error { return nil })
	if e = c.Apply(context.Background(), v, proof); !errors.Is(e, ErrUnavailable) {
		t.Fatal("ungated observation accepted")
	}
	if e = c.Apply(ctx, v, proof); e != nil {
		t.Fatal(e)
	}
	var gated2 *dbwork.Write
	gated2, e = f.store.tx(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tx = gated2.Tx()
	defer gated2.Rollback()
	if e = RequireActiveClaimAuthority(ctx, tx, v); e != nil {
		t.Fatal(e)
	}
}
