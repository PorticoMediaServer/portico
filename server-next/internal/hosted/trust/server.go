package trust

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

// ServerID is a server's identity: the digest of its public key. Nobody can claim an id
// without the key, and an app that knows the id can check any address that claims it. The
// label is part of ids already in use.
func ServerID(public ed25519.PublicKey) (string, error) {
	if len(public) != ed25519.PublicKeySize {
		return "", ErrTrust
	}
	sum := sha256.Sum256(append([]byte("portico.server.identity.v1\x00"), public...))
	return "srv_" + base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// ValidServerID says whether a string has the shape of a server id.
func ValidServerID(id string) bool {
	if len(id) != 47 || id[:4] != "srv_" {
		return false
	}
	_, err := encoding.DecodeString(id[4:])
	return err == nil
}

// A server proves each request to Portico Services with its identity key, so there is no
// server password to keep or steal. The two headers carry the time of signing and the
// signature over the method, the path with its query, that time and the body's digest.
const (
	RequestTimeHeader      = "Portico-Server-Time"
	RequestSignatureHeader = "Portico-Server-Signature"
	// RequestWindow is how far the signing time may be from the reader's clock.
	RequestWindow = 5 * time.Minute
)

func requestBytes(method, pathAndQuery string, at time.Time, body []byte) []byte {
	digest := sha256.Sum256(body)
	return []byte("portico.server-request\x00" + method + "\n" + pathAndQuery + "\n" +
		strconv.FormatInt(at.Unix(), 10) + "\n" + hex.EncodeToString(digest[:]))
}

// SignRequest answers the values of the two headers.
func SignRequest(key ed25519.PrivateKey, method, pathAndQuery string, at time.Time, body []byte) (timeHeader, signatureHeader string) {
	return strconv.FormatInt(at.Unix(), 10), encoding.EncodeToString(ed25519.Sign(key, requestBytes(method, pathAndQuery, at, body)))
}

// VerifyRequest checks the two headers against the server's public key and the clock.
func VerifyRequest(public ed25519.PublicKey, method, pathAndQuery, timeHeader, signatureHeader string, body []byte, now time.Time) error {
	seconds, err := strconv.ParseInt(timeHeader, 10, 64)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return ErrTrust
	}
	at := time.Unix(seconds, 0)
	if now.Sub(at) > RequestWindow || at.Sub(now) > RequestWindow {
		return ErrTrust
	}
	signature, err := encoding.DecodeString(signatureHeader)
	if err != nil || !ed25519.Verify(public, requestBytes(method, pathAndQuery, at, body), signature) {
		return ErrTrust
	}
	return nil
}

// Ack is a server's signed answer to a note: "I am this server and I have applied
// everything up to these numbers." Portico Services accepts a pushed note as delivered only
// on this, so a machine that has taken over a server's address cannot fake it.
type Ack struct {
	ServerID string `json:"serverId"`
	Revision int64  `json:"revision"`
	Fleet    int64  `json:"fleet"`
	// CertificateRevision is the newest signing certificate the server has seen.
	CertificateRevision uint64 `json:"certificateRevision"`
}

// SignedAck is an Ack as it travels.
type SignedAck struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

const ackLabel = "portico.server-ack\x00"

func SignAck(key ed25519.PrivateKey, ack Ack) SignedAck {
	payload, _ := json.Marshal(ack)
	return SignedAck{encoding.EncodeToString(payload), encoding.EncodeToString(ed25519.Sign(key, append([]byte(ackLabel), payload...)))}
}

func VerifyAck(public ed25519.PublicKey, serverID string, signed SignedAck) (Ack, error) {
	payload, err := encoding.DecodeString(signed.Payload)
	if err != nil || len(payload) > 1024 || len(public) != ed25519.PublicKeySize {
		return Ack{}, ErrTrust
	}
	signature, err := encoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(public, append([]byte(ackLabel), payload...), signature) {
		return Ack{}, ErrTrust
	}
	var ack Ack
	if json.Unmarshal(payload, &ack) != nil || ack.ServerID != serverID {
		return Ack{}, ErrTrust
	}
	return ack, nil
}
