package certclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

func pemCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

type rig struct {
	t        *testing.T
	now      time.Time
	linkedAt time.Time
	key      ed25519.PrivateKey
	keys     string
	f        *fakeServices
	c        *Client
	changes  []string
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, now: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC), keys: filepath.Join(t.TempDir(), "Keys")}
	r.linkedAt = r.now.Add(-time.Minute)
	public, key, _ := ed25519.GenerateKey(nil)
	r.key = key
	r.f = newFakeServices(t, public, func() time.Time { return r.now })
	r.restart()
	return r
}

// restart is the server starting again with the same data folder.
func (r *rig) restart() {
	id, _ := trust.ServerID(r.key.Public().(ed25519.PublicKey))
	r.c = New(Options{Services: r.f.server.URL, HTTP: r.f.server.Client(), ServerID: id, Key: r.key, Keys: r.keys,
		Roots: r.f.roots, Now: func() time.Time { return r.now }, OnChange: func(s Status) { r.changes = append(r.changes, s.State) }})
}

// until steps, moving the clock to each next try, until the state is reached; it answers the
// waits it was told.
func (r *rig) until(state string) []time.Duration {
	r.t.Helper()
	var waits []time.Duration
	for range 50 {
		next := r.c.Step(context.Background())
		if r.c.Status().State == state {
			return waits
		}
		if next.IsZero() {
			r.t.Fatalf("stopped in %+v", r.c.Status())
		}
		waits = append(waits, next.Sub(r.now))
		r.now = next
	}
	r.t.Fatalf("never reached %s: %+v", state, r.c.Status())
	return nil
}

func (r *rig) served(name string) *tls.Certificate {
	cert, err := r.c.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	if err != nil {
		return nil
	}
	return cert
}

func TestALinkedServerGetsItsAddressAndCertificateInTheBackground(t *testing.T) {
	r := newRig(t)
	if next := r.c.Step(context.Background()); !next.IsZero() || len(r.f.calls) != 0 {
		t.Fatal("a server that is not linked asked Portico Services for something")
	}
	r.c.Linked(r.linkedAt)
	r.f.plan = []orderAnswer{
		{State: "queued", Position: 45, RetryAfterMs: 60_000},
		{State: "queued", Position: 10, RetryAfterMs: 30_000},
		{State: "working"}, {State: "working"}, {State: "working"}, {State: "issued"},
	}
	r.c.Step(context.Background())
	if s := r.c.Status(); s.State != "queued" || s.Message != "Portico is issuing certificates for a lot of new servers. Yours is about 3 minutes away. Nothing is needed from you." {
		t.Fatalf("in a long line: %+v", s)
	}
	waits := r.until("issued")
	// The pace the contract gives: never sooner than the line's estimate, otherwise from 5
	// seconds doubling, never more than 10 minutes.
	if waits[0] < 60*time.Second || waits[1] < 30*time.Second || waits[2] < 5*time.Second || waits[2] > 20*time.Second {
		t.Fatalf("waits %v", waits)
	}
	for _, wait := range waits {
		if wait > 10*time.Minute || wait < 2500*time.Millisecond {
			t.Fatalf("waits %v", waits)
		}
	}

	ns := r.f.nameSpace
	if r.served("203-0-113-7."+ns+".portico.direct") == nil || r.served("example.com") != nil || r.served("203-0-113-7.ptc-other.portico.direct") != nil {
		t.Fatal("the certificate is not served for exactly this server's Portico addresses")
	}
	if info, err := os.Stat(filepath.Join(r.keys, certFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the certificate and its key are not kept readable by the server alone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.keys, pendingFile)); err == nil {
		t.Fatal("the key of the order was left beside the installed certificate")
	}
	s := r.c.Status()
	if !s.CertificateReady || s.NameSpace != ns || r.f.count("POST", "/name-space") != 1 {
		t.Fatalf("status %+v, name space asked %d times", s, r.f.count("POST", "/name-space"))
	}
	if strings.Join(r.changes, " ") != "asking queued working issued" {
		t.Fatalf("the owner was told %v", r.changes)
	}
}

func TestRenewalComesWhenDueAndIsServedWithoutARestart(t *testing.T) {
	r := newRig(t)
	r.c.Linked(r.linkedAt)
	r.until("issued")
	first := r.served("1-2-3-4." + r.f.nameSpace + ".portico.direct")
	issued := r.now
	due := r.c.Step(context.Background())
	if life := due.Sub(issued); life < 52*24*time.Hour || life > 69*24*time.Hour {
		t.Fatalf("renewal set %v after issue: not between 58%% and 76%% of 90 days", life)
	}
	calls := len(r.f.calls)
	r.now = due.Add(-time.Hour)
	r.c.Step(context.Background())
	if len(r.f.calls) != calls {
		t.Fatal("Portico Services was asked before the renewal was due")
	}
	r.now = due
	r.until("working")
	r.until("issued")
	second := r.served("1-2-3-4." + r.f.nameSpace + ".portico.direct")
	if second == nil || string(second.Certificate[0]) == string(first.Certificate[0]) {
		t.Fatal("the renewed certificate is not the one served")
	}
	if second.PrivateKey.(*ecdsa.PrivateKey).Equal(first.PrivateKey) {
		t.Fatal("the renewal reused the old key")
	}
	for _, c := range r.f.calls {
		if c.noUsableCertificate {
			t.Fatal("a server with a usable certificate said it had none")
		}
	}
}

