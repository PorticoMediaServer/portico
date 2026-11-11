// Package certclient gets a server linked to a Portico Account its Portico address and keeps
// its certificate: it asks Portico Services for the server's name space once, orders a
// certificate for *.<name space>.portico.direct with a key it makes and keeps in the Keys
// folder, waits for it at the pace the contract sets, installs it without a restart and
// renews it when it is due. Nothing here is a step of setup: it all happens in the
// background, and the owner is told in plain words where things stand.
//
// The contract is Portico Services' API.md (portico-internal, hosted-services/hosted-next).
// Every request is signed with the server's identity key (internal/hosted/trust).
package certclient

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/schedule"
)

// Services is where Portico Services answers.
const Services = "https://web.getportico.tv"

// Zone is the domain every Portico address is under.
const Zone = "portico.direct"

// poll is the pace of asking how an order is doing: 5 seconds, doubling to 10 minutes, each
// wait drawn between half and the whole of its step (API.md, GET …/orders/{orderId}).
var poll = schedule.Retry{First: 5 * time.Second, Ceiling: 10 * time.Minute}

type Options struct {
	// Services is the base address; empty is the real one.
	Services string
	HTTP     *http.Client
	ServerID string
	// Key is the server's identity key, which signs every request.
	Key ed25519.PrivateKey
	// Keys is the Keys folder of the server's data folder.
	Keys string
	// Roots are what an issued chain must verify against; nil means the system's.
	Roots *x509.CertPool
	Now   func() time.Time
	Log   *slog.Logger
	// OnChange is told every new status (for Remote access, and Enhanced security).
	OnChange func(Status)
}

// Status is where the Portico address and certificate stand, for the owner.
type Status struct {
	// State is "off" (not linked), "asking", "queued", "working", "waiting" (Portico Services
	// or the authority said to wait, or could not be reached), "issued", or "unlinked"
	// (Portico Services no longer knows this server).
	State string `json:"state"`
	// Message says it in plain words; empty when there is nothing to say.
	Message string `json:"message,omitempty"`
	// NameSpace makes the Portico address: <IP with dashes>.<name space>.portico.direct.
	NameSpace string `json:"nameSpace,omitempty"`
	// CertificateReady says a usable certificate is installed, so Enhanced security can work.
	CertificateReady bool       `json:"certificateReady"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	NextTryAt        *time.Time `json:"nextTryAt,omitempty"`
}

// Client is the certificate side of a linked server.
type Client struct {
	o      Options
	linked chan struct{}
	link   sync.Once
	// wake tells Run the server was linked again after Portico Services stopped knowing it.
	wake chan struct{}
	// stopped: Portico Services no longer knows this server; nothing is asked until it is
	// linked again.
	stopped bool
	mu      sync.Mutex
	status  Status
	cert    *tls.Certificate
	leaf    *x509.Certificate
	state   saved
	orderID string
	// early says the last order was refused as too early while this server has no usable
	// certificate, so the next one says so.
	early bool
	// polls counts the asks about the current order; failures the calls that failed in a row.
	polls, failures int
}

func New(o Options) *Client {
	if o.Services == "" {
		o.Services = Services
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	c := &Client{o: o, linked: make(chan struct{}), wake: make(chan struct{}, 1), status: Status{State: "off"}}
	c.load()
	return c
}

// Linked says the server is linked to a Portico Account, so it may ask Portico Services for its
// address and certificate. A server with local sign-in never calls it, and nothing here ever
// contacts Portico Services before it.
func (c *Client) Linked() {
	c.link.Do(func() { close(c.linked) })
	c.mu.Lock()
	c.stopped = false
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run works in the background until ctx ends.
func (c *Client) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-c.linked:
	}
	for ctx.Err() == nil {
		next := c.Step(ctx)
		if next.IsZero() {
			// Nothing to do until the server is linked again.
			select {
			case <-ctx.Done():
			case <-c.wake:
			}
			continue
		}
		timer := time.NewTimer(max(next.Sub(c.o.Now()), time.Second))
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
}

// Status is where things stand now.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// GetCertificate is for the TLS listener: the Portico certificate for a Portico address of
// this server, and nothing for any other name.
func (c *Client) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert == nil || c.state.NameSpace == "" || !strings.HasSuffix(strings.ToLower(hello.ServerName), "."+c.state.NameSpace+"."+Zone) ||
		c.o.Now().After(c.leaf.NotAfter) {
		return nil, errNotPortico
	}
	return c.cert, nil
}

// renewAt is when the installed certificate is renewed: a point between 58% and 76% of its
// life fixed by the certificate itself, so it needs no saving, a fleet issued together spreads
// out, and it is always past the 55% before which Portico Services refuses a renewal.
func renewAt(leaf *x509.Certificate) time.Time {
	sum := sha256.Sum256(leaf.Raw)
	fraction := 0.58 + 0.18*float64(binary.BigEndian.Uint64(sum[:8]))/float64(^uint64(0))
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return leaf.NotBefore.Add(time.Duration(float64(life) * fraction))
}

// usable says a certificate is installed for this name space and still valid.
func (c *Client) usable(now time.Time) bool {
	return c.cert != nil && now.Before(c.leaf.NotAfter) && !now.Before(c.leaf.NotBefore)
}

func (c *Client) set(s Status) {
	c.mu.Lock()
	s.NameSpace, s.CertificateReady = c.state.NameSpace, c.usable(c.o.Now())
	if s.CertificateReady {
		expires := c.leaf.NotAfter.UTC()
		s.ExpiresAt = &expires
	}
	changed := s.State != c.status.State || s.Message != c.status.Message || s.CertificateReady != c.status.CertificateReady ||
		s.NameSpace != c.status.NameSpace
	c.status = s
	c.mu.Unlock()
	if changed && c.o.OnChange != nil {
		c.o.OnChange(s)
	}
}

func jitter() float64 { return rand.Float64() }
