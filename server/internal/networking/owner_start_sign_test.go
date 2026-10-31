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

func TestProtectedOwnerStartSignsOnlyFullCurrentPreparedSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureCurrentSQL(t)
	f.now = time.Now().UTC()
	f.binding.AccountID = UnboundAccount
	if _, err := f.db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local');INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms,owner_confirmed) VALUES('move','local_owner',1,'local','portico',0,'preview',1,1,1,1,'Fixture',1,1)`); err != nil {
		t.Fatal(err)
	}
	ctx, err := guardedClaimRequest(fixtureClaimContext(t), f.store, func(ctx context.Context, tx *sql.Tx) error { return f.store.ownerGuard(ctx, tx, f.owner) })
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.store.PreparePurpose(ctx, f.binding, f.owner, ClaimPurpose{Purpose: "switch"})
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
	q := claimweb.Request{Kind: "portico.claim.web.v2", Audience: "https://hosted.example", OperationID: v.OperationID, ServerID: v.ServerID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), Name: "Fixture", Address: "http://localhost:32501", Purpose: "switch", IssuedAt: f.now, ExpiresAt: f.now.Add(10 * time.Minute)}
	raw, _ := json.Marshal(q)
	signature, err := keys.Sign(ctx, v.Binding, raw)
	if err != nil {
		t.Fatal(err)
	}
	original := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(signature)}
	hash := sha256.Sum256(raw)
	state, pkce := sha256.Sum256([]byte("owner pending state")), sha256.Sum256([]byte("owner verifier"))
	start := claimweb.OwnerStart{Kind: claimweb.OwnerStartKind, Audience: q.Audience, ClaimHash: base64.RawURLEncoding.EncodeToString(hash[:]), OperationID: q.OperationID, ServerID: q.ServerID, PublicKey: q.PublicKey, Address: q.Address, Purpose: q.Purpose, State: base64.RawURLEncoding.EncodeToString(state[:]), CodeChallenge: base64.RawURLEncoding.EncodeToString(pkce[:]), InstallationID: "eaa4a644-ec2c-4d6e-81b7-e91e3f12fcb0", ServerChallenge: base64.RawURLEncoding.EncodeToString(state[:]), IssuedAt: f.now, ExpiresAt: f.now.Add(4 * time.Minute)}
	proof, err := keys.SignOwnerStart(ctx, v.Binding, original, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, got, err := claimweb.VerifyOwnerStart(original, proof, q.Audience, time.Now()); err != nil || got != start {
		t.Fatal("actual protected-key signature did not verify", err)
	}
	startRaw, _ := json.Marshal(start)
	if _, err = keys.Sign(ctx, v.Binding, startRaw); !errors.Is(err, ErrInvalid) {
		t.Fatal("generic Sign gained owner-start authority", err)
	}
	for name, mutate := range map[string]func(*claimweb.OwnerStart){
		"different operation":     func(s *claimweb.OwnerStart) { s.OperationID = "other" },
		"different generation":    func(s *claimweb.OwnerStart) { s.LocalGeneration++ },
		"different original hash": func(s *claimweb.OwnerStart) { s.ClaimHash = s.CodeChallenge },
		"different callback":      func(s *claimweb.OwnerStart) { s.Address = "https://other.example" },
		"network HTTP":            func(s *claimweb.OwnerStart) { s.Address = "http://server.example" },
		"different purpose":       func(s *claimweb.OwnerStart) { s.Purpose = "setup" },
		"extended start":          func(s *claimweb.OwnerStart) { s.ExpiresAt = s.IssuedAt.Add(claimweb.OwnerStartLifetime + time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := start
			mutate(&bad)
			if out, err := keys.SignOwnerStart(ctx, v.Binding, original, bad); err == nil || out.Payload != "" || out.Signature != "" {
				t.Fatal("invalid owner start signed")
			}
		})
	}
	if _, err = f.db.Exec(`UPDATE fixture_owners SET allowed=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = keys.SignOwnerStart(ctx, v.Binding, original, start); err == nil {
		t.Fatal("revoked captured owner signed a start")
	}
	if _, err = f.db.Exec(`UPDATE fixture_owners SET allowed=1;UPDATE networking_claim_identity SET pending_operation_id=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err = keys.SignOwnerStart(ctx, v.Binding, original, start); !errors.Is(err, ErrStale) {
		t.Fatal("withdrawn operation signed a start", err)
	}
}