func TestAServerThatLostItsCertificateSaysSoAndOnlyThen(t *testing.T) {
	r := newRig(t)
	r.c.Linked(r.linkedAt)
	r.until("issued")
	r.now = r.now.Add(10 * 24 * time.Hour)
	// Restored from a backup without its certificate.
	os.Remove(filepath.Join(r.keys, certFile))
	r.restart()
	r.c.Linked(r.linkedAt)
	r.until("issued")
	var said []bool
	for _, c := range r.f.calls {
		if c.method == "POST" && strings.HasSuffix(c.path, "/orders") {
			said = append(said, c.noUsableCertificate)
		}
	}
	// The first order, then the new key refused as too early, then the same with "no usable
	// certificate".
	if len(said) != 3 || said[0] || said[1] || !said[2] {
		t.Fatalf("orders said no usable certificate: %v", said)
	}
	if r.f.count("POST", "/name-space") != 1 {
		t.Fatal("the name space was asked for again after a restart")
	}
}

func TestEveryRefusalWaitsAndTellsTheOwner(t *testing.T) {
	for _, c := range []struct {
		name    string
		refuse  *refusal
		paused  bool
		clock   time.Duration
		down    bool
		atLeast time.Duration
		says    string
	}{
		{name: "Portico Services busy", refuse: &refusal{503, "busy", "Portico Services is busy. Try again later.", 120, ""}, atLeast: 120 * time.Second,
			says: "Portico Services is busy."},
		{name: "the server's orders today", refuse: &refusal{429, "slow_down", "This server has ordered its certificate too many times today. It can order again later.", 3600, "/orders"},
			atLeast: time.Hour, says: "This server has ordered its certificate too many times today. It can order again later."},
		{name: "the account's orders today", refuse: &refusal{429, "slow_down", "This account's servers have ordered too many certificates today. They can order again later.", 7200, "/orders"},
			atLeast: 2 * time.Hour, says: "This account's servers have ordered too many certificates today. They can order again later."},
		{name: "replaced twice this week", refuse: &refusal{429, "replacement_limit", "This server has replaced its certificate twice this week. It can replace it again later.", 3 * 86400, "/orders"},
			atLeast: 24 * time.Hour, says: "This server has replaced its certificate twice this week. It can replace it again later."},
		{name: "the ordinary call limit", refuse: &refusal{429, "slow_down", "Too many requests. Try again later.", 60, ""}, atLeast: 60 * time.Second,
			says: "Portico Services is busy."},
		{name: "signed a moment before the link", refuse: &refusal{401, "signed_before_link", "This request was signed before this server was linked.", 0, ""},
			atLeast: 30 * time.Second, says: "Getting your secure address. This usually takes under a minute."},
		{name: "a Portico Services that issues no certificates", refuse: &refusal{503, "certificates_unavailable",
			"This Portico Services does not issue certificates.", 6 * 3600, "/orders"}, atLeast: 6 * time.Hour,
			says: "This Portico Services isn't issuing certificates right now. Your Portico address works without one; Enhanced Security waits for it."},
		{name: "the authority paused", paused: true, atLeast: 10 * time.Minute,
			says: "The certificate can't be issued just now. Portico will keep trying; nothing is needed from you."},
		{name: "unreachable", down: true, atLeast: 30 * time.Second, says: "Portico Services can't be reached."},
		{name: "the server's clock is out", clock: 3 * time.Hour, atLeast: 30 * time.Second,
			says: "This server's clock is 3 hours out, so Portico Services can't accept its requests. Correct the clock."},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.c.Linked(r.linkedAt)
			switch {
			case c.refuse != nil:
				r.f.refuse = []refusal{*c.refuse}
			case c.paused:
				r.f.plan = []orderAnswer{{State: "queued", Position: 1, RetryAfterMs: 600_000, Problem: "authority_paused"}}
			case c.down:
				r.f.server.Close()
			case c.clock != 0:
				r.f.now = func() time.Time { return r.now.Add(c.clock) }
				r.f.clockOff = true
			}
			next := r.c.Step(context.Background())
			s := r.c.Status()
			if next.Sub(r.now) < c.atLeast || s.Message != c.says || s.NextTryAt == nil || !s.NextTryAt.Equal(next) {
				t.Fatalf("waits %v and says %q", next.Sub(r.now), s.Message)
			}
			// Nothing is installed, so Enhanced security goes on waiting.
			if s.CertificateReady {
				t.Fatal("a certificate is counted as ready")
			}
			calls := len(r.f.calls)
			r.now = next.Add(-time.Second)
			r.c.Step(context.Background())
			// Nor after a restart: the wait is kept.
			r.restart()
			r.c.Linked(r.linkedAt)
			r.c.Step(context.Background())
			if len(r.f.calls) != calls {
				t.Fatal("it asked again before the wait was over")
			}
			if restored := r.c.Status(); restored.NextTryAt == nil || !restored.NextTryAt.Equal(next) {
				t.Fatal("restart lost the actual retry time", restored)
			}
		})
	}
}

