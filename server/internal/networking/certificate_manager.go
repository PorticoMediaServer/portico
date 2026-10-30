package networking

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"io"
	"net/netip"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/directname"
	"strconv"
	"time"
)

func NewCertificateManager(ctx context.Context, store *SQLiteStore, runner *AuthorityRunner, transport *HTTPTransport, stateDir string, options CertificateOptions) (*CertificateManager, error) {
	if store == nil || runner == nil || transport == nil {
		return nil, ErrInvalid
	}
	if options.Environment == "" {
		options.Environment = "production"
	}
	if options.Environment != "production" && options.Environment != "staging" {
		return nil, ErrInvalid
	}
	if (options.Environment == "staging") != (options.StagingRootsFile != "") {
		return nil, errCertificateConfiguration
	}
	if options.DirectDomain == "" {
		options.DirectDomain = "portico.direct"
	}
	if options.ACMEDomain == "" {
		options.ACMEDomain = "acme.getportico.tv"
	}
	if !directname.ValidZone(options.DirectDomain) || !directname.ValidZone(options.ACMEDomain) {
		return nil, ErrInvalid
	}
	m := &CertificateManager{directDomain: options.DirectDomain, acmeDomain: options.ACMEDomain, store: store, runner: runner, transport: transport, environment: options.Environment, wake: make(chan struct{}, 1)}
	var e error
	if options.Environment == "production" {
		m.roots, e = x509.SystemCertPool()
		if e != nil {
			m.bootError = "public_trust_store_unavailable"
		}
	} else {
		f, e := os.Open(options.StagingRootsFile)
		if e != nil {
			return nil, errCertificateConfiguration
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0022 != 0 || st.Size() > 128<<10 {
			return nil, errCertificateConfiguration
		}
		roots, e := io.ReadAll(io.LimitReader(f, (128<<10)+1))
		if e != nil || len(roots) > 128<<10 {
			return nil, errCertificateConfiguration
		}
		m.roots = x509.NewCertPool()
		if !m.roots.AppendCertsFromPEM(roots) {
			return nil, errCertificateConfiguration
		}
	}
	e = runner.Do(ctx, func(ctx context.Context) error {
		var e error
		m.tlsKey, e = ensureTLSKey(ctx, store.db, stateDir)
		if e != nil {
			m.bootError = "local_tls_key_unavailable"
		} // recovery HTTP remains usable
		return nil
	})
	if e != nil {
		return nil, e
	}
	return m, nil
}
func (m *CertificateManager) Run(ctx context.Context) {
	m.Wake()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		// An order in flight, a renewal window opening or a route change all need
		// the half-minute cadence. A server with no claim at all — which is every
		// LAN-only installation, permanently — needs nothing until it is woken,
		// and paying a database read every thirty seconds for the rest of the
		// machine's life to discover that is the cost this removes.
		wait := certificateActiveInterval
		if m.quiescent.Load() {
			wait = certificateQuietInterval
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-m.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
		stepCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		_ = m.runner.Do(stepCtx, m.step)
		cancel()
	}
}

// certificateActiveInterval is the cadence while there is a certificate to
// order, advance or renew; certificateQuietInterval is the backstop while there
// is not.
const (
	certificateActiveInterval = 30 * time.Second
	certificateQuietInterval  = 6 * time.Hour
)

func (m *CertificateManager) step(ctx context.Context) error {
	c, q, _, e := m.snapshot(ctx)
	if e != nil {
		return e
	}
	m.quiescent.Store(q.Scope.ID == "" || (!c.Enabled && q.PendingID == ""))
	if q.Scope.ID == "" {
		return nil
	}
	// Pausing records a durable cancellation intent. Resuming cannot turn that
	// same pending request positive again, even after a late order/chain response.
	if q.PendingID != "" && (!c.Enabled || q.State == "cancel_pending") {
		if !time.Now().Before(q.NextAttempt) {
			return m.drainPending(ctx, q)
		}
		return nil
	}
	if !c.Enabled {
		return nil
	}
	if m.bootError != "" {
		return m.progress(ctx, q.Scope, "invalid", m.bootError, time.Now().Add(5*time.Minute))
	}
	// Route updates are independent of renewal: a WAN change reuses the wildcard.
	if q.Namespace != "" && c.PublicAddress != "" && (q.RouteAddress != c.PublicAddress || q.RoutePort != c.PublicPort) && !time.Now().Before(q.RouteNextAttempt) {
		m.updateRoute(ctx, q, c)
	}

	// Detect expired/corrupt active material even before a previously scheduled
	// renewal. Do not retire a key on a transient database/authority read failure.
	if q.ActiveID != "" && q.PendingID == "" {
		active, readErr := m.material(ctx, q.Scope, q.ActiveID)
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return readErr
		}
		invalid := readErr != nil
		if !invalid {
			_, checkErr := m.validateMaterial(q.Scope, q.Namespace, active, active.Chain, time.Now())
			invalid = checkErr != nil
		}
		if invalid {
			if q.State == "ready" || !time.Now().Before(q.NextAttempt) {
				return m.retireInvalidActive(ctx, q, active)
			}
			return nil
		}
	}
	if time.Now().Before(q.NextAttempt) {
		return nil
	}
	if q.PendingID != "" {
		return m.advancePending(ctx, q)
	}
	if q.ActiveID != "" {
		active, e := m.material(ctx, q.Scope, q.ActiveID)
		if e == nil && time.Now().Before(active.RenewAt) {
			if _, e = m.validateMaterial(q.Scope, q.Namespace, active, active.Chain, time.Now()); e == nil {
				return nil
			}
		}
	}
	// Refresh operational issuer selection only when issuance/renewal is due.
	// Healthy certificates do not induce repeated Hosted namespace requests.
	var n certificateNamespace
	e = m.transport.certificateCall(ctx, q.Scope.Intent, "namespace", "", nil, &n)
	if e != nil {
		return m.recordFailure(ctx, q, e)
	}
	v := q.Scope.Intent
	if !validCertificateNamespace(n.Namespace) || n.DNSName != "*."+n.Namespace+"."+m.directDomain || n.DNS01Target != n.Namespace+"."+m.acmeDomain || n.OperationID != v.OperationID || n.ClaimGeneration != v.ClaimGeneration || n.CredentialGeneration != v.CredentialGeneration {
		return m.recordFailure(ctx, q, ErrInvalid)
	}
	if !n.Configured {
		// Namespace allocation remains useful without an activated CA. This is not a
		// certificate or reachability success and creates no key/order.
		n.Environment = m.environment
		if e = m.publishNamespace(ctx, q.Scope, n); e != nil {
			return e
		}
		return m.progress(ctx, q.Scope, "configuration_required", "certificate_configuration_required", time.Now().Add(5*time.Minute))
	}
	if n.Environment != m.environment || n.Issuer == "" {
		return m.progress(ctx, q.Scope, "configuration_required", "issuer_environment_mismatch", time.Now().Add(5*time.Minute))
	}
	if e = m.publishNamespace(ctx, q.Scope, n); e != nil {
		return e
	}
	_, q, _, e = m.snapshot(ctx)
	if e != nil {
		return e
	}
	if e = m.prepareMaterial(ctx, q); e != nil {
		return m.recordFailure(ctx, q, e)
	}
	m.Wake()
	return nil
}
func (m *CertificateManager) advancePending(ctx context.Context, q certificateState) error {
	mat, e := m.material(ctx, q.Scope, q.PendingID)
	if e != nil {
		return m.recordFailure(ctx, q, e)
	}
	if _, e = m.privateKey(q.Scope, mat); e != nil {
		return m.recordFailure(ctx, q, e)
	}
	var order certificateOrder
	if mat.OrderID == "" {
		e = m.transport.certificateCall(ctx, q.Scope.Intent, "submit", "", struct {
			RequestID string `json:"requestId"`
			CSR       []byte `json:"csr"`
		}{mat.RequestID, mat.CSR}, &order)
	} else {
		e = m.transport.certificateCall(ctx, q.Scope.Intent, "status", mat.OrderID, nil, &order)
	}
	if e != nil {
		return m.recordFailure(ctx, q, e)
	}
	if !sameCertificateOrder(q, mat, order) {
		return m.recordFailure(ctx, q, ErrInvalid)
	}
	if e = m.receiveOrder(ctx, q, mat, order); e != nil {
		return e
	}
	mat.OrderID = order.ID
	mat.State = order.State
	switch order.State {
	case "issued":
		if order.NotAfter != nil && !time.Now().Before(*order.NotAfter) {
			// Its trusted Hosted receipt is terminal/expired. Preserve exact identity
			// while requesting deny-only revocation, then create a fresh renewal intent.
			return m.drainPending(ctx, q)
		}
		var response struct {
			Chain []byte `json:"chain"`
		}
		if e = m.transport.certificateCall(ctx, q.Scope.Intent, "chain", order.ID, nil, &response); e != nil {
			return m.recordFailure(ctx, q, e)
		}
		if e = m.install(ctx, q, mat, response.Chain); e != nil {
			return m.recordFailure(ctx, q, e)
		}
		m.Wake()
		if f := m.onReady.Load(); f != nil {
			(*f)()
		}
		return nil
	case "failed", "revoked", "cancelled":
		// Only a known terminal provider receipt permits a new local intent. Never
		// discard an uncertain order or a key needed to finish an accepted order.
		return m.clearPending(ctx, q, mat, time.Now().Add(time.Hour))
	case "queued", "creating", "ordering", "finalizing", "reconciliation_required":
		return nil
	default:
		return m.recordFailure(ctx, q, ErrInvalid)
	}
}
func (m *CertificateManager) drainPending(ctx context.Context, q certificateState) error {
	mat, e := m.material(ctx, q.Scope, q.PendingID)
	if e != nil {
		return e
	}
	if mat.OrderID == "" {
		var order certificateOrder
		e = m.transport.certificateCall(ctx, q.Scope.Intent, "lookup", "", struct {
			RequestID string `json:"requestId"`
		}{mat.RequestID}, &order)
		var fault *certificateHTTPError
		if errors.As(e, &fault) && fault.Status == 404 && fault.Code == "certificate_not_found" {
			return m.clearPending(ctx, q, mat, time.Now())
		}
		if e != nil {
			return m.recordFailure(ctx, q, e)
		}
		if !sameCertificateOrder(q, mat, order) {
			return m.recordFailure(ctx, q, ErrInvalid)
		}
		if e = m.receiveOrder(ctx, q, mat, order); e != nil {
			return e
		}
		mat.OrderID = order.ID
	}
	var receipt struct {
		CancelRequested bool `json:"cancelRequested"`
	}
	if e = m.transport.certificateCall(ctx, q.Scope.Intent, "cancel", mat.OrderID, nil, &receipt); e != nil {
		return m.recordFailure(ctx, q, e)
	}
	if !receipt.CancelRequested {
		return m.recordFailure(ctx, q, ErrInvalid)
	}
	return m.clearPending(ctx, q, mat, time.Now())
}
func (m *CertificateManager) recordFailure(ctx context.Context, q certificateState, cause error) error {
	now := time.Now()
	next := RetryAt(now, 15*time.Second, max(q.Attempts, 1), 7, time.Time{})
	code := "certificate_retry"
	state := "retrying"
	if errors.Is(cause, errCertificateMaterial) {
		code = "local_certificate_invalid"
		state = "invalid"
	}
	if errors.Is(cause, ErrInvalid) {
		code = "certificate_response_invalid"
		state = "invalid"
	}
	var fault *certificateHTTPError
	if errors.As(cause, &fault) {
		code = fault.Code
		if code == "" {
			code = "certificate_retry"
		}
		next = RetryAt(now, 15*time.Second, max(q.Attempts, 1), 7, fault.RetryAt)
		if fault.Status == 503 && fault.Code == "certificate_configuration_required" {
			state = "configuration_required"
		}
	}
	// A cancelled/expired interactive/root context cannot publish a failure over a
	// newer owner's successful work. The same current tuple is required here too.
	if e := m.progress(ctx, q.Scope, state, code, next); e != nil {
		return e
	}
	return cause
}
func (m *CertificateManager) updateRoute(ctx context.Context, q certificateState, c CertificateConfig) {
	_ = withRouteMutation(ctx, m.store.db, q.Scope.Intent.ServerID, func(ctx context.Context) error {
		current, scope, _, e := m.snapshot(ctx)
		if e != nil {
			return e
		}
		// This snapshot may have waited behind an acknowledged withdrawal, owner
		// change or new address. Never emit the stale address before checking it.
		if scope.Scope.ID != q.Scope.ID || scope.Namespace != q.Namespace || !current.Enabled || current.PublicAddress == "" || current.Revision != c.Revision || current.PublicAddress != c.PublicAddress || current.PublicPort != c.PublicPort {
			return ErrStale
		}
		m.updateRouteLocked(ctx, scope, current)
		return nil
	})
}

