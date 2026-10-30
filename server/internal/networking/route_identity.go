package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"strconv"
	"strings"
	"time"
)

// RouteIdentity is public information. Its v2 binding authorizes server-owned
// TLS leaf certificates, never a session or claim. A fresh client nonce proves
// possession of the already pinned identity key. Fingerprint is SHA256 of the
// identity public key; TLSCertificateFingerprints are SHA256 of leaf DER. A
// client must compare its own observed leaf with the signed v2 list, as well as
// verify the signature, origin, nonce and lifetime. See IDENTITY.md.
type RouteIdentity struct {
	Kind                       string    `json:"kind"`
	Version                    string    `json:"version"`
	ServerID                   string    `json:"serverId"`
	PublicKey                  string    `json:"publicKey"`
	Fingerprint                string    `json:"fingerprint"`
	BaseURL                    string    `json:"baseUrl"`
	Nonce                      string    `json:"nonce"`
	IssuedAt                   time.Time `json:"issuedAt"`
	ExpiresAt                  time.Time `json:"expiresAt"`
	TLSCertificateFingerprints []string  `json:"tlsCertificateFingerprints,omitempty"`
}
type RouteIdentityHandler struct {
	keys         *ProtectedKeys
	runner       *AuthorityRunner
	slots        chan struct{}
	failures     *failureLog
	certificates *CertificateManager
	custom       *CustomCertificate
}

func NewRouteIdentityHandler(keys *ProtectedKeys, runner *AuthorityRunner, certificates *CertificateManager, custom *CustomCertificate) *RouteIdentityHandler {
	return &RouteIdentityHandler{keys: keys, runner: runner, slots: make(chan struct{}, 32), failures: sharedFailureLog, certificates: certificates, custom: custom}
}

