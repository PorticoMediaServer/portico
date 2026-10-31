package hosted

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
	"time"
)

func TestRegisteredReturnActualHostedProducerConsumer(t *testing.T) {
	producer := os.Getenv("PORTICO_RETURN_HOSTED_CHECKOUT")
	if producer == "" {
		t.Skip("set actual isolated Hosted source for producer/consumer proof")
	}
	f, secret := pendingClaimFixture(t)
	ctx := context.Background()
	installation := strings.Repeat("i", 43)
	challenge, _, e := f.service.IssueChallenge(ctx, installation)
	if e != nil {
		t.Fatal(e)
	}
	// Export only this disposable fixture key to its private temporary exchange;
	// no real user/server credential or state is read.
	files, e := filepath.Glob(storagepaths.Path(f.state, "networking-keys", "*.ed25519"))
	if e != nil || len(files) != 1 {
		t.Fatal("fixture key registry")
	}
	key, e := os.ReadFile(files[0])
	if e != nil {
		t.Fatal(e)
	}
	input := map[string]string{"ServerID": f.id.ID(), "PublicKey": base64.RawURLEncoding.EncodeToString(ed25519.PrivateKey(key).Public().(ed25519.PublicKey)), "PrivateKey": base64.RawURLEncoding.EncodeToString(key), "OperationID": f.installed.OperationID, "Challenge": challenge, "InstallationID": installation}
	clear(key)
	temp := t.TempDir()
	inputPath := filepath.Join(temp, "input.json")
	encoded, _ := json.Marshal(input)
	if e = os.WriteFile(inputPath, encoded, 0600); e != nil {
		t.Fatal(e)
	}
	clear(encoded)
	output := filepath.Join(temp, "producer.json")
	cmd := exec.Command("go", "test", "./internal/postgres", "-run", "^TestRegisteredReturnRawProducerArtifact$", "-count=1")
	cmd.Dir = filepath.Join(producer, "hosted-services/hosted")
	cmd.Env = append(os.Environ(), "PORTICO_RETURN_CONSUMER_INPUT="+inputPath, "PORTICO_RETURN_PRODUCER_JSON="+output)
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("actual Hosted producer: %v %s", e, raw)
	}
	raw, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var artifact struct {
		Grant   json.RawMessage
		Root    string
		Request RegisteredReturnExchange
	}
	if json.Unmarshal(raw, &artifact) != nil {
		t.Fatal("bad producer artifact")
	}
	root, e := base64.RawURLEncoding.DecodeString(artifact.Root)
	if e != nil {
		t.Fatal(e)
	}
	f.service.key = ed25519.PublicKey(root)
	f.service.keyID = trust.KeyID(root)
	// This is the account that the actual installed Hosted claim approved. No
	// owner is manufactured locally until its genuine signed assertion arrives.
	if _, e = f.db.Exec(`UPDATE networking_claim_intents SET account_id='strict-owner' WHERE operation_id=?`, f.installed.OperationID); e != nil {
		t.Fatal(e)
	}
	f.service.client = &http.Client{Transport: returnRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.String() != "https://hosted.example/v1/server-returns/exchange" {
			t.Error("credential/origin leak")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(artifact.Grant))}, nil
	})}
	who, e := f.service.ExchangeRegisteredReturn(ctx, artifact.Request)
	if e != nil {
		t.Fatal("actual signed producer rejected", e)
	}
	if who.AccountID != "strict-owner" || who.Username == "" || who.DisplayName == "" || who.Return == nil || who.Return.Purpose != "setup" {
		t.Fatal("authority/privacy projection")
	}
	out, e := f.id.CompleteAccountSetup(ctx, pendingSetupID, secret, who)
	if e != nil || out.Session == nil || out.Account.HostedAccountID != who.AccountID || out.ResetFileOwnerSession {
		t.Fatal("actual setup consumer", e)
	}
	if _, e = f.service.ExchangeRegisteredReturn(ctx, artifact.Request); e == nil {
		t.Fatal("consumer replay accepted")
	}
	if path := os.Getenv("PORTICO_RETURN_CONSUMER_JSON"); path != "" {
		result, _ := json.Marshal(map[string]any{"producer": json.RawMessage(raw), "consumer": out})
		if e = os.WriteFile(path, result, 0600); e != nil {
			t.Fatal(e)
		}
	}
}

type returnRoundTripper func(*http.Request) (*http.Response, error)

