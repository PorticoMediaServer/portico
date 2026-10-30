package claimweb

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const SetupStartKind = "portico.setup-start.v1"

// SetupStartLifetime is the longest one setup start (and its setup claim request) may
// live. Thirty minutes covers the most common first run: creating and email-verifying
// a Portico Account in the middle of setting up a server.
const SetupStartLifetime = 30 * time.Minute

var setupLifetime atomic.Int64

// SetupLifetime is the lifetime a new setup start is issued with: SetupStartLifetime,
// unless a development build shortened it for an end-to-end expiry check.
func SetupLifetime() time.Duration {
	if d := time.Duration(setupLifetime.Load()); d > 0 {
		return d
	}
	return SetupStartLifetime
}

// SetDevelopmentSetupLifetime shortens SetupLifetime. Only devtrust-tagged startup
// code calls it; it can never lengthen the window past SetupStartLifetime.
func SetDevelopmentSetupLifetime(d time.Duration) {
	if d >= time.Second && d <= SetupStartLifetime {
		setupLifetime.Store(int64(d))
	}
}

// RequestLifetime is the longest a signed web claim request may live: a setup claim
// spans account creation; every other purpose keeps ten minutes.
func RequestLifetime(purpose string) time.Duration {
	if purpose == "setup" {
		return SetupStartLifetime
	}
	return 10 * time.Minute
}

// SetupStartAddress is the original address a first-run setup may start from and
// return to: any exact HTTPS origin, or plain HTTP to this machine or the local
// network (PLEX-SETUP-NOTES S1/S3/S5) — localhost, a loopback, private (RFC 1918),
// CGNAT, link-local or IPv6 ULA literal, or a host name. A public IP literal over
// plain HTTP is refused. The server signs only hosts its own setup check admits.
func SetupStartAddress(value string) bool {
	u, e := url.Parse(value)
	if e != nil {
		return false
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return false
		}
	}
	if !strings.HasPrefix(value, "http://") {
		return ownerStartAddress(value)
	}
	// Exactly the canonical HTTPS spelling with the scheme swapped back.
	if !ownerStartAddress("https://" + strings.TrimPrefix(value, "http://")) {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		return LocalSetupAddr(ip)
	}
	// A host name, never a legacy numeric IPv4 spelling a resolver may read as an address
	// (0xc6336401, 0x7f.1, 3325256705): its last label must be a name, not a number.
	last := host[strings.LastIndex(host, ".")+1:]
	return !strings.Contains(host, ":") && last != "" && !strings.HasPrefix(last, "0x") &&
		strings.ContainsAny(last, "abcdefghijklmnopqrstuvwxyz") && !strings.HasPrefix(last, "-")
}

var localSetupPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"),
}

// LocalSetupAddr reports an address in the fixed pre-claim local set (S1): loopback,
// RFC 1918, CGNAT 100.64/10, link-local and IPv6 ULA. No zone, no IPv4-mapped form.
func LocalSetupAddr(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() {
		return false
	}
	for _, p := range localSetupPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

var ErrSetupStart = errors.New("invalid setup start")

// SetupStart is the distinct initial-setup wire. These seventeen fields are
// signed in declaration order. It is not an OwnerStart or an issued session.
type SetupStart struct {
	Kind            string    `json:"kind"`
	Audience        string    `json:"audience"`
	ClaimHash       string    `json:"claimHash"`
	OperationID     string    `json:"operationId"`
	ServerID        string    `json:"serverId"`
	PublicKey       string    `json:"publicKey"`
	LocalGeneration int64     `json:"localGeneration,string"`
	Address         string    `json:"address"`
	Purpose         string    `json:"purpose"`
	SetupRequestID  string    `json:"setupRequestId"`
	SetupRevision   int64     `json:"setupRevision,string"`
	State           string    `json:"state"`
	CodeChallenge   string    `json:"codeChallenge"`
	InstallationID  string    `json:"installationId"`
	ServerChallenge string    `json:"serverChallenge"`
	IssuedAt        time.Time `json:"issuedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

func (SetupStart) String() string   { return "[setup start redacted]" }
func (SetupStart) GoString() string { return "[setup start redacted]" }

// VerifySetupStart verifies the two complete signed envelopes. Runtime must
// separately enforce the captured setup-token/request/revision, live one-use
// challenge, installed owner and atomic setup completion. No token is here.
func VerifySetupStart(original, signed Signed, audience string, now time.Time) (Request, SetupStart, error) {
	invalid := func() (Request, SetupStart, error) { return Request{}, SetupStart{}, ErrSetupStart }
	var claim Request
	claimRaw, e := ownerStartBase64(original.Payload, -1)
	if e != nil || ownerStartJSON(claimRaw, &claim) != nil {
		return invalid()
	}
	key, e := ownerStartBase64(claim.PublicKey, ed25519.PublicKeySize)
	if e != nil {
		return invalid()
	}
	if _, e = ownerStartBase64(original.Signature, ed25519.SignatureSize); e != nil {
		return invalid()
	}
	if _, e = Verify(original, audience, now); e != nil || claim.Purpose != "setup" || claim.ReturnRegistrationID != "" {
		return invalid()
	}
	raw, e := ownerStartBase64(signed.Payload, -1)
	var start SetupStart
	if e != nil || ownerStartJSON(raw, &start) != nil {
		return invalid()
	}
	start.IssuedAt, start.ExpiresAt = start.IssuedAt.UTC(), start.ExpiresAt.UTC()
	canonical, e := json.Marshal(start)
	if e != nil || !bytes.Equal(canonical, raw) {
		return invalid()
	}
	signature, e := ownerStartBase64(signed.Signature, ed25519.SignatureSize)
	if e != nil || !ed25519.Verify(key, raw, signature) {
		return invalid()
	}
	hash, e := ownerStartBase64(start.ClaimHash, sha256.Size)
	digest := sha256.Sum256(claimRaw)
	if e != nil || subtle.ConstantTimeCompare(hash, digest[:]) != 1 {
		return invalid()
	}
	if _, e = ownerStartBase64(start.CodeChallenge, sha256.Size); e != nil {
		return invalid()
	}
	if start.Kind != SetupStartKind || start.Purpose != "setup" || start.Audience != audience || start.Audience != claim.Audience ||
		start.OperationID != claim.OperationID || start.ServerID != claim.ServerID || start.PublicKey != claim.PublicKey ||
		start.LocalGeneration != claim.LocalGeneration || start.Address != claim.Address || !SetupStartAddress(start.Address) ||
		!id.MatchString(start.SetupRequestID) || start.SetupRevision <= 0 || !ownerStartSecret.MatchString(start.State) ||
		!ownerStartSecret.MatchString(start.InstallationID) || !ownerStartSecret.MatchString(start.ServerChallenge) ||
		start.IssuedAt.IsZero() || start.IssuedAt.Before(claim.IssuedAt) || start.IssuedAt.After(now.Add(time.Minute)) ||
		!start.ExpiresAt.After(now) || !start.ExpiresAt.After(start.IssuedAt) ||
		start.ExpiresAt.Sub(start.IssuedAt) > SetupStartLifetime || start.ExpiresAt.After(claim.ExpiresAt) {
		return invalid()
	}
	return claim, start, nil
}
