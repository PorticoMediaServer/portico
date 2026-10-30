package reading

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"
)

func TestReadingSeriesCursorFixedFrameCanonicalTupleAndOriginalExpiry(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	now := time.Unix(1800000000, 0)
	tuple := seriesTuple{Numbered: 0, Number: 2.5, Sort: strings.Repeat("日本\x00", 10000), Item: 17}
	digest, err := tuple.digest()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := seriesScopeDigest("library", "series", "book", SeriesScope{Profile: "profile", Fence: "fence", RestrictionFence: "restrictions"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	c := seriesCursor{Namespace: 1, Input: 3, Ready: 3, Catalog: 5, Viewer: 7, Anchor: 17, Expiry: now.Add(30 * time.Minute).Unix(), Scope: scope, Tuple: digest}
	token, err := c.encode(key)
	if err != nil || len(token) != 215 {
		t.Fatal(len(token), err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	if len(raw) != 128 || string(raw[:8]) != "PRDSER01" || binary.BigEndian.Uint64(raw[48:56]) != 17 {
		t.Fatal("wrong approved fixed frame")
	}
	decoded, err := decodeSeriesCursor(token, key, now)
	if err != nil || decoded != c {
		t.Fatal(decoded, err)
	}
	decoded.Anchor = 18
	continued, _ := decoded.encode(key)
	last, err := decodeSeriesCursor(continued, key, now.Add(29*time.Minute))
	if err != nil || last.Expiry != c.Expiry {
		t.Fatal("continuation extended expiry", last, err)
	}
	if _, err = decodeSeriesCursor(continued, key, now.Add(30*time.Minute)); err == nil {
		t.Fatal("original expiry was renewed")
	}
	for _, bad := range []string{token + "=", token[:214], "x" + token[1:], token[:171] + "/" + token[172:]} {
		if _, err = decodeSeriesCursor(bad, key, now); err == nil {
			t.Fatal("malformed/tampered cursor accepted")
		}
	}
}

func TestReadingSeriesTupleDigestPreservesFullBytesAndCanonicalZero(t *testing.T) {
	one := seriesTuple{Numbered: 0, Number: 0, Sort: "a\x00b", Item: 1}
	positive, _ := one.digest()
	one.Number = math.Copysign(0, -1)
	negative, _ := one.digest()
	if positive != negative {
		t.Fatal("negative zero has a second representation")
	}
	one.Sort = "a\x00b\x00"
	other, _ := one.digest()
	if other == positive {
		t.Fatal("sort-key suffix was truncated")
	}
	one.Sort = "a\x00b"
	one.Item = 2
	other, _ = one.digest()
	if other == positive {
		t.Fatal("item anchor absent from tuple digest")
	}
	for _, tuple := range []seriesTuple{{Numbered: 0, Number: math.NaN(), Item: 1}, {Numbered: 0, Number: math.Inf(1), Item: 1}, {Numbered: 1, Number: 1, Item: 1}, {Numbered: 2, Item: 1}, {Item: 0}, {Item: 1, Sort: string([]byte{255})}} {
		if _, err := tuple.digest(); err == nil {
			t.Fatal("invalid canonical tuple accepted", tuple)
		}
	}
	first, _ := seriesScopeDigest("ab", "c", "book", SeriesScope{Profile: "p", Fence: "f", RestrictionFence: "r"}, 50)
	second, _ := seriesScopeDigest("a", "bc", "book", SeriesScope{Profile: "p", Fence: "f", RestrictionFence: "r"}, 50)
	third, _ := seriesScopeDigest("ab", "c", "book", SeriesScope{Profile: "p", Fence: "f", RestrictionFence: "r"}, 51)
	if first == second || first == third {
		t.Fatal("scope fields/limit have ambiguous framing")
	}
}