func (m *CertificateManager) updateRouteLocked(ctx context.Context, q certificateState, c CertificateConfig) {
	ip, err := netip.ParseAddr(c.PublicAddress)
	if err != nil {
		return
	}
	host := directname.IPHostname(ip, q.Namespace, m.directDomain)
	if host == "" {
		return
	}
	origin := "https://" + host
	if c.PublicPort != 443 {
		origin += ":" + strconv.Itoa(c.PublicPort)
	}
	_ = m.publishRoute(ctx, q, c, certificateRoute{Hostname: host, BaseURL: origin, Namespace: q.Namespace, CertificateDNSName: "*." + q.Namespace + "." + m.directDomain, DNS01Target: q.Namespace + "." + m.acmeDomain})
}
func (m *CertificateManager) Status(ctx context.Context) (CertificateStatus, error) {
	c, q, id, e := m.snapshot(ctx)
	if e != nil {
		return CertificateStatus{}, e
	}
	out := CertificateStatus{Configured: true, AuthorityID: id, Config: c, State: q.State, ErrorCode: q.ErrorCode, Namespace: q.Namespace, Issuer: q.Issuer, Environment: m.environment, ListenerBound: m.listening.Load(), ListenPort: int(m.listenPort.Load()), Reachability: "probe_required", RouteHostname: q.RouteHostname, RouteURL: q.RouteURL, RouteErrorCode: q.RouteErrorCode}
	if q.Namespace != "" {
		out.DNSName = "*." + q.Namespace + "." + m.directDomain
	}
	if !q.NextAttempt.IsZero() {
		out.NextAttemptAt = &q.NextAttempt
	}
	if q.PendingID != "" {
		mat, e := m.material(ctx, q.Scope, q.PendingID)
		if e != nil {
			return out, e
		}
		out.OrderState = mat.State
	}

	if q.Scope.ID != "" && m.bootError == "" {
		for _, id := range []string{q.ActiveID, q.PreviousID} {
			if id == "" {
				continue
			}
			mat, readErr := m.material(ctx, q.Scope, id)
			if readErr != nil {
				out.State = "invalid"
				continue
			}
			if !mat.NotAfter.IsZero() {
				out.NotAfter = &mat.NotAfter
				out.RenewAt = &mat.RenewAt
			}
			_, checkErr := m.validateMaterial(q.Scope, q.Namespace, mat, mat.Chain, time.Now())
			if checkErr == nil && c.Enabled {
				out.TLSReady = true
				out.PubliclyTrusted = m.environment == "production"
				out.State = "ready"
				if q.PendingID != "" {
					out.State = "renewing"
				} else if id != q.ActiveID {
					out.State = "recovering"
				}
				break
			}
			if !time.Now().Before(mat.NotAfter) {
				out.State = "expired"
			} else {
				out.State = "invalid"
			}
		}
	}

	if m.bootError != "" {
		out.State = "invalid"
		out.ErrorCode = m.bootError
	}
	if !c.Enabled {
		out.State = "paused"
		out.TLSReady = false
		out.PubliclyTrusted = false
	}
	if q.State == "claim_required" {
		out.State = "unconfigured"
		out.Configured = false
		out.Config.Enabled = false
		out.TLSReady = false
		out.PubliclyTrusted = false
	}
	return out, nil
}

