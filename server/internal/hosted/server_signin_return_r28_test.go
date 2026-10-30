package hosted

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
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

type actualReturnArtifact struct {
	Grant   json.RawMessage
	Root    string
	Request RegisteredReturnExchange
}

func actualSignInReturn(t *testing.T, f *claimFixture, installation string) actualReturnArtifact {
	t.Helper()
	producer := os.Getenv("PORTICO_RETURN_HOSTED_CHECKOUT")
	if producer == "" {
		t.Skip("set actual Hosted checkout for PostgreSQL producer boundary")
	}
	if os.Getenv("PORTICO_TEST_DATABASE_URL_FILE") == "" {
		t.Fatal("actual PostgreSQL producer is required, cannot accept a skip")
	}
	challenge, _, e := f.service.IssueChallenge(context.Background(), installation)
	if e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(storagepaths.Path(f.state, "networking-keys", "*.ed25519"))
	if len(files) != 1 {
		t.Fatal("fixture key")
	}
	key, e := os.ReadFile(files[0])
	if e != nil {
		t.Fatal(e)
	}
	defer clear(key)
	input := map[string]string{"ServerID": f.id.ID(), "PublicKey": base64.RawURLEncoding.EncodeToString(ed25519.PrivateKey(key).Public().(ed25519.PublicKey)), "PrivateKey": base64.RawURLEncoding.EncodeToString(key), "OperationID": f.installed.OperationID, "Challenge": challenge, "InstallationID": installation}
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	raw, _ := json.Marshal(input)
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	clear(raw)
	output := filepath.Join(dir, "producer.json")
	cmd := exec.Command("go", "test", "./internal/postgres", "-run", "^TestRegisteredReturnRawProducerArtifact$", "-count=1")
	cmd.Dir = filepath.Join(producer, "hosted-services/hosted")
	cmd.Env = append(os.Environ(), "PORTICO_RETURN_CONSUMER_INPUT="+path, "PORTICO_RETURN_PRODUCER_JSON="+output, "PORTICO_RETURN_PRODUCER_PURPOSE=sign-in")
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("actual producer: %v %s", e, raw)
	}
	raw, e = os.ReadFile(output)
	if e != nil {
		t.Fatal("producer did not run", e)
	}
	var a actualReturnArtifact
	if e = json.Unmarshal(raw, &a); e != nil {
		t.Fatal(e)
	}
	root, e := base64.RawURLEncoding.DecodeString(a.Root)
	if e != nil {
		t.Fatal(e)
	}
	f.service.key = ed25519.PublicKey(root)
	f.service.keyID = trust.KeyID(root)
	f.service.client = &http.Client{Transport: returnRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.String() != "https://hosted.example/v1/server-returns/exchange" {
			t.Error("credential/audience leak")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(a.Grant))}, nil
	})}
	if out := os.Getenv("PORTICO_SIGNIN_RETURN_ARTIFACT"); out != "" {
		if e = os.WriteFile(out, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return a
}
func TestRegisteredSignInActualProducerCannotClaimAndKeepsProfileNames(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	installation := strings.Repeat("i", 43)
	a := actualSignInReturn(t, f, installation)
	who, e := f.service.ExchangeRegisteredReturn(ctx, a.Request)
	if e != nil {
		t.Fatal("real sign-in return", e)
	}
	if who.AccountID != "strict-other" || who.Username != "strict-other" || who.Return == nil || who.Return.Purpose != "sign-in" || who.Return.CurrentOwnerAccountID != "" || who.Return.PreviousOwnerRole != "" {
		t.Fatalf("actual identity projection %+v", who)
	}
	if _, e = f.id.PorticoSignIn(ctx, who); !errors.Is(e, identity.ErrAccessRefused) {
		t.Fatal("unlinked return created membership", e)
	}
	if _, e = f.id.CompletePorticoOwnerClaim(ctx, f.owner.AccessToken, 1, who); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("member return claimed server", e)
	}
	if _, e = f.service.ExchangeRegisteredReturn(ctx, a.Request); e == nil {
		t.Fatal("return challenge replay")
	}
	if _, e = f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,display_name) VALUES('local-return-member','old-username',X'','local-return-profile','Old identity');INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('local-return-member','strict-other');UPDATE direct_profiles SET name='My chosen profile name' WHERE id='local-return-profile'`); e != nil {
		t.Fatal(e)
	}
	a = actualSignInReturn(t, f, installation)
	who, e = f.service.ExchangeRegisteredReturn(ctx, a.Request)
	if e != nil {
		t.Fatal(e)
	}
	signed, e := f.id.PorticoSignIn(ctx, who)
	if e != nil || signed.Account.ID != "local-return-member" || signed.Account.Username != "strict-other" {
		t.Fatalf("member return %+v %v", signed, e)
	}
	var name, display string
	if e = f.db.QueryRow(`SELECT p.name,a.display_name FROM accounts a JOIN direct_profiles p ON p.id=a.profile_id WHERE a.id='local-return-member'`).Scan(&name, &display); e != nil || name != "My chosen profile name" || display != who.DisplayName {
		t.Fatalf("identity/profile refresh %q %q %v", name, display, e)
	}
	bad := who
	copyBinding := *who.Return
	bad.Return = &copyBinding
	bad.Return.CurrentOwnerAccountID = "account"
	if _, e = f.id.PorticoSignIn(ctx, bad); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("sign-in borrowed owner outcome", e)
	}
	var op string
	if e = f.db.QueryRow(`SELECT installed_operation_id FROM networking_claim_identity`).Scan(&op); e != nil || op != f.installed.OperationID {
		t.Fatal("sign-in changed claim", op, e)
	}
	expires, e := time.Parse(time.RFC3339, signed.Session.AuthorizationHorizon)
	if e != nil || time.Until(expires) > identity.LocalSignInWindow {
		t.Fatal("offline horizon", e)
	}
}
