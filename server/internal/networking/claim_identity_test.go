package networking

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

func TestClaimIdentitySharedVector(t *testing.T) {
	// RFC8032 section7.1 public key (test1); expected digest is independently
	// calculated with Python hashlib, not from ServerIdentity itself.
	raw, e := hex.DecodeString("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	if e != nil {
		t.Fatal(e)
	}
	id, e := ServerIdentity(ed25519.PublicKey(raw))
	if e != nil {
		t.Fatal(e)
	}
	if id != "srv_7BFIeYyXBpRWRLzgrvXRa8Ml2H7kI5jz7XcCg1ymsaE" {
		t.Fatalf("wrong domain-separated identity: %s", id)
	}
	raw[0] ^= 1
	other, e := ServerIdentity(raw)
	if e != nil || other == id {
		t.Fatal("changed key retained identity")
	}
	for _, bad := range [][]byte{nil, make([]byte, 31), make([]byte, 33)} {
		if _, e = ServerIdentity(bad); e == nil {
			t.Fatal("invalid public key length accepted")
		}
	}
}

func TestIdentityCodeDesignVectorAndCanonicalInputs(t *testing.T) {
	public := make([]byte, 32)
	for i := range public {
		public[i] = byte(i)
	}
	server, e := ServerIdentity(public)
	if e != nil || server != "srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY" {
		t.Fatal("public identity vector mismatch", e)
	}
	if IdentityCode(server) != "JJWY-C8J8-J26F" {
		t.Fatal("identity code design vector mismatch")
	}
	for _, bad := range []string{"", server[4:], "SRV_" + server[4:], server + "=", server[:46], server + "A", server[:46] + "!", server[:46] + "\n", server[:46] + "B", "srv_" + strings.Repeat("A", 42) + " ", "srv_" + strings.Repeat("A", 42) + "é"} {
		if IdentityCode(bad) != "" {
			t.Fatal("invalid/noncanonical identity produced a code")
		}
	}
}

func TestIdentityCodeKeepsLeadingDigitsAndUsesUnsignedBigEndian(t *testing.T) {
	// Boundary vectors are independent consequences of base31, including the
	// reduction at31^12. None are a live server identity.
	for _, tc := range []struct {
		value uint64
		want  string
	}{
		{0, "AAAA-AAAA-AAAA"}, {1, "AAAA-AAAA-AAAB"},
		{787662783788549760, "9999-9999-9999"},
		{787662783788549761, "AAAA-AAAA-AAAA"},
		{787662783788549762, "AAAA-AAAA-AAAB"},
		{18446744073709551615, "QAHF-VNEF-XJGS"},
	} {
		raw := make([]byte, 32)
		binary.BigEndian.PutUint64(raw[:8], tc.value)
		if IdentityCode("srv_"+base64.RawURLEncoding.EncodeToString(raw)) != tc.want {
			t.Fatal("base31 boundary mismatch")
		}
	}
}
