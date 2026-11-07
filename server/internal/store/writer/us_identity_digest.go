package writer

import "encoding/base64"

// usIdentityDigest validates identity.Digest's canonical, unpadded base64url
// SHA-256 stored in authorization_family_tokens.token_hash. Installation hashes
// and invitation access digests have separate hexadecimal encodings.
func usIdentityDigest(v string) bool {
	if len(v) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(v)
	return err == nil && len(b) == 32
}
