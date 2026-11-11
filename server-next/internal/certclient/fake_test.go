package certclient

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// fakeServices is Portico Services as API.md describes it, for one server, in memory: every
// request must be signed by the server's identity key, the order route is safe to repeat by
// key, a new key is too early before 55% of the current certificate's life unless the server
// says it has no usable certificate, and an order goes through the states it is told to.
type fakeServices struct {
	t      *testing.T
	now    func() time.Time
	public ed25519.PublicKey
	id     string
	server *httptest.Server
	caKey  *ecdsa.PrivateKey
	ca     *x509.Certificate
	roots  *x509.CertPool

	mu        sync.Mutex
	nameSpace string
	orders    map[string]*fakeOrder
	byKey     map[[32]byte]string
	current   *x509.Certificate
	// plan is what every new order answers on successive asks; the last one repeats.
	plan []orderAnswer
	// refuse are answers given in place of the next requests: a status, a code, a message
	// and a Retry-After in seconds.
	refuse []refusal
	calls  []call
	// clockOff: the test puts the server's clock out on purpose, so a refused signature is
	// expected rather than a failure of the client.
	clockOff bool
}

type refusal struct {
	status     int
	code       string
	message    string
	retryAfter int
}

type call struct {
	method, path        string
	noUsableCertificate bool
}

type fakeOrder struct {
	id   string
	csr  *x509.CertificateRequest
	asks int
	plan []orderAnswer
	done orderAnswer
}

func newFakeServices(t *testing.T, public ed25519.PublicKey, now func() time.Time) *fakeServices {
	f := &fakeServices{t: t, now: now, public: public, orders: map[string]*fakeOrder{}, byKey: map[[32]byte]string{},
		plan: []orderAnswer{{State: "working"}, {State: "issued"}}}
	f.id, _ = trust.ServerID(public)
	f.caKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake authority"},
		NotBefore: time.Unix(0, 0), NotAfter: time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, _ := x509.CreateCertificate(rand.Reader, template, template, &f.caKey.PublicKey, f.caKey)
	f.ca, _ = x509.ParseCertificate(der)
	f.roots = x509.NewCertPool()
	f.roots.AddCert(f.ca)
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeServices) fail(w http.ResponseWriter, status int, code, message string, retryAfter int) {
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.Header().Set("Date", f.now().UTC().Format(http.TimeFormat))
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retry": true, "requestId": "req_fake"}})
}

func (f *fakeServices) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if trust.VerifyRequest(f.public, r.Method, r.URL.RequestURI(), r.Header.Get(trust.RequestTimeHeader), r.Header.Get(trust.RequestSignatureHeader), body, f.now()) != nil {
		if !f.clockOff {
			f.t.Errorf("%s %s was not signed by the server's identity key", r.Method, r.URL.Path)
		}
		f.fail(w, http.StatusUnauthorized, "signature", "not signed", 0)
		return
	}
	var in orderRequest
	json.Unmarshal(body, &in)
	f.calls = append(f.calls, call{r.Method, r.URL.Path, in.NoUsableCertificate})
	if len(f.refuse) > 0 {
		next := f.refuse[0]
		f.refuse = f.refuse[1:]
		f.fail(w, next.status, next.code, next.message, next.retryAfter)
		return
	}
	prefix := "/v1/servers/" + f.id + "/certificate/"
	switch {
	case r.Method == "POST" && r.URL.Path == prefix+"name-space":
		if f.nameSpace == "" {
			f.nameSpace = "ptc-" + strings.Repeat("a", 19) + "b"
		}
		json.NewEncoder(w).Encode(map[string]string{"nameSpace": f.nameSpace, "name": Name(f.nameSpace), "zone": Zone})
	case r.Method == "POST" && r.URL.Path == prefix+"orders":
		f.place(w, in)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, prefix+"orders/"):
		o := f.orders[strings.TrimPrefix(r.URL.Path, prefix+"orders/")]
		if o == nil {
			f.fail(w, http.StatusNotFound, "not_found", "This server has no certificate order with that id.", 0)
			return
		}
		json.NewEncoder(w).Encode(f.advance(o))
	default:
		f.fail(w, http.StatusNotFound, "not_found", "There is nothing at this address.", 0)
	}
}

func (f *fakeServices) place(w http.ResponseWriter, in orderRequest) {
	csr, err := x509.ParseCertificateRequest(in.CSR)
	if err != nil || csr.CheckSignature() != nil || len(csr.DNSNames) != 1 || csr.DNSNames[0] != Name(f.nameSpace) {
		f.fail(w, http.StatusUnprocessableEntity, "invalid", "The signing request must be for exactly this server's name.", 0)
		return
	}
	digest := sha256.Sum256(csr.RawSubjectPublicKeyInfo)
	if id, ok := f.byKey[digest]; ok {
		json.NewEncoder(w).Encode(f.answer(f.orders[id]))
		return
	}
	if f.current != nil {
		life := f.current.NotAfter.Sub(f.current.NotBefore)
		opens := f.current.NotBefore.Add(life * 55 / 100)
		if f.now().Before(opens) && !in.NoUsableCertificate {
			f.fail(w, http.StatusConflict, "too_early", "This server's certificate is not due for renewal yet.", int(opens.Sub(f.now()).Seconds())+1)
			return
		}
	}
	o := &fakeOrder{id: fmt.Sprintf("cert_%d", len(f.orders)+1), csr: csr, plan: f.plan}
	f.orders[o.id], f.byKey[digest] = o, o.id
	json.NewEncoder(w).Encode(f.advance(o))
}

// advance answers the order's next state; issuing makes the certificate, valid from now.
func (f *fakeServices) advance(o *fakeOrder) orderAnswer {
	if o.done.State != "" {
		return o.done
	}
	step := o.plan[min(o.asks, len(o.plan)-1)]
	o.asks++
	step.ID = o.id
	if step.State == "issued" {
		now := f.now()
		template := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), DNSNames: o.csr.DNSNames,
			NotBefore: now, NotAfter: now.Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, _ := x509.CreateCertificate(rand.Reader, template, f.ca, o.csr.PublicKey, f.caKey)
		f.current, _ = x509.ParseCertificate(der)
		step.Chain = string(pemCert(der))
		o.done = step
	}
	return step
}

func (f *fakeServices) answer(o *fakeOrder) orderAnswer {
	if o.done.State != "" {
		return o.done
	}
	return orderAnswer{ID: o.id, State: "working"}
}

// count is how many calls went to a path ending in suffix.
func (f *fakeServices) count(method, suffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.method == method && strings.HasSuffix(c.path, suffix) {
			n++
		}
	}
	return n
}