func TestAServerPorticoServicesNoLongerKnowsStopsAsking(t *testing.T) {
	r := newRig(t)
	r.c.Linked(r.linkedAt)
	r.f.refuse = []refusal{{http.StatusNotFound, "server_unknown", "Portico Services does not know this server.", 0, ""}}
	if next := r.c.Step(context.Background()); !next.IsZero() || r.c.Status().State != "unlinked" ||
		r.c.Status().Message != "This server is no longer linked to Portico Services, so it has no Portico address. Link it again, or switch to local sign-in." {
		t.Fatalf("%+v", r.c.Status())
	}
	r.now = r.now.Add(48 * time.Hour)
	calls := len(r.f.calls)
	if next := r.c.Step(context.Background()); !next.IsZero() || len(r.f.calls) != calls {
		t.Fatal("it went on asking after Portico Services said it no longer knows this server")
	}
	r.c.Linked(r.linkedAt)
	r.until("issued")
}

// A server that left, or whose row Services removed, and links again is a new row at
// Services with a new name space or none: it must ask again, or apps get no address for it.
// Only a new link asks; a restart does not.
func TestLinkingAgainGetsTheNewAddressAndCertificate(t *testing.T) {
	r := newRig(t)
	r.c.Linked(r.linkedAt)
	r.until("issued")
	old := r.f.nameSpace
	r.now = r.now.Add(5 * 24 * time.Hour)

	// It leaves and links again. This server's clock is 20 seconds slow, so the link's time
	// is just ahead of it: nothing may be signed before it.
	r.linkedAt = r.now.Add(20 * time.Second)
	r.f.relink(r.linkedAt)
	r.restart()
	r.c.Linked(r.linkedAt)
	r.until("issued")
	if r.f.nameSpace == old || r.c.Status().NameSpace != r.f.nameSpace || r.f.count("POST", "/name-space") != 2 {
		t.Fatalf("name space %q (was %q), held %q, asked %d times", r.f.nameSpace, old, r.c.Status().NameSpace, r.f.count("POST", "/name-space"))
	}
	if r.f.beforeLink != 0 {
		t.Fatalf("%d requests were signed before the link was made", r.f.beforeLink)
	}
	if r.served("1-2-3-4."+r.f.nameSpace+".portico.direct") == nil || r.served("1-2-3-4."+old+".portico.direct") != nil {
		t.Fatal("the certificate served is not the new name space's")
	}
	for _, c := range r.f.calls {
		if c.noUsableCertificate {
			t.Fatal("the new link's first order said it had no usable certificate; it is an ordinary first order")
		}
	}
	// A restart with the same link asks for nothing.
	calls := len(r.f.calls)
	r.restart()
	r.c.Linked(r.linkedAt)
	r.c.Step(context.Background())
	if len(r.f.calls) != calls {
		t.Fatal("a restart asked Portico Services again")
	}
}

// A server that switched to local sign-in contacts Portico Services no more, renewal included.
func TestAServerThatIsNoLongerLinkedAsksNothing(t *testing.T) {
	r := newRig(t)
	r.c.Linked(r.linkedAt)
	r.until("issued")
	r.c.Unlinked()
	calls := len(r.f.calls)
	r.now = r.now.Add(80 * 24 * time.Hour) // past its renewal
	if next := r.c.Step(context.Background()); !next.IsZero() || len(r.f.calls) != calls {
		t.Fatal("a server that is no longer linked asked Portico Services for something")
	}
}

func TestCertificateRenewalFactsKeepTheGoodCertificateAndRememberTheActualAttempt(t *testing.T) {
	r := newRig(t)
	r.c.Linked(r.linkedAt)
	r.until("issued")
	before := r.c.Status()
	if before.LastAttemptAt == nil || before.LastProblem != "" || before.ExpiresAt == nil {
		t.Fatal(before)
	}
	r.now = renewAt(r.c.leaf).Add(time.Second)
	r.f.refuse = []refusal{{503, "busy", "Portico Services is busy.", 120, "/orders"}}
	r.c.Step(context.Background())
	failed := r.c.Status()
	if failed.LastAttemptAt == nil || !failed.LastAttemptAt.Equal(r.now) || !strings.Contains(failed.LastProblem, "busy") {
		t.Fatal(failed)
	}
	if !failed.CertificateReady || r.served("203-0-113-1."+failed.NameSpace+"."+Zone) == nil {
		t.Fatal("a refused renewal took away the installed certificate")
	}
	r.restart()
	r.c.Linked(r.linkedAt)
	r.c.Step(context.Background())
	restored := r.c.Status()
	if restored.LastAttemptAt == nil || !restored.LastAttemptAt.Equal(*failed.LastAttemptAt) ||
		restored.LastProblem != "Portico Services is busy." || strings.Contains(restored.LastProblem, "Trying again") {
		t.Fatal("restart lost the last real attempt or its reason", restored)
	}
}
