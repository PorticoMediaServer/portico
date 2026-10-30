package networking

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// CertificateManager consumes the existing claim authority, never substitutes a
// second identity system. TLS, claim, Hosted ACME and DNS-MAC keys are distinct.
type CertificateManager struct {
	directDomain, acmeDomain string
	servingMu                sync.Mutex
	serving                  *servingCertificate
	store                    *SQLiteStore
	runner                   *AuthorityRunner
	transport                *HTTPTransport
	tlsKey                   *ecdsa.PrivateKey
	roots                    *x509.CertPool
	environment              string
	bootError                string
	wake                     chan struct{}
	listening                atomic.Bool
	listenPort               atomic.Int64
	// quiescent means there is no scope and no pending order, so nothing this
	// loop does can make progress until something wakes it. See CertificateManager.Run.
	quiescent atomic.Bool
	// onReady is called once after each certificate is installed and active.
	onReady atomic.Pointer[func()]
}

// SetOnReady installs the callback run after a newly issued certificate has
// been committed. It is never called on failure or for an expired receipt.
func (m *CertificateManager) SetOnReady(f func()) {
	if m == nil {
		return
	}
	if f == nil {
		m.onReady.Store(nil)
		return
	}
	m.onReady.Store(&f)
}

type CertificateOptions struct {
	DirectDomain, ACMEDomain string
	Environment              string
	StagingRootsFile         string
}
type CertificateConfig struct {
	Enabled       bool   `json:"enabled"`
	PublicPort    int    `json:"publicPort"`
	PublicAddress string `json:"publicAddress"`
	Revision      int64  `json:"revision,string"`
}
type CertificateStatus struct {
	Configured      bool              `json:"configured"`
	AuthorityID     string            `json:"authorityId"`
	Config          CertificateConfig `json:"config"`
	State           string            `json:"state"`
	ErrorCode       string            `json:"errorCode,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	DNSName         string            `json:"dnsName,omitempty"`
	Issuer          string            `json:"issuer,omitempty"`
	Environment     string            `json:"environment"`
	OrderState      string            `json:"orderState,omitempty"`
	NotAfter        *time.Time        `json:"notAfter,omitempty"`
	RenewAt         *time.Time        `json:"renewAt,omitempty"`
	NextAttemptAt   *time.Time        `json:"nextAttemptAt,omitempty"`
	TLSReady        bool              `json:"tlsReady"`
	PubliclyTrusted bool              `json:"publiclyTrusted"`
	ListenerBound   bool              `json:"listenerBound"`
	ListenPort      int               `json:"listenPort"`
	RouteHostname   string            `json:"routeHostname,omitempty"`
	RouteURL        string            `json:"routeUrl,omitempty"`
	RouteErrorCode  string            `json:"routeErrorCode,omitempty"`
	Reachability    string            `json:"reachability"`
}

type certificateScope struct {
	ID, Incarnation string
	Intent          Intent
}
type certificateState struct {
	Scope                                                                             certificateScope
	Namespace, Issuer, Environment, PendingID, ActiveID, PreviousID, State, ErrorCode string
	RouteAddress, RouteHostname, RouteURL                                             string
	RoutePort                                                                         int
	RouteNextAttempt                                                                  time.Time
	RouteErrorCode                                                                    string
	NextAttempt                                                                       time.Time
	Attempts                                                                          int
}
type certificateMaterial struct {
	RequestID, ScopeID, OrderID, State string
	Nonce, Cipher, CSR, Chain          []byte
	NotBefore, NotAfter, RenewAt       time.Time
}
type certificateNamespace struct {
	Namespace            string `json:"namespace"`
	DNSName              string `json:"dnsName"`
	DNS01Target          string `json:"dns01Target"`
	OperationID          string `json:"operationId"`
	ClaimGeneration      string `json:"claimGeneration"`
	CredentialGeneration string `json:"credentialGeneration"`
	Issuer               string `json:"issuer"`
	Environment          string `json:"environment"`
	Configured           bool   `json:"configured"`
}
type certificateOrder struct {
	ID                   string     `json:"id"`
	RequestID            string     `json:"requestId"`
	Namespace            string     `json:"namespace"`
	State                string     `json:"state"`
	Issuer               string     `json:"issuer"`
	Environment          string     `json:"environment"`
	OperationID          string     `json:"operationId"`
	ClaimGeneration      string     `json:"claimGeneration"`
	CredentialGeneration string     `json:"credentialGeneration"`
	NotAfter             *time.Time `json:"notAfter,omitempty"`
	NextAttemptAt        time.Time  `json:"nextAttemptAt"`
	ErrorCode            string     `json:"errorCode,omitempty"`
}
type certificateRoute struct {
	Hostname           string `json:"hostname"`
	BaseURL            string `json:"baseUrl"`
	Namespace          string `json:"namespace"`
	CertificateDNSName string `json:"certificateDnsName"`
	DNS01Target        string `json:"dns01Target"`
}

var errCertificateConfiguration = errors.New("certificate configuration required")
var errCertificateMaterial = errors.New("certificate material is invalid or unavailable")

func (m *CertificateManager) Wake() {
	if m != nil {
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
}
func (m *CertificateManager) SetListening(port int, bound bool) {
	m.listenPort.Store(int64(port))
	m.listening.Store(bound)
}
func (h *ClaimHandler) Certificates() *CertificateManager {
	if h == nil {
		return nil
	}
	return h.certificates
}
func (h *ClaimHandler) ConfigureCertificates(ctx context.Context, stateDir string, options CertificateOptions) error {
	t, ok := h.transport.(*HTTPTransport)
	if !ok {
		return ErrInvalid
	}
	m, e := NewCertificateManager(ctx, h.store, h.runner, t, stateDir, options)
	if e != nil {
		return e
	}
	// A ready certificate lets the managed HTTPS route be proved at once; the
	// remote manager is read at call time because it is configured afterwards.
	m.SetOnReady(func() { h.Remote().Reprove() })
	h.certificates = m
	return nil
}
