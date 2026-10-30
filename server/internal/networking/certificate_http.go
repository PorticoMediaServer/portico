package networking

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

type certificateHTTPError struct {
	Status  int
	Code    string
	RetryAt time.Time
}

func (*certificateHTTPError) Error() string {
	return "Hosted certificate request requires retry or configuration"
}
func safeCertificateCode(code string) string {
	switch code {
	case "", "certificate_not_found", "certificate_not_due", "certificate_conflict", "certificate_configuration_required", "invalid_certificate_request",
		"acceptance_uncertain", "authority_changed", "creation_started", "provider_rejected", "provider_response_invalid", "provider_order_invalid", "dns_validation_failed",
		"coordinator_retry", "cleanup_retry", "revocation_retry", "provider_retry", "acme_account_configuration_or_retry", "dns_or_issuer_configuration":
		return code
	default:
		return "certificate_retry"
	}
}
func certificateRetryAt(value string) time.Time {
	if seconds, e := time.ParseDuration(value + "s"); e == nil && seconds > 0 {
		return time.Now().Add(seconds)
	}
	when, e := http.ParseTime(value)
	if e == nil {
		return when
	}
	return time.Time{}
}
func validCertificateOrderID(id string) bool {
	if !strings.HasPrefix(id, "cert_") {
		return false
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(id, "cert_"))
	return e == nil && len(b) == 32
}

// Dedicated typed operation set. No URL/path/bearer is supplied by a UI, DNS
// response or provider. Uses the established protected installed credential.
func (t *HTTPTransport) certificateCall(ctx context.Context, v Intent, operation, id string, in, out any) error {
	if v.Stage != Installed || !v.InstallationAcknowledged {
		return ErrStale
	}
	method := http.MethodPost
	path := "/v1/servers/" + v.ServerID
	switch operation {
	case "namespace":
		path += "/certificates/namespace"
	case "submit":
		path += "/certificates/orders"
	case "lookup":
		path += "/certificates/orders/lookup"
	case "status", "chain", "cancel":
		if !validCertificateOrderID(id) {
			return ErrInvalid
		}
		path += "/certificates/orders/" + id
		if operation == "cancel" {
			path += "/cancel"
		} else {
			method = http.MethodGet
			if operation == "chain" {
				path += "/chain"
			}
		}
	default:
		return ErrInvalid
	}
	secret, e := t.credentials.InstalledCredential(ctx, v)
	if e != nil {
		return e
	}
	defer secret.Clear()
	if _, e = claimAuthority(ctx); e != nil {
		return e
	}
	raw, e := json.Marshal(in)
	if e != nil || len(raw) > 16<<10 {
		return ErrInvalid
	}
	defer clear(raw)
	client := t.client
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(callCtx, method, t.origin+path, bytes.NewReader(raw))
	if e != nil {
		return ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if e = secret.Use(func(b []byte) error { req.Header.Set("Authorization", "Bearer "+string(b)); return nil }); e != nil {
		return e
	}
	defer req.Header.Del("Authorization")
	if e = t.credentials.Current(ctx, v); e != nil {
		return e
	}
	if e = checkClaimRequest(ctx); e != nil {
		return e
	}
	response, e := client.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	defer clear(body)
	if e != nil || len(body) > 128<<10 {
		return ErrUnavailable
	}
	if _, e = claimAuthority(ctx); e != nil {
		return e
	}
	if e = t.credentials.Current(ctx, v); e != nil {
		return e
	}
	if e = checkClaimRequest(ctx); e != nil {
		return e
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return ErrUnavailable
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		var fault struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		// Only a whitelisted code is retained. A generic HTTP failure never changes
		// claim authority; that requires the existing authenticated observation path.
		_ = json.Unmarshal(body, &fault)
		return &certificateHTTPError{Status: response.StatusCode, Code: safeCertificateCode(fault.Error.Code), RetryAt: certificateRetryAt(response.Header.Get("Retry-After"))}
	}
	return decodeClaimJSON(body, out)
}
func sameCertificateOrder(q certificateState, mat certificateMaterial, o certificateOrder) bool {
	v := q.Scope.Intent
	return validCertificateOrderID(o.ID) && o.RequestID == mat.RequestID && o.Namespace == q.Namespace && o.OperationID == v.OperationID && o.ClaimGeneration == v.ClaimGeneration && o.CredentialGeneration == v.CredentialGeneration && o.Environment == q.Environment && o.Issuer == q.Issuer
}
func (h *ClaimHandler) registerCertificates(mux HTTPRegistrar) {
	if h.certificates == nil {
		return
	}
	for _, pattern := range []string{"GET /v1/networking/certificate", "POST /v1/networking/certificate/config", "POST /v1/networking/certificate/retry"} {
		mux.HandleFunc(pattern, h.certificateHTTP)
	}
}
func (h *ClaimHandler) certificateHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	guard, e := h.authorizeManagement(r)
	if e != nil || guard == nil {
		writeClaimFailure(w, http.StatusUnauthorized, "owner_required", "Sign in using this server’s owner account to manage automatic HTTPS.", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var status CertificateStatus
	e = h.runner.Do(ctx, func(ctx context.Context) error {
		ctx, e = guardedClaimRequest(ctx, h.store, guard)
		if e != nil {
			return e
		}
		if e = checkClaimRequest(ctx); e != nil {
			return e
		}
		switch r.URL.Path {
		case "/v1/networking/certificate/config":
			var in struct {
				AuthorityID string            `json:"authorityId"`
				Config      CertificateConfig `json:"config"`
			}
			if e = readClaimBody(w, r, &in); e != nil {
				return e
			}
			if e = h.certificates.configure(ctx, in.AuthorityID, in.Config); e != nil {
				return e
			}
		case "/v1/networking/certificate/retry":
			// Wake is coalesced and never bypasses durable Retry-After or manufactures a
			// new idempotency key for an unresolved order.
		case "/v1/networking/certificate":
		default:
			return ErrInvalid
		}
		status, e = h.certificates.Status(ctx)
		if e != nil {
			return e
		}
		return checkClaimRequest(ctx)
	})
	if e != nil {
		h.failure(w, r, e)
		return
	}
	if r.Method == http.MethodPost {
		h.certificates.Wake()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}
