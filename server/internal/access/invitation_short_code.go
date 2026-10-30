package access

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

const shortInvitationAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var shortInvitationEncoding = base32.NewEncoding(shortInvitationAlphabet).WithPadding(base32.NoPadding)

// shortInvitationCode is a distinct 80-bit capability. It is never derived from
// a long-link secret. Issuance must still bind its hash to the live invitation.
func newShortInvitationCode() (string, error) {
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return shortInvitationEncoding.EncodeToString(entropy[:]), nil
}

// Only ASCII case and the displayed four groups of four are equivalent. In
// particular, ambiguous letters, Unicode lookalikes and extra separators fail.
func canonicalShortInvitationCode(raw string) (string, bool) {
	if len(raw) == 19 {
		if raw[4] != '-' || raw[9] != '-' || raw[14] != '-' {
			return "", false
		}
		raw = raw[:4] + raw[5:9] + raw[10:14] + raw[15:]
	}
	if len(raw) != 16 {
		return "", false
	}
	code := []byte(raw)
	for i, c := range code {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if !strings.ContainsRune(shortInvitationAlphabet, rune(c)) {
			return "", false
		}
		code[i] = c
	}
	return string(code), true
}

func displayShortInvitationCode(raw string) (string, bool) {
	code, ok := canonicalShortInvitationCode(raw)
	if !ok {
		return "", false
	}
	return code[:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:], true
}
