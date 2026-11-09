package auth

import (
	"crypto/rand"
	"strings"
)

// codeAlphabet leaves out the letters and digits people mix up when reading a code aloud
// or off a television: I, L, O, 0 and 1.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NewCode returns a random code of the given number of groups of four symbols, such as
// "K7QP-2MXD". It is for codes a person reads and types.
func NewCode(groups int) string {
	var b strings.Builder
	raw := make([]byte, 1)
	for i := 0; i < groups*4; {
		rand.Read(raw)
		// Reject the few byte values that would make some symbols likelier than others.
		if int(raw[0]) >= 256-256%len(codeAlphabet) {
			continue
		}
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(codeAlphabet[int(raw[0])%len(codeAlphabet)])
		i++
	}
	return b.String()
}

// CodeKey is the form of a typed code that is hashed and compared: upper case, with the
// dashes and spaces people add or leave out removed.
func CodeKey(typed string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(typed)))
}
