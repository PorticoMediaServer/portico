package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestCurrentNetworkingStartupOwnerAndRestart(t *testing.T) {
	state := t.TempDir()
	if e := os.Chmod(state, 0700); e != nil {
		t.Fatal(e)
	}
	runner, e := openCurrentAuthority(state)
	if e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if e = persistence.VerifyNetworkingClaims(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	keys, e := networking.OpenCurrentKeys(context.Background(), db, state, runner)
	if e != nil {
		t.Fatal(e)
	}
	ident, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	operations.WireSetupRemote(ident)
	var server string
	var pub []byte
	if e = db.QueryRow(`SELECT server_id,public_key FROM networking_claim_identity`).Scan(&server, &pub); e != nil {
		t.Fatal(e)
	}
	derived, _ := networking.ServerIdentity(pub)
	if server != derived || ident.ID() != derived {
		t.Fatal("detached identities")
	}
	setup, e := os.ReadFile(storagepaths.Path(state, "setup-token"))
	if e != nil {
		t.Fatal(e)
	}
	web, e := identity.WithIssuingDevice(context.Background(), identity.DeviceRegistration{InstallationID: identity.Token(), Name: "Owner browser", Platform: "web", App: "portico"}, "127.0.0.1:1234")
	if e != nil {
		t.Fatal(e)
	}
	created, e := ident.SetupWithOptions(web, identity.SetupOptions{SetupToken: string(setup), Username: "owner", Password: "test-owner-password", Name: "Current server", AuthMode: "local"}, true)
	if e != nil {
		t.Fatal(e)
	}
	owner := created.Envelope
	// A configured Local server enrolls only through its owner's confirmed
	// switch. Use the real identity service, rather than seeding move rows or
	// preparing a setup-purpose claim that cannot leave a Local server.
	move, e := ident.PreviewAccountMove(web, owner.AccessToken, identity.MovePreviewRequest{To: identity.SignInPortico})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ident.ConfirmPorticoMove(web, owner.AccessToken, identity.MoveConfirmRequest{MoveID: move.ID, ExpectedRevision: move.Revision}); e != nil {
		t.Fatal(e)
	}
	hostedPub, _, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("PORTICO_HOSTED_ORIGIN", "https://hosted.example")
	t.Setenv("PORTICO_HOSTED_PUBLIC_KEY", base64.RawURLEncoding.EncodeToString(hostedPub))
	t.Setenv("PORTICO_HOSTED_KEY_ID", trust.KeyID(hostedPub))
	control, e := hosted.New(db, ident, "https://hosted.example", base64.RawURLEncoding.EncodeToString(hostedPub), trust.KeyID(hostedPub))
	if e != nil {
		t.Fatal(e)
	}
	handler, e := initializeCurrentClaimControl(db, state, ident, runner, keys, control, []string{"https://web.example"})
	if e != nil {
		t.Fatal(e)
	}
	request := httptest.NewRequest("GET", "https://web.example/v1/networking/claim", nil)
	request.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	request.Header.Set("Origin", "https://web.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("actual owner rejected", response.Code, response.Body.String())
	}
	var status networking.ClaimStatus
	if e = json.Unmarshal(response.Body.Bytes(), &status); e != nil || status.Identity.ServerID != derived {
		t.Fatal("bad current status", e)
	}

	// Exercise the actual database-backed display-name callback while preparing.
	// Component fixtures with a literal name cannot detect nested connection borrowing.
	raw, _ := json.Marshal(networking.ClaimPrepareRequest{Purpose: "switch", Expected: status.Identity})
	prepare := httptest.NewRequest("POST", "https://web.example/v1/networking/claim/prepare", bytes.NewReader(raw))
	prepare.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	prepare.Header.Set("Origin", "https://web.example")
	prepare.Header.Set("Content-Type", "application/json")
	prepareContext, cancelPrepare := context.WithTimeout(context.Background(), 2*time.Second)
	prepareResponse := httptest.NewRecorder()
	handler.ServeHTTP(prepareResponse, prepare.WithContext(prepareContext))
	cancelPrepare()
	if prepareResponse.Code != 200 {
		t.Fatal("actual owner preparation failed", prepareResponse.Code, prepareResponse.Body.String())
	}
	var prepared networking.ClaimStatus
	if e = json.Unmarshal(prepareResponse.Body.Bytes(), &prepared); e != nil || prepared.ApprovalRequest == nil || prepared.ApprovalRequest.Name != "Current server" || prepared.ApprovalRequest.Purpose != "switch" {
		t.Fatal("missing current approval display name", e)
	}
	request.Header.Set("Origin", "https://untrusted.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("untrusted origin allowed")
	}
	keys.Close()
	db.Close()
	runner, e = openCurrentAuthority(state)
	if e != nil {
		t.Fatal(e)
	}
	db, e = persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = persistence.VerifyNetworkingClaims(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	keys, e = networking.OpenCurrentKeys(context.Background(), db, state, runner)
	if e != nil {
		t.Fatal(e)
	}
	defer keys.Close()
	if persistence.Get(db, "id") != derived {
		t.Fatal("restart changed identity")
	}
}
func TestCurrentNetworkingMissingIncarnationRefused(t *testing.T) {
	state := t.TempDir()
	if e := writePathFixture(storagepaths.Path(state, "server.sqlite"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := openCurrentAuthority(state); e == nil {
		t.Fatal("old database adopted")
	}
}

// The custom certificate domain publishes as an HTTPS origin on the remote
// public port, with no :port at 443; the access URL list passes through.
func TestAccessURLsForPublicationComposesDomainRoute(t *testing.T) {
	v := operations.Settings{AccessURLs: []string{"https://vpn.example.com:8443"}, CustomCertificateDomain: "media.example.com"}
	got := accessURLsForPublication(v, 32500)
	if len(got) != 2 || got[0] != "https://vpn.example.com:8443" || got[1] != "https://media.example.com:32500" {
		t.Fatalf("port 32500: %v", got)
	}
	got = accessURLsForPublication(v, 443)
	if len(got) != 2 || got[1] != "https://media.example.com" {
		t.Fatalf("port 443: %v", got)
	}
	got = accessURLsForPublication(operations.Settings{}, 32500)
	if got == nil || len(got) != 0 {
		t.Fatalf("empty settings must clear, not nil: %v", got)
	}
}

func TestPublicationPreservesPlainHTTPAndExternalAddress(t *testing.T) {
	v := operations.Settings{AccessURLs: []string{"http://overlay.example:32500", "https://media.example"}, AdvertiseURL: "https://external.example"}
	got := accessURLsForPublication(v, 32500)
	if len(got) != 3 || got[2] != "https://media.example" || got[1] != "http://overlay.example:32500" || got[0] != "https://external.example" {
		t.Fatalf("%v", got)
	}
	v.AdvertiseURL = "http://external.example"
	got = accessURLsForPublication(v, 32500)
	if len(got) != 3 || got[0] != "http://external.example" {
		t.Fatalf("owner HTTP address was lost: %v", got)
	}
}
