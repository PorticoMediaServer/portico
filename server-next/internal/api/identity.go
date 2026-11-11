package api

import (
	"crypto/ed25519"
	"sync"

	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/argon2"
	"math/big"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// identity is the key this server proves itself with. An app remembers the public key the
// first time it connects, and from then on sends its sign-in to an address only after that
// address has signed a fresh question with the same key. So a different machine that takes
// over the address, or a wrong address that happens to answer, is never sent a password or
// a token. The server's id is made from the public key, so the two cannot be told apart.
type identity struct {
	key         ed25519.PrivateKey
	codeOnce    *sync.Once
	codeValue   string
	id          string
	publicKey   string
	fingerprint string
}

func newIdentity(seed string) (identity, error) {
	raw, err := base64.RawURLEncoding.DecodeString(seed)
	if err != nil || len(raw) != ed25519.SeedSize {
		return identity{}, errors.New("the stored identity key is damaged")
	}
	key := ed25519.NewKeyFromSeed(raw)
	public := []byte(key.Public().(ed25519.PublicKey))
	id := sha256.Sum256(append([]byte("portico.server.identity.v1\x00"), public...))
	digest := sha256.Sum256(public)
	enc := base64.RawURLEncoding.EncodeToString
	return identity{key: key, codeOnce: &sync.Once{}, id: "srv_" + enc(id[:]), publicKey: enc(public), fingerprint: enc(digest[:])}, nil
}

// identityCode is the server's identity in twelve characters a person can read out, such as
// "JJWY-C8J8-J26F". The owner reads it in Remote access; an app that meets this server away
// from home through the owner's own certificate, with nothing else to vouch for it, asks for
// it once. The code is public, so it only helps if making a key with the same code is out of
// reach: it is the first 8 bytes of Argon2id of the server's public key (the salt
// "portico identity code", 3 passes, 64 MiB, one lane), taken modulo 31^12 and written in the
// code alphabet. Each try costs a quarter of a second and 64 MiB, so the 2^59 tries a matching
// key needs are out of reach, while an app pays it once per first meeting. The app works it out
// from the public key in the proof the same way; these parameters can never change once one does.
func identityCode(publicKey []byte) string {
	sum := argon2.IDKey(publicKey, []byte("portico identity code"), 3, 64<<10, 1, 8)
	n := new(big.Int).SetUint64(binary.BigEndian.Uint64(sum))
	radix := big.NewInt(int64(len(auth.CodeAlphabet)))
	n.Mod(n, new(big.Int).Exp(radix, big.NewInt(12), nil))
	out := make([]byte, 12)
	digit := new(big.Int)
	for i := 11; i >= 0; i-- {
		n.DivMod(n, radix, digit)
		out[i] = auth.CodeAlphabet[digit.Int64()]
	}
	return string(out[:4]) + "-" + string(out[4:8]) + "-" + string(out[8:])
}

// code is this server's identity code, worked out once, when first asked for.
func (i *identity) code() string {
	i.codeOnce.Do(func() { i.codeValue = identityCode(i.key.Public().(ed25519.PublicKey)) })
	return i.codeValue
}

// IdentityQuestion is what an app asks an address before trusting it: the address it used
// and a random value it has just made, 43 letters, digits, "_" or "-".
type IdentityQuestion struct {
	BaseURL string `json:"baseUrl"`
	Nonce   string `json:"nonce"`
}

// IdentityProof is the answer. Payload is JSON in base64url: the question's two values with
// this server's id, public key and the key's fingerprint. Signature is the server's Ed25519
// signature of the payload's bytes, in base64url.
type IdentityProof struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func (s *Server) identityRoutes() {
	Handle(s, "POST /v1/system/identity-proof", Public, func(r *http.Request, req IdentityQuestion) (IdentityProof, error) {
		if !auth.ValidToken(req.Nonce, 43) || len(req.Nonce) != 43 {
			return IdentityProof{}, ErrField("nonce", "Send a new random value of 43 letters, digits, _ or -.")
		}
		if len(req.BaseURL) == 0 || len(req.BaseURL) > 300 {
			return IdentityProof{}, ErrField("baseUrl", "Send the address this request was made to.")
		}
		payload, _ := json.Marshal(struct {
			Kind        string `json:"kind"`
			Version     string `json:"version"`
			BaseURL     string `json:"baseUrl"`
			Nonce       string `json:"nonce"`
			ServerID    string `json:"serverId"`
			PublicKey   string `json:"publicKey"`
			Fingerprint string `json:"fingerprint"`
			// Certificates are the SHA-256 fingerprints, base64url, of the certificates this
			// server serves itself (the owner's own), so an app that met one over HTTPS knows
			// the server's identity key vouches for it, and a renewal asks nothing.
			Certificates []string `json:"certificates,omitempty"`
		}{"portico.route-proof", "1", req.BaseURL, req.Nonce, s.identity.id, s.identity.publicKey, s.identity.fingerprint, s.certs.Fingerprints()})
		enc := base64.RawURLEncoding.EncodeToString
		return IdentityProof{Payload: enc(payload), Signature: enc(ed25519.Sign(s.identity.key, payload))}, nil
	})
}
