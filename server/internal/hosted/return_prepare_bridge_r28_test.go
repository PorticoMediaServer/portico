package hosted

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func connectActualReturnBridge(t *testing.T, f *claimFixture, b *repairBridge) {
	t.Helper()
	if b.serverToken == "" {
		t.Fatal("actual producer must provide disposable installed credential privately")
	}
	if _, e := f.db.Exec(`UPDATE networking_claim_credentials SET nonce=X'',ciphertext=? WHERE operation_id=?`, []byte(b.serverToken), f.installed.OperationID); e != nil {
		t.Fatal(e)
	}
	f.service.key = ed25519.PublicKey(b.root)
	f.service.keyID = trust.KeyID(b.root)
	rt := returnRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "hosted.example" || r.Header.Get("Cookie") != "" {
			t.Fatal("actual bridge audience/credential leak")
		}
		raw, _ := io.ReadAll(r.Body)
		reply := b.call("http", r.URL.RequestURI(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), json.RawMessage(raw))
		body := []byte(reply.Body)
		if reply.BodyEmpty {
			body = nil
		}
		return &http.Response{StatusCode: reply.Status, Header: reply.Headers, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	f.service.current.transport.UseRoundTripper(rt)
	f.service.client = &http.Client{Transport: rt}
}

func TestRegisteredReturnOfflineRefusesClearlyWithoutEndingExistingSession(t *testing.T) {
	f := newClaimFixture(t)
	f.service.client = &http.Client{Transport: returnRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	_, e := f.service.ExchangeRegisteredReturn(context.Background(), RegisteredReturnExchange{Code: strings.Repeat("c", 43), Verifier: strings.Repeat("v", 43), TransactionID: "srt_disposable", RegistrationID: "ret_disposable", ServerChallenge: strings.Repeat("c", 32), InstallationID: strings.Repeat("i", 43), Purpose: "sign-in"})
	if !errors.Is(e, ErrServicesUnavailable) {
		t.Fatal("new sign-in hid unavailable Services", e)
	}
	if _, e = f.id.Authenticate(f.owner.AccessToken); e != nil {
		t.Fatal("offline refusal ended existing server session", e)
	}
}

func TestBrowserPreparationActualProducerConsentAndOfflineDeparture(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	if _, e := f.db.Exec(`UPDATE account_portico_links SET hosted_account_id='strict-owner';UPDATE networking_claim_intents SET account_id='strict-owner' WHERE operation_id=?`, f.installed.OperationID); e != nil {
		t.Fatal(e)
	}
	f.installed.AccountID = "strict-owner"
	b := startOwnerBridge(t, f)
	connectActualReturnBridge(t, f, b)
	const origin = "https://server.example:32500"
	const installation = "iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii"
	if _, e := f.service.PrepareRegisteredReturn(ctx, "https://unregistered.example"); e == nil {
		t.Fatal("unregistered origin admitted")
	}
	getIdentity := func() identity.PorticoIdentity {
		t.Helper()
		p, e := f.service.PrepareRegisteredReturn(ctx, origin)
		if e != nil || p.Registration.Origin != origin || p.ServerID != f.id.ID() || p.ClaimOperationID != f.installed.OperationID {
			t.Fatal("actual preparation", e)
		}
		challenge, _, e := f.service.IssueChallenge(ctx, installation)
		if e != nil {
			t.Fatal(e)
		}
		verifier := strings.Repeat("v", 43)
		sum := sha256.Sum256([]byte(verifier))
		start := map[string]string{"serverId": p.ServerID, "purpose": "sign-in", "claimOperationId": p.ClaimOperationID, "registrationId": p.Registration.ID, "state": strings.Repeat("s", 43), "codeChallenge": base64.RawURLEncoding.EncodeToString(sum[:]), "serverChallenge": challenge, "installationId": installation}
		var started struct{ TransactionID string }
		if e = b.post("/v1/server-returns/start", b.targetToken, start, &started); e != nil {
			t.Fatal("actual start", e)
		}
		var completed struct{ Location string }
		if e = b.post("/v1/server-returns/"+started.TransactionID+"/complete", b.targetToken, struct{}{}, &completed); e != nil {
			t.Fatal("actual completion", e)
		}
		u, e := url.Parse(completed.Location)
		if e != nil || u.Scheme+"://"+u.Host != origin || u.Path != "/auth/portico-return" {
			t.Fatal("registered origin return", e)
		}
		values, _ := url.ParseQuery(u.Fragment)
		who, e := f.service.ExchangeRegisteredReturn(ctx, RegisteredReturnExchange{Code: values.Get("code"), TransactionID: started.TransactionID, Verifier: verifier, RegistrationID: p.Registration.ID, ServerChallenge: challenge, InstallationID: installation, Purpose: "sign-in"})
		if e != nil {
			t.Fatal("actual Server exchange", e)
		}
		return who
	}
	who := getIdentity()
	if _, e := f.id.PorticoSignIn(ctx, who); !errors.Is(e, identity.ErrAccessRefused) {
		t.Fatal("identity invented local membership", e)
	}
	if list := b.call("http", "/v1/servers", b.targetToken, nil); list.Status != 200 || bytes.Contains(list.Body, []byte(f.id.ID())) {
		t.Fatal("identity invented directory membership")
	}
	if _, e := f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('local-member','old-name',X'','kept-profile');INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('local-member','strict-other');UPDATE direct_profiles SET name='Kept profile' WHERE id='kept-profile'`); e != nil {
		t.Fatal(e)
	}
	who = getIdentity()
	if _, e := f.id.PorticoSignIn(ctx, who); e != nil {
		t.Fatal("existing member admission", e)
	}
	if e := f.runner.Do(ctx, func(ctx context.Context) error {
		v, e := f.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		q, e := f.service.memberSyncState(ctx)
		if e != nil {
			return e
		}
		return f.service.pushMembers(ctx, v, q)
	}); e != nil {
		t.Fatal("actual authenticated member push", e)
	}
	var unindexed bool
	if e := f.db.QueryRow(`SELECT unindexed FROM account_portico_links WHERE account_id='local-member'`).Scan(&unindexed); e != nil || unindexed {
		t.Fatal("unchanged registered-return consent was refused", e)
	}
	if list := b.call("http", "/v1/servers", b.targetToken, nil); list.Status != 200 || !bytes.Contains(list.Body, []byte(f.id.ID())) {
		t.Fatalf("actual directory membership missing %d %s", list.Status, list.Body)
	}
	if e := f.service.ConfigureIdentityDepartures(f.keys); e != nil {
		t.Fatal(e)
	}
	f.id.PrepareIdentityDeparture = f.service.PrepareIdentityDeparture
	web, e := identity.WithIssuingDevice(ctx, identity.DeviceRegistration{InstallationID: identity.Token(), Name: "Owner browser", Platform: "web", App: "portico"}, "192.0.2.1")
	if e != nil {
		t.Fatal(e)
	}
	owner, e := f.id.IssueWithDevice(web, f.owner.Viewer.AccountID, f.owner.Viewer.ProfileID, "local", identity.TierOwner, 1)
	if e != nil {
		t.Fatal(e)
	}
	move, e := f.id.PreviewAccountMove(web, owner.AccessToken, identity.MovePreviewRequest{To: identity.SignInLocal})
	if e != nil {
		t.Fatal(e)
	}
	// Flip succeeds while the transport is offline. Only exact queued bytes may
	// later cross the Local service fence.
	f.service.current.transport.UseRoundTripper(returnRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }))
	if _, e = f.id.ConfirmLocalMove(web, owner.AccessToken, identity.MoveConfirmRequest{MoveID: move.ID, ExpectedRevision: move.Revision, Username: "owner", Password: "New-owner1!"}); e != nil {
		t.Fatal("offline Local move", e)
	}
	if kind, _ := f.id.SignInKind(ctx); kind != identity.SignInLocal {
		t.Fatal("offline move did not flip")
	}
	var payload, signature []byte
	if e = f.db.QueryRow(`SELECT payload,signature FROM identity_service_outbox WHERE delivered_ms=0`).Scan(&payload, &signature); e != nil {
		t.Fatal("durable signed departure", e)
	}
	if e = f.service.DeliverIdentityDepartures(ctx); e == nil {
		t.Fatal("offline delivery reported success")
	}
	connectActualReturnBridge(t, f, b)
	if _, e = f.db.Exec(`UPDATE identity_service_outbox SET next_ms=0`); e != nil {
		t.Fatal(e)
	}
	if e = f.service.DeliverIdentityDepartures(ctx); e != nil {
		t.Fatal("actual queued departure", e)
	}
	if e = f.service.DeliverIdentityDepartures(ctx); e != nil {
		t.Fatal("drained outbox", e)
	}
	if reply := b.call("http", "/v1/servers/leave", "", map[string]string{"payload": base64.RawURLEncoding.EncodeToString(payload), "signature": base64.RawURLEncoding.EncodeToString(signature)}); reply.Status != 204 || !reply.BodyEmpty {
		t.Fatal("actual departure replay", reply.Status)
	}
	if list := b.call("http", "/v1/servers", b.targetToken, nil); list.Status != 200 || bytes.Contains(list.Body, []byte(f.id.ID())) {
		t.Fatal("leave kept discovery row")
	}
	if _, e = f.service.PrepareRegisteredReturn(ctx, origin); !errors.Is(e, identity.ErrSignInKindLocal) {
		t.Fatal("Local attempted return preparation", e)
	}
	var name string
	if e = f.db.QueryRow(`SELECT name FROM direct_profiles WHERE id='kept-profile'`).Scan(&name); e != nil || name != "Kept profile" {
		t.Fatal("departure erased local profile", e)
	}
}
