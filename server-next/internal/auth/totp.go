package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Two-step sign-in uses the authenticator-app standard (TOTP): SHA-1, six digits, a new
// code every 30 seconds, and the code before or after the current one is accepted so a
// slightly wrong clock still works.

const totpPeriod = 30 * time.Second

var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a new authenticator secret.
func NewTOTPSecret() []byte {
	secret := make([]byte, 20)
	rand.Read(secret)
	return secret
}

// TOTPSetup returns the secret as text to type into an authenticator app, and as the
// address behind the QR code that does the same.
func TOTPSetup(secret []byte, server, username string) (text, uri string) {
	text = secretEncoding.EncodeToString(secret)
	label := url.PathEscape(server + ":" + username)
	return text, "otpauth://totp/" + label + "?secret=" + text + "&issuer=" + url.QueryEscape(server)
}

// TOTPCode is the code an authenticator shows during the given 30-second step.
func TOTPCode(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

// TOTPStep is the 30-second step that now falls in.
func TOTPStep(now time.Time) int64 { return now.Unix() / int64(totpPeriod/time.Second) }

// CheckTOTP reports whether code is right for secret now, and which step it belongs to.
// A step at or before lastStep is refused, so a code that was accepted once cannot be
// used again by someone who watched it being typed.
func CheckTOTP(secret []byte, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 || len(secret) == 0 {
		return 0, false
	}
	current := TOTPStep(now)
	for _, candidate := range []int64{current, current - 1, current + 1} {
		if candidate > lastStep && subtle.ConstantTimeCompare([]byte(TOTPCode(secret, candidate)), []byte(code)) == 1 {
			return candidate, true
		}
	}
	return 0, false
}

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

// NewSecret returns a long random secret for a machine to hold, such as an API key's.
func NewSecret() string {
	raw := make([]byte, 24)
	rand.Read(raw)
	return strings.ToLower(secretEncoding.EncodeToString(raw))
}
