package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"time"
)

const claimHTTPBound = 16 << 10

// CredentialReader is private server control-plane access, never a browser API.
type CredentialReader interface {
	InstalledCredential(context.Context, Intent) (*Secret, error)
	Current(context.Context, Intent) error
}

// HTTPTransport is bound to one explicit Hosted HTTPS origin. It neither adopts
// historical enrollment credentials nor discovers a new authority from a reply.
type HTTPTransport struct {
	origin      string
	client      *http.Client
	credentials CredentialReader
}

// devHostedRoots is nil (the system roots) except in a development build
// given PORTICO_DEV_HOSTED_CA_FILE (dev_hosted_ca.go).
var devHostedRoots *x509.CertPool

func NewHTTPTransport(origin string, credentials CredentialReader) (*HTTPTransport, error) {
	if !validAudience(origin) || credentials == nil {
		return nil, ErrInvalid
	}
	return &HTTPTransport{
		origin: origin, credentials: credentials,
		client: newClaimHTTPClient(""),
	}, nil
}

func newClaimHTTPClient(network string) *http.Client {
	dial := (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if network != "" {
		// A separate transport owns each route family so an idle connection from
		// ordinary control traffic or the other family can never be reused.
		baseDial := dial
		dial = func(ctx context.Context, _ string, address string) (net.Conn, error) {
			return baseDial(ctx, network, address)
		}
	}
	rt := &http.Transport{
		Proxy:                 nil,
		DialContext:           dial,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: devHostedRoots},
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          2, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2,
		MaxResponseHeaderBytes: claimHTTPBound,
		DisableCompression:     true,
	}
	return &http.Client{
		Transport: rt, Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
func (t *HTTPTransport) CloseIdleConnections() {
	for _, client := range []*http.Client{t.client} {
		if client != nil {
			client.CloseIdleConnections()
		}
	}
}

type proofEnvelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type commitEnvelope struct {
	OperationID          string `json:"operationId"`
	ServerID             string `json:"serverId"`
	ClaimGeneration      string `json:"claimGeneration"`
	CredentialGeneration string `json:"credentialGeneration"`
}

func (w commitEnvelope) commit() Commit {
	return Commit{w.OperationID, w.ServerID, w.ClaimGeneration, w.CredentialGeneration}
}
func commitRequest(v Intent) commitEnvelope {
	return commitEnvelope{v.OperationID, v.ServerID, v.ClaimGeneration, v.CredentialGeneration}
}

func decodeClaimJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (t *HTTPTransport) call(ctx context.Context, path string, in, out any, secret *Secret) error {
	return t.controlRequest(ctx, http.MethodPost, "/v1/server-claims/"+path, in, out, secret, claimHTTPBound)
}

func (t *HTTPTransport) controlRequest(ctx context.Context, method, path string, in, out any, secret *Secret, responseBound int64) error {
	return t.boundedControlRequest(ctx, method, path, in, out, secret, claimHTTPBound, responseBound)
}

// boundedControlRequest is controlRequest with its own request body bound
// (the membership push is up to Hosted's 1 MiB; everything else 16 KiB).
func (t *HTTPTransport) boundedControlRequest(ctx context.Context, method, path string, in, out any, secret *Secret, requestBound int, responseBound int64) error {
	if _, e := claimAuthority(ctx); e != nil {
		return e
	}
	if store, ok := t.credentials.(*SQLiteStore); ok {
		if err := store.checkServiceRequest(ctx, path, in); err != nil {
			return err
		}
	}
	var body []byte
	var e error
	if in != nil {
		body, e = json.Marshal(in)
	}
	if e != nil || len(body) > requestBound {
		return ErrInvalid
	}
	defer clear(body)
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, method, t.origin+path, bytes.NewReader(body))
	if e != nil {
		return ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if secret != nil {
		if e = secret.Use(func(b []byte) error { req.Header.Set("Authorization", "Bearer "+string(b)); return nil }); e != nil {
			return e
		}
		defer req.Header.Del("Authorization")
	}
	if _, e = claimAuthority(ctx); e != nil {
		return e
	}
	if e = checkClaimRequest(ctx); e != nil {
		return e
	}
	resp, e := t.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.ContentLength > responseBound {
		return ErrInvalid
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, responseBound+1))
	defer clear(raw)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	if int64(len(raw)) > responseBound {
		return ErrInvalid
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if resp.StatusCode == http.StatusNoContent && out == nil && len(raw) == 0 {
		return checkClaimRequest(ctx)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
		return &controlHTTPError{retryAt: certificateRetryAt(resp.Header.Get("Retry-After"))}
	}
	media, _, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return ErrUnavailable
	}
	if resp.StatusCode == http.StatusConflict {
		var fault struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		if decodeClaimJSON(raw, &fault) != nil {
			return ErrUnavailable
		}
		switch fault.Error.Code {
		case "approval_required":
			return ErrApprovalRequired
		case "claim_cancelled":
			return ErrCancelled
		case "claim_stale":
			return ErrStale
		}
	}
	// An HTTP status is not a signed terminal authority observation. In particular
	// generic proxy/auth failures must never revoke a durable local credential.
	if resp.StatusCode != http.StatusOK {
		auth := resp.StatusCode == 401 || resp.StatusCode == 403
		return &controlHTTPError{retryAt: certificateRetryAt(resp.Header.Get("Retry-After")), checkClaim: auth, rejected: permanentControlStatus(resp.StatusCode)}
	}
	if e = checkClaimRequest(ctx); e != nil {
		return e
	}
	return decodeClaimJSON(raw, out)
}
func proofRequest(v Intent, p SignedProof) (proofEnvelope, error) {
	if !validBinding(v.Binding) || len(p.Payload) < 1 || len(p.Payload) > claimHTTPBound || len(p.Signature) != ed25519.SignatureSize || !ed25519.Verify(v.PublicKey, p.Payload, p.Signature) {
		return proofEnvelope{}, ErrInvalid
	}
	return proofEnvelope{base64.RawURLEncoding.EncodeToString(p.Payload), base64.RawURLEncoding.EncodeToString(p.Signature)}, nil
}
func (t *HTTPTransport) Challenge(ctx context.Context, v Intent, purpose Purpose, p SignedProof) (Challenge, error) {
	if purpose != FinalizePurpose && purpose != ResultPurpose {
		return Challenge{}, ErrInvalid
	}
	in, e := proofRequest(v, p)
	if e != nil {
		return Challenge{}, e
	}
	var out struct {
		RequestDigest string    `json:"requestDigest"`
		NonceID       string    `json:"nonceId"`
		Nonce         string    `json:"nonce"`
		IssuedAt      time.Time `json:"issuedAt"`
		ExpiresAt     time.Time `json:"expiresAt"`
	}
	if e = t.call(ctx, "nonce", in, &out, nil); e != nil {
		return Challenge{}, e
	}
	nonce, e := base64.RawURLEncoding.Strict().DecodeString(out.Nonce)
	if e != nil || len(nonce) != 32 {
		return Challenge{}, ErrInvalid
	}
	return Challenge{out.RequestDigest, out.NonceID, nonce, out.IssuedAt, out.ExpiresAt}, nil
}
func (t *HTTPTransport) Finalize(ctx context.Context, v Intent, p SignedProof) (Commit, error) {
	in, e := proofRequest(v, p)
	if e != nil {
		return Commit{}, e
	}
	var out commitEnvelope
	if e = t.call(ctx, "finalize", in, &out, nil); e != nil {
		return Commit{}, e
	}
	result := out.commit()
	if e = validateCommit(v, result); e != nil {
		return Commit{}, e
	}
	return result, nil
}
func (t *HTTPTransport) Retrieve(ctx context.Context, v Intent, p SignedProof) (Result, error) {
	in, e := proofRequest(v, p)
	if e != nil {
		return Result{}, e
	}
	var out struct {
		commitEnvelope
		ServerCredential string `json:"serverCredential"`
	}
	if e = t.call(ctx, "result", in, &out, nil); e != nil {
		return Result{}, e
	}
	defer func() { out.ServerCredential = "" }()
	result := out.commit()
	if e = validateCommit(v, result); e != nil {
		return Result{}, e
	}
	raw := []byte(out.ServerCredential)
	defer clear(raw)
	secret, e := NewSecret(raw)
	if e != nil {
		return Result{}, e
	}
	return Result{result, secret}, nil
}
func (t *HTTPTransport) Acknowledge(ctx context.Context, v Intent) error {
	if validateIntent(v, v.OperationID) != nil || v.Stage != Installed {
		return ErrInvalid
	}
	secret, e := t.credentials.InstalledCredential(ctx, v)
	if e != nil {
		return e
	}
	defer secret.Clear()
	var out commitEnvelope
	if e = t.call(ctx, "ack", commitRequest(v), &out, secret); e != nil {
		return e
	}
	return validateCommit(v, out.commit())
}
func (t *HTTPTransport) Cancel(ctx context.Context, item Cancellation) error {
	if e := validateCancellation(t.origin, item); e != nil {
		return e
	}
	in, e := proofRequest(Intent{Binding: item.Binding}, item.Proof)
	if e != nil {
		return e
	}
	var out struct {
		OperationID string `json:"operationId"`
		RequestID   string `json:"requestId"`
	}
	if e = t.call(ctx, "cancel", in, &out, nil); e != nil {
		return e
	}
	if out.OperationID != item.OperationID || out.RequestID != item.RequestID {
		return ErrStale
	}
	return nil
}

// permanentControlStatus: a 4xx Hosted will give again for the same request.
// Authentication (401/403) asks for claim reconciliation; timeouts (408) and
// rate limiting (429) are retried with backoff (A71).
func permanentControlStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return status >= 400 && status < 500
}
