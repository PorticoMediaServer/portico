package hosted

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// OfficialRoot is set with -X by the controlled release builder. Its value is the
// unpadded base64url public key actually used to verify Portico Services documents.
var OfficialRoot string

func linkedRoot() (ed25519.PublicKey, error) {
	key, err := base64.RawURLEncoding.Strict().DecodeString(OfficialRoot)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Portico Services root key")
	}
	sum := sha256.Sum256(key)
	if hex.EncodeToString(sum[:]) == "a96af130ee5a59294c90fcb8883dc7c6c3878365d970f4a6e67fa7ed2fc7ef4c" {
		return nil, errors.New("a development root cannot be used for Portico Services")
	}
	return ed25519.PublicKey(key), nil
}
