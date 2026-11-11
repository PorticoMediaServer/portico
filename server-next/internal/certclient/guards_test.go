package certclient

// The guards the security review of #2575 asked for, from its probes: no wait is obeyed or
// kept beyond its cap, a refusal Services keeps giving is not asked every second, only the
// key this server made is ever used, a signed request goes nowhere else, and no test reaches
// the real Portico Services.

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// The contract says an order's retryAfterMs is at most 10 minutes. An answer that says
// longer (a fault at Portico Services, or milliseconds sent where seconds were meant) must
// not park the certificate for years: the wait is kept on disk, so not even a restart
// would end it.
func TestAnEndlessWaitOnAnOrderIsNotObeyed(t *testing.T) {
	r := newRig(t)
	r.f.plan = []orderAnswer{{State: "queued", Position: 3, RetryAfterMs: (50 * 365 * 24 * time.Hour).Milliseconds()}}
	r.c.Linked(r.linkedAt)
	next := r.c.Step(context.Background())
	if wait := next.Sub(r.now); wait > 10*time.Minute {
		t.Errorf("an order answered \"try again in 50 years\" and the server will wait %s; the contract's longest is 10 minutes", wait)
	}
	r.restart()
	r.c.Linked(r.linkedAt)
	if next := r.c.Step(context.Background()); next.Sub(r.now) > 10*time.Minute {
		t.Errorf("after a restart the server still waits %s for its certificate", next.Sub(r.now))
	}
}

// The same for Retry-After on a refusal: the client waits at most a day on Services' word, and
// a wait kept from before (an earlier build, say) is cut to a day too.
func TestAnEndlessRetryAfterIsCappedAtADayAndSoIsWhatIsKept(t *testing.T) {
	r := newRig(t)
	r.f.refuse = []refusal{{status: http.StatusServiceUnavailable, code: "busy", message: "busy", retryAfter: 50 * 365 * 24 * 3600}}
	r.c.Linked(r.linkedAt)
	next := r.c.Step(context.Background())
	if wait := next.Sub(r.now); wait > 24*time.Hour {
		t.Errorf("a refusal said Retry-After 50 years and the server will wait %s", wait)
	}
	r.c.state.NextTryAt = r.now.Add(50 * 365 * 24 * time.Hour)
	r.c.save()
	r.restart()
	r.c.Linked(r.linkedAt)
	if next := r.c.Step(context.Background()); next.Sub(r.now) > 24*time.Hour {
		t.Errorf("a kept wait of 50 years is still %s after a restart", next.Sub(r.now))
	}
}

// A test that builds the client with the real address and no fake gets a connection that
// refuses: no test can ever reach the real Portico Services.
func TestTestsNeverReachTheRealPorticoServices(t *testing.T) {
	r := newRig(t)
	id, _ := trust.ServerID(r.key.Public().(ed25519.PublicKey))
	c := New(Options{ServerID: id, Key: r.key, Keys: r.keys, Now: func() time.Time { return r.now }})
	if _, ok := c.o.HTTP.Transport.(refuseAll); !ok {
		t.Fatal("a client built in a test with the real address can reach it")
	}
	c.Linked(r.linkedAt)
	c.Step(context.Background())
	if s := c.Status(); !strings.HasPrefix(s.Message, "Portico Services can't be reached.") {
		t.Fatalf("%+v", s)
	}
}

// A chain that carries a private key of its own must not be installed: the certificate must
// be for the key this server made, and that key is the only one ever written to Keys.
func TestAChainCarryingItsOwnKeyIsNotInstalled(t *testing.T) {
	r := newRig(t)
	r.f.plan = []orderAnswer{{State: "working"}}
	r.c.Linked(r.linkedAt)
	r.c.Step(context.Background())
	if r.c.state.NameSpace == "" {
		t.Fatal("no name space yet")
	}
	ours, err := r.c.pendingKey()
	if err != nil {
		t.Fatal(err)
	}
	// A certificate for this server's name, from an authority the server trusts, but for
	// somebody else's key; that key travels in the "chain".
	theirs, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(7), DNSNames: []string{Name(r.c.state.NameSpace)},
		NotBefore: r.now.Add(-time.Hour), NotAfter: r.now.Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, template, r.f.ca, &theirs.PublicKey, r.f.caKey)
	theirDER, _ := x509.MarshalPKCS8PrivateKey(theirs)
	chain := append(pemCert(der), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: theirDER})...)

	if err := r.c.install(chain, ours, r.now); err == nil {
		t.Error("a certificate for another key was installed because its key came along in the chain")
	}
	if raw, err := os.ReadFile(filepath.Join(r.keys, certFile)); err == nil && strings.Contains(string(raw), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: theirDER}))) {
		t.Error("a private key this server did not make was written to Keys/portico.pem")
	}
	if cert := r.served("192-0-2-7." + r.c.state.NameSpace + "." + Zone); cert != nil {
		t.Error("the server now serves a certificate whose key it did not make")
	}
}

// A wrong answer that Portico Services keeps giving must be waited out on the backoff like
// every other refusal, not asked again every second.
func TestARepeatedRefusalIsNotAskedEverySecond(t *testing.T) {
	r := newRig(t)
	for range 5000 {
		r.f.refuse = append(r.f.refuse, refusal{status: http.StatusConflict, code: "name_space_missing", message: "Ask for the name space first.", path: "/orders"})
	}
	r.c.Linked(r.linkedAt)
	end := r.now.Add(10 * time.Minute)
	for r.now.Before(end) {
		next := r.c.Step(context.Background())
		if !next.After(r.now) {
			next = r.now.Add(time.Second) // what Run does with a time that has passed
		}
		r.now = next
	}
	r.f.mu.Lock()
	calls := len(r.f.calls)
	r.f.mu.Unlock()
	// On the change backoff (1 minute, doubling) ten minutes hold about four tries of two calls.
	if calls > 20 {
		t.Errorf("ten minutes of the same refusal made %d calls to Portico Services, want a handful", calls)
	}
}

// A signed request is not sent on to wherever an answer points.
func TestASignedRequestDoesNotFollowARedirect(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(trust.RequestSignatureHeader) != "" {
			elsewhere.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer other.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer front.Close()

	r := newRig(t)
	id, _ := trust.ServerID(r.key.Public().(ed25519.PublicKey))
	// No HTTP client is given, so this is the one a real server uses.
	c := New(Options{Services: front.URL, ServerID: id, Key: r.key, Keys: r.keys, Roots: r.f.roots, Now: func() time.Time { return r.now }})
	c.Linked(r.linkedAt)
	c.Step(context.Background())
	if n := elsewhere.Load(); n != 0 {
		t.Errorf("%d signed requests were sent on to the address a redirect named", n)
	}
}