// GetCertificate is wired into the actual HTTP/TLS listener, not merely a file
// export. It reads the last durable active generation on every new handshake.
func (m *CertificateManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if m == nil || hello == nil || m.bootError != "" {
		return nil, errCertificateMaterial
	}
	parent := hello.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	var selected *tls.Certificate
	e := m.runner.Do(ctx, func(ctx context.Context) error {
		q, e := m.servingSnapshot(ctx)
		if e != nil {
			return e
		}
		for _, id := range []string{q.ActiveID, q.PreviousID} {
			if id == "" {
				continue
			}
			cert, e := m.cachedMaterial(ctx, q, id)
			if e != nil {
				continue
			}
			if hello.ServerName == "" || cert.Leaf.VerifyHostname(hello.ServerName) != nil {
				return errCertificateMaterial
			}
			selected = cert
			return nil
		}
		return errCertificateMaterial
	})
	if e != nil {
		return nil, e
	}
	return selected, nil
}

// A local material failure must not strand the server until the old renewal date.
// Cancel the exact already-known order before replacing it; current authority and
// the Hosted daily budget still apply. No generic transport error permits this.
func (m *CertificateManager) retireInvalidActive(ctx context.Context, q certificateState, mat certificateMaterial) error {
	if mat.RequestID == "" {
		mat.RequestID = q.ActiveID
		mat.ScopeID = q.Scope.ID
	}
	if mat.OrderID == "" {
		var order certificateOrder
		e := m.transport.certificateCall(ctx, q.Scope.Intent, "lookup", "", struct {
			RequestID string `json:"requestId"`
		}{mat.RequestID}, &order)
		var fault *certificateHTTPError
		if e != nil && !(errors.As(e, &fault) && fault.Status == 404 && fault.Code == "certificate_not_found") {
			return m.recordFailure(ctx, q, e)
		}
		if e == nil {
			if !sameCertificateOrder(q, mat, order) {
				return m.recordFailure(ctx, q, ErrInvalid)
			}
			mat.OrderID = order.ID
		}
	}
	if mat.OrderID != "" {
		var receipt struct {
			CancelRequested bool `json:"cancelRequested"`
		}
		if e := m.transport.certificateCall(ctx, q.Scope.Intent, "cancel", mat.OrderID, nil, &receipt); e != nil {
			return m.recordFailure(ctx, q, e)
		}
		if !receipt.CancelRequested {
			return m.recordFailure(ctx, q, ErrInvalid)
		}
	}
	e := m.mutate(ctx, q.Scope, true, func(tx *sql.Tx, current certificateState) error {
		if current.ActiveID != q.ActiveID || current.PendingID != "" {
			return ErrStale
		}
		if _, e := tx.ExecContext(ctx, `UPDATE networking_certificate_state SET active_id='',state='waiting',error_code='local_certificate_invalid',next_attempt=0 WHERE scope_id=?`, q.Scope.ID); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `DELETE FROM networking_certificates WHERE request_id=? AND scope_id=? AND request_id<>?`, q.ActiveID, q.Scope.ID, current.PreviousID)
		return e
	})
	if e == nil {
		m.Wake()
	}
	return e
}
