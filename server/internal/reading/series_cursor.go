package reading

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const seriesCursorMagic = "PRDSER01"

type seriesTuple struct {
	Numbered int
	Number   float64
	Sort     string
	Item     int64
}

func (t seriesTuple) digest() ([32]byte, error) {
	if t.Item <= 0 || t.Numbered < 0 || t.Numbered > 1 || math.IsNaN(t.Number) || math.IsInf(t.Number, 0) || t.Number < 0 || t.Numbered == 1 && t.Number != 0 || !utf8.ValidString(t.Sort) {
		return [32]byte{}, ErrChanged
	}
	number := t.Number
	if number == 0 {
		number = 0 // Positive zero is the sole canonical binary64 zero.
	}
	h := sha256.New()
	h.Write([]byte("portico.reading.series.tuple.v1\x00"))
	h.Write([]byte{byte(t.Numbered)})
	var u [8]byte
	binary.BigEndian.PutUint64(u[:], math.Float64bits(number))
	h.Write(u[:])
	binary.BigEndian.PutUint64(u[:], uint64(len(t.Sort)))
	h.Write(u[:])
	h.Write([]byte(t.Sort))
	binary.BigEndian.PutUint64(u[:], uint64(t.Item))
	h.Write(u[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func seriesScopeDigest(library, series, kind string, scope SeriesScope, limit int) ([32]byte, error) {
	if limit < 1 || limit > 100 {
		return [32]byte{}, ErrInvalid
	}
	h := sha256.New()
	h.Write([]byte("portico.reading.series.scope.v1\x00"))
	var u [8]byte
	for _, value := range []string{library, series, kind, scope.Profile, scope.Fence, scope.RestrictionFence, scope.Formats} {
		if !utf8.ValidString(value) {
			return [32]byte{}, ErrChanged
		}
		binary.BigEndian.PutUint64(u[:], uint64(len(value)))
		h.Write(u[:])
		h.Write([]byte(value))
	}
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(limit))
	h.Write(n[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

type seriesCursor struct {
	Namespace, Input, Ready, Catalog, Viewer, Anchor, Expiry int64
	Scope, Tuple                                             [32]byte
}

func (c seriesCursor) valid(now time.Time) bool {
	return c.Namespace > 0 && c.Input > 0 && c.Input == c.Ready && c.Catalog >= 0 && c.Viewer >= 0 && c.Anchor > 0 && c.Expiry > now.Unix()
}

func (c seriesCursor) encode(key []byte) (string, error) {
	if len(key) != 32 || !c.valid(time.Unix(0, 0)) {
		return "", ErrChanged
	}
	raw := make([]byte, 128)
	copy(raw, seriesCursorMagic)
	for i, value := range []int64{c.Namespace, c.Input, c.Ready, c.Catalog, c.Viewer, c.Anchor, c.Expiry} {
		binary.BigEndian.PutUint64(raw[8+i*8:], uint64(value))
	}
	copy(raw[64:96], c.Scope[:])
	copy(raw[96:], c.Tuple[:])
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeSeriesCursor(value string, key []byte, now time.Time) (seriesCursor, error) {
	var c seriesCursor
	if len(value) != 215 || len(key) != 32 || value[171] != '.' {
		return c, ErrChanged
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return c, ErrChanged
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil || len(raw) != 128 || base64.RawURLEncoding.EncodeToString(raw) != parts[0] || string(raw[:8]) != seriesCursorMagic {
		return c, ErrChanged
	}
	signed, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	if err != nil || len(signed) != 32 || base64.RawURLEncoding.EncodeToString(signed) != parts[1] || !hmac.Equal(signed, mac.Sum(nil)) {
		return c, ErrChanged
	}
	for i, target := range []*int64{&c.Namespace, &c.Input, &c.Ready, &c.Catalog, &c.Viewer, &c.Anchor, &c.Expiry} {
		value := binary.BigEndian.Uint64(raw[8+i*8:])
		if value > math.MaxInt64 {
			return seriesCursor{}, ErrChanged
		}
		*target = int64(value)
	}
	copy(c.Scope[:], raw[64:96])
	copy(c.Tuple[:], raw[96:])
	if !c.valid(now) {
		return seriesCursor{}, ErrChanged
	}
	return c, nil
}
