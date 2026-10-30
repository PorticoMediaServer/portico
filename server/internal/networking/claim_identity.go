package networking

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// ServerIdentity derives the sole current server ID from its Ed25519 public key.
// It is a namespace binding, not proof of private-key possession. Claim and
// reset still require their signed operation and durable publication fences.
func ServerIdentity(public ed25519.PublicKey) (string, error) {
	if len(public) != ed25519.PublicKeySize {
		return "", ErrInvalid
	}
	h := sha256.New()
	_, _ = h.Write([]byte("portico.server.identity.v1\x00"))
	_, _ = h.Write(public)
	return "srv_" + base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}

// IdentityCode renders a canonical server ID as a twelve-symbol identity code.
// It conveys an expected identity; it is never proof of private-key possession,
// setup authority, or a replacement for checking the complete server pin.
// Invalid or noncanonical server IDs have no code.
func IdentityCode(serverID string) string {
	if len(serverID) != 47 || !strings.HasPrefix(serverID, "srv_") {
		return ""
	}
	encoded := serverID[4:]
	for _, c := range encoded {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if e != nil || len(raw) != sha256.Size {
		return ""
	}
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	const space uint64 = 787662783788549761 //31^12
	n := binary.BigEndian.Uint64(raw[:8]) % space
	var digits [12]byte
	for i := len(digits) - 1; i >= 0; i-- {
		digits[i] = alphabet[n%31]
		n /= 31
	}
	return string(digits[:4]) + "-" + string(digits[4:8]) + "-" + string(digits[8:])
}
