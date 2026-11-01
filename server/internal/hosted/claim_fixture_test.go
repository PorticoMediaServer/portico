package hosted

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// claimFixture is a server with a Portico claim prepared, approved, installed and
// acknowledged: the state every control-plane behaviour needs before it can be observed at
// all. Its Hosted origin is https://hosted.example, which resolves to nothing, so any attempt
// to contact Hosted fails — which is exactly what makes "did this call Hosted?" observable.
type claimFixture struct {
	setupOwner *networking.LocalOwner
	db         *sql.DB
	state      string
	id         *identity.Service
	service    *Service
	runner     *networking.AuthorityRunner
	store      *networking.SQLiteStore
	installed  networking.Intent
	hostedKey  ed25519.PrivateKey
	keys       *networking.ProtectedKeys
	owner      identity.Envelope
}

func newClaimFixture(t *testing.T, beforeInstall ...func(*claimFixture)) *claimFixture {
	t.Helper()
	state := t.TempDir()
	os.Chmod(state, 0700)
	db, e := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = persistence.VerifyNetworkingClaims(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	gate, _ := authoritygate.NewAuthorityGate(strings.Repeat("a", 64), false)
	runner, _ := networking.NewAuthorityRunner(func(ctx context.Context) (networking.LifecycleLease, error) { return gate.Acquire(ctx) })
	keys, e := networking.OpenCurrentKeys(context.Background(), db, state, runner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { keys.Close() })
	id, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	operations.WireSetupRemote(id)
	token, _ := os.ReadFile(storagepaths.Path(state, "setup-token"))
	owner, e := id.Setup(string(token), "owner", "test-password-long", "Server")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico'`); e != nil {
		t.Fatal(e)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	service, e := New(db, id, "https://hosted.example", base64.RawURLEncoding.EncodeToString(pub), trust.KeyID(pub))
	if e != nil {
		t.Fatal(e)
	}
	verifier, _ := networking.NewApprovalVerifier("https://hosted.example", trust.KeyID(pub), pub)
	cipher, e := networking.OpenClaimCipher(context.Background(), db, state)
	if e != nil {
		t.Fatal(e)
	}
	store, e := networking.NewSQLiteStore(db, cipher, networking.GuardLocalOwner, networking.GuardInstalledClaim, verifier, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	store.SetTerminalRevoker(service.RevokeClaimTx)
	transport, _ := networking.NewHTTPTransport("https://hosted.example", store)
	service.UseCurrentClaims(store, runner, transport)
	var public []byte
	db.QueryRow(`SELECT public_key FROM networking_claim_identity`).Scan(&public)
	var installed networking.Intent
	f := &claimFixture{db: db, state: state, id: id, service: service, runner: runner, store: store, hostedKey: key, keys: keys, owner: owner}
	for _, configure := range beforeInstall {
		configure(f)
	}
	e = runner.Do(context.Background(), func(ctx context.Context) error {
		actor := networking.LocalOwner{AccountID: owner.Viewer.AccountID, ProfileID: owner.Viewer.ProfileID, Epoch: 1}
		if f.setupOwner != nil {
			actor = *f.setupOwner
		}
		v, e := store.Prepare(ctx, networking.Binding{OperationID: "operation", ServerID: id.ID(), AccountID: "account", PublicKey: public}, actor)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		payload := struct {
			Kind                  string `json:"kind"`
			Version               string `json:"version"`
			Audience              string `json:"audience"`
			OperationID           string `json:"operationId"`
			ServerID              string `json:"serverId"`
			AccountID             string `json:"accountId"`
			PublicKey             string `json:"publicKey"`
			LocalGeneration       string `json:"localGeneration"`
			ApprovalRevision      string `json:"approvalRevision"`
			Purpose               string `json:"purpose"`
			CurrentOwnerAccountID string `json:"currentOwnerAccountId"`
			PreviousOwnerRole     string `json:"previousOwnerRole"`
			ReturnRegistrationID  string `json:"returnRegistrationId"`
			IssuedAt              string `json:"issuedAt"`
			ExpiresAt             string `json:"expiresAt"`
		}{"portico.claim.approval", "2", "https://hosted.example", v.OperationID, v.ServerID, v.AccountID, base64.RawURLEncoding.EncodeToString(public), "0", "1", "setup", "", "", "", now.Format(time.RFC3339Nano), now.Add(4 * time.Minute).Format(time.RFC3339Nano)}
		raw, _ := json.Marshal(payload)
		proof, _ := json.Marshal(Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw)), KeyID: "pin1", Certificate: testSigningCertificate(key, "pin1")})
		v, e = store.Approve(ctx, v, proof)
		if e != nil {
			return e
		}
		v, e = store.BeginFinalizing(ctx, v)
		if e != nil {
			return e
		}
		commit := networking.Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}
		v, e = store.Committed(ctx, v, commit)
		if e != nil {
			return e
		}
		secret, _ := networking.NewSecret([]byte(strings.Repeat("s", 43)))
		defer secret.Clear()
		v, e = store.Install(ctx, v, networking.Result{Commit: commit, Credential: secret})
		if e != nil {
			return e
		}
		installed, e = store.Acknowledged(ctx, v)
		return e
	})
	if e != nil {
		t.Fatal("install", e)
	}
	f.installed = installed
	return f
}
