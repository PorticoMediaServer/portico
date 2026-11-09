package db

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

var idEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// NewID returns a new opaque public id such as "itm_5h2k9q0x7vbd3m1c": the prefix names the
// kind of thing for people reading logs, and the rest is 80 random bits. Clients never parse it.
func NewID(prefix string) string {
	var b [10]byte
	rand.Read(b[:])
	return prefix + "_" + strings.ToLower(idEncoding.EncodeToString(b[:]))
}