func (f returnRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRegisteredReturnInstalledTupleAndSignerRevocation(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	who := identity.PorticoIdentity{AccountID: f.installed.AccountID, SignerKeyID: trust.KeyID(f.hostedKey.Public().(ed25519.PublicKey)), Return: &identity.PorticoReturnBinding{ExpiresAt: time.Now().Add(time.Minute), Purpose: "setup", RegistrationID: "ret_verified", OperationID: f.installed.OperationID, ClaimGeneration: 1, CredentialGeneration: 1}}
	for _, change := range []func(*identity.PorticoReturnBinding){func(r *identity.PorticoReturnBinding) { r.OperationID = "another" }, func(r *identity.PorticoReturnBinding) { r.ClaimGeneration = 2 }, func(r *identity.PorticoReturnBinding) { r.CredentialGeneration = 2 },
		func(r *identity.PorticoReturnBinding) { r.ExpiresAt = time.Now().Add(-time.Second) }, func(r *identity.PorticoReturnBinding) { r.Purpose = "change-owner" }} {
		bad := who
		binding := *who.Return
		bad.Return = &binding
		change(bad.Return)
		tx, e := f.db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		e = identity.CheckPorticoReturnTx(ctx, tx, bad)
		tx.Rollback()
		if e == nil {
			t.Fatal("changed tuple admitted")
		}
	}
	if _, e := f.db.Exec(`INSERT INTO hosted_signing_revocations(key_id) VALUES(?)`, who.SignerKeyID); e != nil {
		t.Fatal(e)
	}
	tx, e := f.db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = identity.CheckPorticoReturnTx(ctx, tx, who); e == nil {
		t.Fatal("revoked signer admitted")
	}
}

func TestRegisteredReturnLocalOwnerOutcomesPreserveProfilesAndRejectMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, role := range []string{"admin", "member", "remove", "collision", "current", "stale"} {
		t.Run(role, func(t *testing.T) {
			f := newClaimFixture(t)
			ctx := context.Background()
			ownerID := f.owner.Viewer.AccountID
			profileID := f.owner.Viewer.ProfileID
			if _, e := f.db.Exec(`UPDATE account_portico_links SET hosted_account_id='previous-account',consent='signed-consent-fixture' WHERE account_id=?`, ownerID); e != nil {
				t.Fatal(e)
			}
			if _, e := f.db.Exec(`UPDATE onboarding_state_v1 SET auth_mode='hosted' WHERE singleton=1`); e != nil {
				t.Fatal(e)
			}
			requested := role
			if role == "collision" || role == "current" || role == "stale" {
				requested = "admin"
			}
			if _, e := f.db.Exec(`UPDATE networking_claim_intents SET account_id='new-account',purpose='change-owner',current_owner_account_id='previous-account',previous_owner_role=? WHERE operation_id=?`, requested, f.installed.OperationID); e != nil {
				t.Fatal(e)
			}
			who := identity.PorticoIdentity{AccountID: "new-account", Username: "new_username", Consent: "genuine-verified-assertion-fixture", SignerKeyID: trust.KeyID(f.hostedKey.Public().(ed25519.PublicKey)), Return: &identity.PorticoReturnBinding{ExpiresAt: time.Now().Add(time.Minute), Purpose: "change-owner", RegistrationID: "ret_fixture_verified", OperationID: f.installed.OperationID, ClaimGeneration: 1, CredentialGeneration: 1, CurrentOwnerAccountID: "previous-account", PreviousOwnerRole: requested}}
			// The authority method receives the same verified projection as the actual
			// producer/consumer test above; this fixture isolates the atomic local writer.
			if role == "collision" {
				who.Username = "owner"
			}
			if role == "current" {
				who.AccountID = "previous-account"
			}
			if _, e := f.db.Exec(`UPDATE networking_claim_intents SET approved_username=?,owner_change_reserved=1 WHERE operation_id=?`, who.Username, who.Return.OperationID); e != nil {
				t.Fatal(e)
			}
			var revision int64
			if e := f.db.QueryRow(`SELECT revision FROM direct_memberships WHERE account_id=?`, ownerID).Scan(&revision); e != nil {
				t.Fatal(e)
			}
			if role == "stale" {
				revision--
			}
			out, e := f.id.CompletePorticoOwnerClaim(ctx, f.owner.AccessToken, revision, who)
			if role == "collision" || role == "current" || role == "stale" {
				if e == nil {
					t.Fatal("invalid ownership admitted")
				}
				var tier string
				f.db.QueryRow(`SELECT role FROM direct_memberships WHERE account_id=?`, ownerID).Scan(&tier)
				if tier != "owner" {
					t.Fatal("failed transfer changed original")
				}
				return
			}
			if e != nil || out.Session == nil || out.Account.Role != identity.TierOwner || out.Account.HostedAccountID != "new-account" {
				t.Fatal("owner outcome", e)
			}
			var profile, tier string
			var disabled bool
			if e = f.db.QueryRow(`SELECT a.profile_id,m.role,m.disabled FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.id=?`, ownerID).Scan(&profile, &tier, &disabled); e != nil {
				t.Fatal(e)
			}
			if profile != profileID {
				t.Fatal("original profile identity changed")
			}
			if role == "remove" {
				if !disabled {
					t.Fatal("removed owner still enabled")
				}
				if _, e = f.id.Authenticate(f.owner.AccessToken); e == nil {
					t.Fatal("removed owner's session survived")
				}
			} else {
				if tier != role || disabled {
					t.Fatal("wrong previous tier")
				}
				p, e := f.id.Authenticate(f.owner.AccessToken)
				if e != nil || p.Role != role {
					t.Fatal("old session not retiered", e)
				}
			}
		})
	}
}

