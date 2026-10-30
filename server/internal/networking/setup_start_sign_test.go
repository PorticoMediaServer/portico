package networking

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

func TestProtectedSetupStartSignsOnlyFullCurrentPendingSetup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureCurrentSQL(t)
	f.now = time.Now().UTC()
	f.binding.AccountID = UnboundAccount
	// This fixture's guard models revocation only. Identity's setup capture tests
	// separately use the actual setup-token/request/revision authority.
	f.owner = LocalOwner{SetupOperationID: "82a879b3-abf5-48c6-bba0-02ffd762f99c", Epoch: 1}
	f.store.ownerGuard = func(ctx context.Context, tx *sql.Tx, owner LocalOwner) error {
		var allowed int
		if err := tx.QueryRowContext(ctx, `SELECT allowed FROM fixture_owners WHERE id='local_owner'`).Scan(&allowed); err != nil {
			return err
		}
		if owner != f.owner || allowed != 1 {
			return ErrClaimOwnerRequired
		}
		return ctx.Err()
	}
	ctx, err := guardedClaimRequest(fixtureClaimContext(t), f.store, func(ctx context.Context, tx *sql.Tx) error { return f.store.ownerGuard(ctx, tx, f.owner) })
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.store.PreparePurpose(ctx, f.binding, f.owner, ClaimPurpose{Purpose: "setup"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "key1.ed25519"), f.localKey, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	keys := &ProtectedKeys{db: f.db, root: root}
	q := claimweb.Request{Kind: "portico.claim.web.v2", Audience: "https://hosted.example", OperationID: v.OperationID, ServerID: v.ServerID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), Name: "Fixture", Address: "http://localhost:32501", Purpose: "setup", IssuedAt: f.now, ExpiresAt: f.now.Add(10 * time.Minute)}
	raw, _ := json.Marshal(q)
	signature, err := keys.Sign(ctx, v.Binding, raw)
	if err != nil {
		t.Fatal(err)
	}
	original := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(signature)}
	hash := sha256.Sum256(raw)
	state, pkce := sha256.Sum256([]byte("owner pending state")), sha256.Sum256([]byte("owner verifier"))
	start := claimweb.SetupStart{Kind: claimweb.SetupStartKind, SetupRequestID: f.owner.SetupOperationID, SetupRevision: f.owner.Epoch, Audience: q.Audience, ClaimHash: base64.RawURLEncoding.EncodeToString(hash[:]), OperationID: q.OperationID, ServerID: q.ServerID, PublicKey: q.PublicKey, Address: q.Address, Purpose: q.Purpose, State: base64.RawURLEncoding.EncodeToString(state[:]), CodeChallenge: base64.RawURLEncoding.EncodeToString(pkce[:]), InstallationID: "eaa4a644-ec2c-4d6e-81b7-e91e3f12fcb0", ServerChallenge: base64.RawURLEncoding.EncodeToString(state[:]), IssuedAt: f.now, ExpiresAt: f.now.Add(4 * time.Minute)}
	proof, err := keys.SignSetupStart(ctx, v.Binding, original, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, got, err := claimweb.VerifySetupStart(original, proof, q.Audience, time.Now()); err != nil || got != start {
		t.Fatal("actual protected-key signature did not verify", err)
	}
	startRaw, _ := json.Marshal(start)
	if _, err = keys.Sign(ctx, v.Binding, startRaw); !errors.Is(err, ErrInvalid) {
		t.Fatal("generic Sign gained setup-start authority", err)
	}
	for name, mutate := range map[string]func(*claimweb.SetupStart){
		"different operation":     func(s *claimweb.SetupStart) { s.OperationID = "other" },
		"different generation":    func(s *claimweb.SetupStart) { s.LocalGeneration++ },
		"different original hash": func(s *claimweb.SetupStart) { s.ClaimHash = s.CodeChallenge },
		"different callback":      func(s *claimweb.SetupStart) { s.Address = "https://other.example" },
		"public IP over HTTP":     func(s *claimweb.SetupStart) { s.Address = "http://198.51.100.7:32400" },
		"different purpose":       func(s *claimweb.SetupStart) { s.Purpose = "switch" },
		"extended start":          func(s *claimweb.SetupStart) { s.ExpiresAt = s.IssuedAt.Add(31 * time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := start
			mutate(&bad)
			if out, err := keys.SignSetupStart(ctx, v.Binding, original, bad); err == nil || out.Payload != "" || out.Signature != "" {
				t.Fatal("invalid setup start signed")
			}
		})
	}
	if _, err = f.db.Exec(`UPDATE fixture_owners SET allowed=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = keys.SignSetupStart(ctx, v.Binding, original, start); err == nil {
		t.Fatal("revoked captured setup signed a start")
	}
	if _, err = f.db.Exec(`UPDATE fixture_owners SET allowed=1;UPDATE networking_claim_identity SET active_operation_id=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err = keys.SignSetupStart(ctx, v.Binding, original, start); !errors.Is(err, ErrStale) {
		t.Fatal("withdrawn operation signed a start", err)
	}
}