// RouteOrigin is shared by owner configuration and the public identity proof.
// HTTP and HTTPS are accepted from any address. Hosted publication remains TLS.
func RouteOrigin(raw string) bool {
	if len(raw) > 300 || strings.ContainsAny(raw, "\\\r\n\t") {
		return false
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || !validEndpointPort(u) {
		return false
	}
	host, ok := networkpolicy.CanonicalHost(u.Host)
	if !ok || host != u.Host || !strings.HasPrefix(raw, u.Scheme+"://") {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	return true
}
func (h *RouteIdentityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		writeClaimFailure(w, 429, "probe_busy", "Connection verification is busy.", true)
		return
	}
	var q struct {
		BaseURL string `json:"baseUrl"`
		Nonce   string `json:"nonce"`
	}
	if readClaimBody(w, r, &q) != nil || !RouteOrigin(q.BaseURL) {
		writeClaimFailure(w, 400, "invalid_probe", "Invalid connection verification request.", false)
		return
	}
	nonce, e := base64.RawURLEncoding.Strict().DecodeString(q.Nonce)
	u, _ := url.Parse(q.BaseURL)
	if e != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != q.Nonce || !networkpolicy.HostsMatch(u.Host, r.Host) {
		writeClaimFailure(w, 400, "invalid_probe", "Invalid connection verification request.", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	// Accept negotiation keeps existing apps working and leaves the request body
	// compatible with older servers. A stripped preference only removes auto-trust.
	version := "1"
	var fingerprints []string
	if media, params, err := mime.ParseMediaType(r.Header.Get("Accept")); err == nil && media == "application/json" && params["portico-route-proof"] == "2" {
		version = "2"
		fingerprints = h.certificates.routeCertificateFingerprints(ctx)
		if fingerprint := h.custom.routeCertificateFingerprint(ctx); fingerprint != "" {
			found := false
			for _, existing := range fingerprints {
				found = found || existing == fingerprint
			}
			if !found {
				fingerprints = append(fingerprints, fingerprint)
			}
		}
	}
	var out struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	e = h.runner.Do(ctx, func(ctx context.Context) error {
		gated, e := dbwork.BeginSnapshot(ctx, h.keys.db)
		if e != nil {
			return stepFailure("snapshot", e)
		}
		tx := gated.Tx()
		defer gated.Rollback()
		id, e := CurrentIdentityTx(ctx, tx)
		if e != nil {
			return stepFailure("identity row", e)
		}
		key, e := h.keys.read(id.KeyIncarnation)
		if e != nil {
			return stepFailure("key file", e)
		}
		defer clear(key)
		if !bytes.Equal(key.Public().(ed25519.PublicKey), id.PublicKey) {
			return stepFailure("key mismatch", ErrStale)
		}
		fp := sha256.Sum256(id.PublicKey)
		now := time.Now().UTC()
		raw, e := json.Marshal(RouteIdentity{
			Kind: "portico.route-proof", Version: version, ServerID: id.ServerID,
			PublicKey: base64.RawURLEncoding.EncodeToString(id.PublicKey), Fingerprint: base64.RawURLEncoding.EncodeToString(fp[:]),
			BaseURL: q.BaseURL, Nonce: q.Nonce, IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
			TLSCertificateFingerprints: fingerprints,
		})
		if e != nil {
			return e
		}
		signature := ed25519.Sign(key, raw)
		out.Payload = base64.RawURLEncoding.EncodeToString(raw)
		out.Signature = base64.RawURLEncoding.EncodeToString(signature)
		return stepFailure("commit", commitClaimTx(ctx, func() error {
			w.Header().Set("Content-Type", "application/json")
			return json.NewEncoder(w).Encode(out)
		}))
	})
	if e != nil {
		h.failures.report(routeLabel(r, "POST /v1/networking/identity-proof"), 503, r.Context(), e)
		writeClaimFailure(w, 503, "identity_unavailable", "Server identity verification is temporarily unavailable.", true)
		return
	}
}
func routeURL(ip netip.Addr, port int, scheme string) string {
	host := strings.ReplaceAll(ip.String(), "%", "%25")
	if ip.Is6() {
		host = "[" + host + "]"
	}
	if scheme == "https" && port == 443 || scheme == "http" && port == 80 {
		return scheme + "://" + host
	}
	return scheme + "://" + net.JoinHostPort(strings.ReplaceAll(ip.String(), "%", "%25"), strconv.Itoa(port))
}

// ChallengeProof is this server's signed answer to a Portico challenge
// request (POST /v1/direct/portico-challenge): the challenge Hosted is to sign
// into an identity assertion, the client's nonce and installation, under the
// server's pinned identity key. A client verifies it against the server id it
// meant to reach (the id is the key's own digest) before it asks Hosted for an
// assertion, so a server that does not hold that key cannot collect one for
// it (INT M7).
type ChallengeProof struct {
	Kind           string    `json:"kind"`
	Version        string    `json:"version"`
	ServerID       string    `json:"serverId"`
	PublicKey      string    `json:"publicKey"`
	Fingerprint    string    `json:"fingerprint"`
	Challenge      string    `json:"challenge"`
	Nonce          string    `json:"nonce"`
	InstallationID string    `json:"installationId"`
	IssuedAt       time.Time `json:"issuedAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// SignChallenge signs a ChallengeProof with the current identity key. The
// nonce is the client's: 32 random bytes, base64url.
func (h *RouteIdentityHandler) SignChallenge(ctx context.Context, challenge, nonce, installation string, expires time.Time) (payload, signature string, err error) {
	raw, e := base64.RawURLEncoding.Strict().DecodeString(nonce)
	if e != nil || len(raw) != 32 || challenge == "" || len(installation) > 128 {
		return "", "", ErrInvalid
	}
	err = h.runner.Do(ctx, func(ctx context.Context) error {
		gated, e := dbwork.BeginSnapshot(ctx, h.keys.db)
		if e != nil {
			return e
		}
		tx := gated.Tx()
		defer gated.Rollback()
		id, e := CurrentIdentityTx(ctx, tx)
		if e != nil {
			return e
		}
		key, e := h.keys.read(id.KeyIncarnation)
		if e != nil {
			return e
		}
		defer clear(key)
		if !bytes.Equal(key.Public().(ed25519.PublicKey), id.PublicKey) {
			return ErrStale
		}
		fp := sha256.Sum256(id.PublicKey)
		body, e := json.Marshal(ChallengeProof{"portico.challenge-proof", "1", id.ServerID, base64.RawURLEncoding.EncodeToString(id.PublicKey), base64.RawURLEncoding.EncodeToString(fp[:]), challenge, nonce, installation, time.Now().UTC(), expires.UTC()})
		if e != nil {
			return e
		}
		payload = base64.RawURLEncoding.EncodeToString(body)
		signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, body))
		return nil
	})
	return payload, signature, err
}