func TestRegisteredReturnConsumerRejectsSignedOtherServerAndAccount(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	installation := strings.Repeat("i", 43)
	challenge, _, e := f.service.IssueChallenge(ctx, installation)
	if e != nil {
		t.Fatal(e)
	}
	q := RegisteredReturnExchange{ServerChallenge: challenge, InstallationID: installation, RegistrationID: "ret_verified_fixture", Purpose: "setup"}
	c := serverReturnAssertion{Kind: "portico.server-return.v1", AccountID: f.installed.AccountID, Username: "verified_username", ServerID: f.id.ID(), Challenge: challenge, Nonce: strings.Repeat("n", 32), IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute), Purpose: q.Purpose, InstallationID: installation, RegistrationID: q.RegistrationID, ClaimOperationID: f.installed.OperationID, ClaimGeneration: 1, CredentialGeneration: 1}
	goodRaw, _ := json.Marshal(c)
	good := Signed{Payload: base64.RawURLEncoding.EncodeToString(goodRaw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.hostedKey, goodRaw)), KeyID: "pin1", Certificate: testSigningCertificate(f.hostedKey, "pin1")}
	if verified, e := f.service.verifyEnvelope(ctx, good); e != nil || !bytes.Equal(verified, goodRaw) {
		t.Fatal("fixture did not establish genuine producer signature", e)
	}

	for _, change := range []func(*serverReturnAssertion){func(c *serverReturnAssertion) { c.ServerID = "srv_other" }, func(c *serverReturnAssertion) { c.AccountID = "different-account" }, func(c *serverReturnAssertion) { c.ClaimGeneration = 2 }, func(c *serverReturnAssertion) { c.ExpiresAt = time.Now().UTC().Add(-time.Second) }} {
		bad := c
		change(&bad)
		raw, _ := json.Marshal(bad)
		signed := Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.hostedKey, raw)), KeyID: "pin1", Certificate: testSigningCertificate(f.hostedKey, "pin1")}
		if _, e = f.service.verifyRegisteredReturn(ctx, signed, q); e == nil {
			t.Fatal("signed wrong authority admitted")
		}
	}
}

// The exchange's {assertion} wrapper is a plain Hosted response and reads open
// (R-09b), but the signed assertion inside stays exact: a field an older server
// would ignore inside a signature could carry meaning it never checked.
func TestRegisteredReturnWrapperReadsOpenAndSignedAssertionStaysExact(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	installation := strings.Repeat("i", 43)
	challenge, _, e := f.service.IssueChallenge(ctx, installation)
	if e != nil {
		t.Fatal(e)
	}
	q := RegisteredReturnExchange{Code: strings.Repeat("c", 43), TransactionID: "return_transaction", Verifier: strings.Repeat("v", 43), RegistrationID: "ret_valid_shape", InstallationID: installation, ServerChallenge: challenge, Purpose: "sign-in"}
	now := time.Now().UTC()
	c := serverReturnAssertion{Kind: "portico.server-return.v1", AccountID: f.installed.AccountID, Username: "verified_owner", ServerID: f.id.ID(), Challenge: challenge, Nonce: identity.Token(), IssuedAt: now, ExpiresAt: now.Add(time.Minute), Purpose: q.Purpose, InstallationID: q.InstallationID, RegistrationID: q.RegistrationID, ClaimOperationID: f.installed.OperationID, ClaimGeneration: 1, CredentialGeneration: 1}
	sign := func(raw []byte) string {
		signed, _ := json.Marshal(Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.hostedKey, raw)), KeyID: "pin1", Certificate: testSigningCertificate(f.hostedKey, "pin1")})
		return string(signed)
	}
	exact, _ := json.Marshal(c)
	added := []byte(strings.TrimSuffix(string(exact), "}") + `,"addedByANewerHosted":true}`)
	var grant string
	f.service.client = &http.Client{Transport: returnRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(grant))}, nil
	})}
	grant = `{"assertion":` + sign(added) + `}`
	if _, e = f.service.ExchangeRegisteredReturn(ctx, q); e == nil {
		t.Fatal("signed assertion with an added field accepted")
	}
	grant = `{"assertion":` + sign(exact) + `,"addedByANewerHosted":{"nested":true}}`
	if who, e := f.service.ExchangeRegisteredReturn(ctx, q); e != nil || who.AccountID != f.installed.AccountID {
		t.Fatal("wrapper with an added field refused", e)
	}
}
