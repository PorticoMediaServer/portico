package auth

import (
	"context"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// bcrypt work factors: 10 for a password, 8 for a profile PIN. A PIN is an informal second
// gate behind an account sign-in, not a credential of its own. The cost is stored in each
// hash, so hashes made at an older cost still verify.
const (
	PasswordCost = 10
	PINCost      = 8
)

// Passwords hashes and checks passwords and PINs. Hashing is deliberately slow, so only a
// few run at once and the rest wait their turn; call it outside any database transaction.
type Passwords struct {
	passwordCost, pinCost int
	slots                 chan struct{}
	dummy                 string
}

// NewPasswords returns a hasher with the given costs. Tests pass bcrypt.MinCost to stay fast.
func NewPasswords(passwordCost, pinCost int) *Passwords {
	p := &Passwords{passwordCost: passwordCost, pinCost: pinCost, slots: make(chan struct{}, 2)}
	// What a username nobody has is compared against, so the answer takes as long as for a
	// real account. No password matches it.
	dummy, _ := bcrypt.GenerateFromPassword([]byte(NewKey()), passwordCost)
	p.dummy = string(dummy)
	return p
}

func (p *Passwords) slot(ctx context.Context) (func(), error) {
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Passwords) hash(ctx context.Context, secret string, cost int) (string, error) {
	done, err := p.slot(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), cost)
	return string(hash), err
}

// Hash returns the stored form of a password that ValidPassword accepts.
func (p *Passwords) Hash(ctx context.Context, password string) (string, error) {
	return p.hash(ctx, password, p.passwordCost)
}

// HashPIN returns the stored form of a profile PIN.
func (p *Passwords) HashPIN(ctx context.Context, pin string) (string, error) {
	return p.hash(ctx, pin, p.pinCost)
}

// Check reports whether secret is the password or PIN behind hash. An empty hash, for an
// account that does not exist, costs the same work and is never a match.
func (p *Passwords) Check(ctx context.Context, hash, secret string) (bool, error) {
	done, err := p.slot(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	if hash == "" {
		bcrypt.CompareHashAndPassword([]byte(p.dummy), []byte(secret))
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil, nil
}

// ValidPassword is the one password rule: at least 8 characters. Apps show a strength hint,
// but a weak password is the person's choice. 72 bytes is the most bcrypt reads.
func ValidPassword(v string) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= 8 && len(v) <= 72
}

// ValidPIN reports whether v is a profile PIN: four digits.
func ValidPIN(v string) bool {
	return len(v) == 4 && strings.Trim(v, "0123456789") == ""
}

// UsernameKey returns the form of a username that identifies the account (trimmed, lower
// case), and whether it is acceptable: 3 to 64 bytes with no spaces, controls or "@".
func UsernameKey(v string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(v))
	ok := utf8.ValidString(key) && len(key) >= 3 && len(key) <= 64 &&
		strings.IndexFunc(key, func(r rune) bool { return r <= ' ' || r == 127 || r == '@' }) < 0
	return key, ok
}

// ValidName reports whether v can name a profile or a device: not blank, at most 120 bytes,
// no control characters.
func ValidName(v string) bool {
	return strings.TrimSpace(v) != "" && len(v) <= 120 && utf8.ValidString(v) &&
		strings.IndexFunc(v, func(r rune) bool { return r < ' ' || r == 127 }) < 0
}

// ValidToken reports whether v is an id an app made for itself (an installation id, a
// request id): min to 128 letters, digits, "_" or "-".
func ValidToken(v string, min int) bool {
	return len(v) >= min && len(v) <= 128 &&
		strings.Trim(v, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}
