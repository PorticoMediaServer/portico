package claimweb

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

// This fixed key is a public interoperability fixture, never a credential.
func ownerStartFixture(t *testing.T) (ed25519.PrivateKey, Request, OwnerStart, time.Time) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	public := key.Public().(ed25519.PublicKey)
	digest := sha256.New()
	digest.Write([]byte("portico.server.identity.v1\x00"))
	digest.Write(public)
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	claim := Request{Kind: "portico.claim.web.v2", Audience: "https://accounts.example.test", OperationID: "op_owner_vector", ServerID: "srv_" + base64.RawURLEncoding.EncodeToString(digest.Sum(nil)), PublicKey: base64.RawURLEncoding.EncodeToString(public), LocalGeneration: 7, Name: "Vector server", Address: "https://server.example.test", Purpose: "switch", IssuedAt: at, ExpiresAt: at.Add(10 * time.Minute)}
	raw, e := json.Marshal(claim)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(raw)
	challenge := sha256.Sum256([]byte("public-vector-pkce-verifier-only-not-a-runtime-secret"))
	start := OwnerStart{Kind: OwnerStartKind, Audience: claim.Audience, ClaimHash: base64.RawURLEncoding.EncodeToString(hash[:]), OperationID: claim.OperationID, ServerID: claim.ServerID, PublicKey: claim.PublicKey, LocalGeneration: claim.LocalGeneration, Address: claim.Address, Purpose: claim.Purpose, State: strings.Repeat("s", 43), CodeChallenge: base64.RawURLEncoding.EncodeToString(challenge[:]), InstallationID: strings.Repeat("i", 43), ServerChallenge: strings.Repeat("c", 43), IssuedAt: at.Add(time.Second), ExpiresAt: at.Add(5 * time.Minute)}
	return key, claim, start, at.Add(time.Minute)
}

func signOwnerFixture(t *testing.T, key ed25519.PrivateKey, value any) Signed {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return signOwnerFixtureBytes(key, raw)
}

func signOwnerFixtureBytes(key ed25519.PrivateKey, raw []byte) Signed {
	return Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))}
}

func TestOwnerStartCanonicalVector(t *testing.T) {
	key, claim, start, now := ownerStartFixture(t)
	original, signed := signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start)
	gotClaim, gotStart, e := VerifyOwnerStart(original, signed, claim.Audience, now)
	if e != nil || gotClaim != claim || gotStart != start {
		t.Fatal("canonical signed pair refused", e)
	}
	var vector struct {
		ClaimPayload string `json:"claimPayload"`
		StartPayload string `json:"startPayload"`
		Claim        Signed `json:"claim"`
		Start        Signed `json:"start"`
	}
	raw, e := os.ReadFile("testdata/owner_start.json")
	if e != nil || json.Unmarshal(raw, &vector) != nil {
		t.Fatal("interoperability vector missing or invalid", e)
	}
	claimRaw, _ := json.Marshal(claim)
	startRaw, _ := json.Marshal(start)
	if vector.Claim != original || vector.Start != signed || vector.ClaimPayload != string(claimRaw) || vector.StartPayload != string(startRaw) {
		t.Fatal("canonical bytes/signatures differ from the pinned public vector")
	}
	if fmt.Sprint(start) != "[owner start redacted]" || fmt.Sprintf("%#v", start) != "[owner start redacted]" {
		t.Fatal("owner start fields leaked through formatting")
	}
}

func TestOwnerStartRejectsSignedTupleChanges(t *testing.T) {
	key, claim, start, now := ownerStartFixture(t)
	original := signOwnerFixture(t, key, claim)
	cases := map[string]func(*OwnerStart){
		"kind":              func(v *OwnerStart) { v.Kind = "portico.server-return.v1" },
		"audience":          func(v *OwnerStart) { v.Audience = "https://other.example.test" },
		"claim hash":        func(v *OwnerStart) { v.ClaimHash = strings.Repeat("A", 43) },
		"operation":         func(v *OwnerStart) { v.OperationID += "_other" },
		"server":            func(v *OwnerStart) { v.ServerID += "_other" },
		"key":               func(v *OwnerStart) { v.PublicKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)) },
		"generation":        func(v *OwnerStart) { v.LocalGeneration++ },
		"origin":            func(v *OwnerStart) { v.Address = "https://other.example.test" },
		"http":              func(v *OwnerStart) { v.Address = "http://server.example.test" },
		"purpose":           func(v *OwnerStart) { v.Purpose = "sign-in" },
		"state":             func(v *OwnerStart) { v.State = "short" },
		"pkce":              func(v *OwnerStart) { v.CodeChallenge = "short" },
		"installation":      func(v *OwnerStart) { v.InstallationID = "short" },
		"challenge":         func(v *OwnerStart) { v.ServerChallenge = "short" },
		"future":            func(v *OwnerStart) { v.IssuedAt = now.Add(2 * time.Minute) },
		"before claim":      func(v *OwnerStart) { v.IssuedAt = claim.IssuedAt.Add(-time.Second) },
		"expiry":            func(v *OwnerStart) { v.ExpiresAt = now },
		"empty interval":    func(v *OwnerStart) { v.ExpiresAt = v.IssuedAt },
		"over the step":     func(v *OwnerStart) { v.ExpiresAt = v.IssuedAt.Add(OwnerStartLifetime + time.Nanosecond) },
		"over claim expiry": func(v *OwnerStart) { v.ExpiresAt = claim.ExpiresAt.Add(time.Second) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			value := start
			change(&value)
			if _, _, e := VerifyOwnerStart(original, signOwnerFixture(t, key, value), claim.Audience, now); e == nil {
				t.Fatal("signed altered start accepted")
			}
		})
	}
	// New state/installation/challenge are opaque, not external authority. Their
	// equality to private pending and stored challenge belongs to runtime checks.
	// The exact boundary: a start may last a whole Portico Account step, inside a
	// claim that does too.
	claim.ExpiresAt = claim.IssuedAt.Add(RequestLifetime(claim.Purpose))
	original = signOwnerFixture(t, key, claim)
	raw, _ := json.Marshal(claim)
	hash := sha256.Sum256(raw)
	start.ClaimHash = base64.RawURLEncoding.EncodeToString(hash[:])
	start.IssuedAt, start.ExpiresAt = claim.IssuedAt, claim.IssuedAt.Add(OwnerStartLifetime)
	if _, _, e := VerifyOwnerStart(original, signOwnerFixture(t, key, start), claim.Audience, now); e != nil {
		t.Fatal("exact boundary refused", e)
	}
}

func TestOwnerStartRejectsNoncanonicalSignedJSON(t *testing.T) {
	key, claim, start, now := ownerStartFixture(t)
	original := signOwnerFixture(t, key, claim)
	raw, _ := json.Marshal(start)
	cases := map[string]string{
		"duplicate":           strings.Replace(string(raw), `"kind":`, `"kind":"portico.owner-start.v1","kind":`, 1),
		"unknown":             strings.TrimSuffix(string(raw), "}") + `,"unknown":true}`,
		"missing":             strings.Replace(string(raw), `"purpose":"switch",`, "", 1),
		"whitespace":          " " + string(raw),
		"trailing value":      string(raw) + "{}",
		"field order":         strings.Replace(string(raw), `"kind":"portico.owner-start.v1","audience":"https://accounts.example.test"`, `"audience":"https://accounts.example.test","kind":"portico.owner-start.v1"`, 1),
		"generation encoding": strings.Replace(string(raw), `"localGeneration":"7"`, `"localGeneration":"07"`, 1),
		"non-UTC time":        strings.Replace(string(raw), "2030-01-02T03:04:06Z", "2030-01-02T04:04:06+01:00", 1),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if value == string(raw) {
				t.Fatal("negative fixture did not change bytes")
			}
			if _, _, e := VerifyOwnerStart(original, signOwnerFixtureBytes(key, []byte(value)), claim.Audience, now); e == nil {
				t.Fatal("validly signed noncanonical start accepted")
			}
		})
	}
	claimRaw, _ := json.Marshal(claim)
	claimRaw = append([]byte(" "), claimRaw...)
	// Even an updated matching hash cannot authorize a noncanonical original.
	hash := sha256.Sum256(claimRaw)
	start.ClaimHash = base64.RawURLEncoding.EncodeToString(hash[:])
	if _, _, e := VerifyOwnerStart(signOwnerFixtureBytes(key, claimRaw), signOwnerFixture(t, key, start), claim.Audience, now); e == nil {
		t.Fatal("noncanonical original claim accepted")
	}
}

func TestOwnerStartRejectsBase64AliasesAndSignatureReplacement(t *testing.T) {
	key, claim, start, now := ownerStartFixture(t)
	original, signed := signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start)
	for _, name := range []string{"claim payload", "claim signature", "start payload", "start signature", "claim key", "start key", "claim hash", "pkce"} {
		t.Run(name, func(t *testing.T) {
			a, b := original, signed
			q, v := claim, start
			switch name {
			case "claim payload":
				a.Payload += "\r\n"
			case "claim signature":
				a.Signature += "\r\n"
			case "start payload":
				b.Payload += "\r\n"
			case "start signature":
				b.Signature += "\r\n"
			case "claim key":
				q.PublicKey += "\r\n"
				a = signOwnerFixture(t, key, q)
			case "start key":
				v.PublicKey += "\r\n"
				b = signOwnerFixture(t, key, v)
			case "claim hash":
				v.ClaimHash += "\r\n"
				b = signOwnerFixture(t, key, v)
			case "pkce":
				v.CodeChallenge += "\r\n"
				b = signOwnerFixture(t, key, v)
			}
			if _, _, e := VerifyOwnerStart(a, b, claim.Audience, now); e == nil {
				t.Fatal("CR/LF encoding alias accepted")
			}
		})
	}
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x43}, ed25519.SeedSize))
	if _, _, e := VerifyOwnerStart(original, signOwnerFixture(t, other, start), claim.Audience, now); e == nil {
		t.Fatal("a different signer substituted a start")
	}
	original.Signature = signOwnerFixture(t, other, claim).Signature
	if _, _, e := VerifyOwnerStart(original, signed, claim.Audience, now); e == nil {
		t.Fatal("original claim signature replacement accepted")
	}
}

func TestOwnerStartCannotFollowClaimRefreshOrBroadenPurpose(t *testing.T) {
	key, claim, start, now := ownerStartFixture(t)
	signed := signOwnerFixture(t, key, start)
	claim.IssuedAt = claim.IssuedAt.Add(time.Second)
	claim.ExpiresAt = claim.ExpiresAt.Add(time.Second)
	if _, _, e := VerifyOwnerStart(signOwnerFixture(t, key, claim), signed, claim.Audience, now); e == nil {
		t.Fatal("old start followed a time-only claim refresh")
	}
	for _, purpose := range []string{"setup", "change-owner"} {
		_, q, v, at := ownerStartFixture(t)
		q.Purpose, v.Purpose = purpose, purpose
		if purpose == "change-owner" {
			q.CurrentOwnerAccountID, q.PreviousOwnerRole = "acct_old_owner", "member"
		}
		raw, _ := json.Marshal(q)
		hash := sha256.Sum256(raw)
		v.ClaimHash = base64.RawURLEncoding.EncodeToString(hash[:])
		if _, _, e := VerifyOwnerStart(signOwnerFixture(t, key, q), signOwnerFixture(t, key, v), q.Audience, at); e == nil {
			t.Fatal("switch-only scope broadened to", purpose)
		}
	}
}

func TestOwnerStartKeepsClaimExpiryAndExactLargeGeneration(t *testing.T) {
	key, claim, start, now := ownerStartFixture(t)
	claim.LocalGeneration = 9_007_199_254_740_993
	start.LocalGeneration = claim.LocalGeneration
	raw, _ := json.Marshal(claim)
	hash := sha256.Sum256(raw)
	start.ClaimHash = base64.RawURLEncoding.EncodeToString(hash[:])
	_, got, e := VerifyOwnerStart(signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start), claim.Audience, now)
	encoded, _ := json.Marshal(got)
	if e != nil || got.LocalGeneration != claim.LocalGeneration || !bytes.Contains(encoded, []byte(`"localGeneration":"9007199254740993"`)) {
		t.Fatal("generation lost its exact decimal-string representation", e)
	}
	// A shorter original claim cannot be extended by a valid five-minute start.
	claim.ExpiresAt = now.Add(time.Minute)
	raw, _ = json.Marshal(claim)
	hash = sha256.Sum256(raw)
	start.ClaimHash = base64.RawURLEncoding.EncodeToString(hash[:])
	if _, _, e = VerifyOwnerStart(signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start), claim.Audience, now); e == nil {
		t.Fatal("start extended the original claim expiry")
	}
}

// An owner start takes the same plain-HTTP addresses as a setup start (LocalHTTPAddress),
// and none of them is ever a registered return.
func TestOwnerStartLocalHTTPIsSeparateFromRegistration(t *testing.T) {
	cases := []struct {
		address string
		valid   bool
	}{
		{"http://localhost", true},
		{"http://localhost:32500", true},
		{"http://localhost:65535", true},
		{"https://server.example.test", true},
		{"https://server.example.test:32500", true},
		{"http://127.0.0.1:32500", true},
		{"http://[::1]:32500", true},
		{"http://192.168.1.50:32500", true},
		{"http://10.0.0.5:32500", true},
		{"http://[fd00::5]:32500", true},
		{"http://tower.local:32500", true},
		{"http://203.0.113.5:32500", false},
		{"http://[2001:db8::1]:32500", false},
		{"http://[::ffff:192.168.1.2]:32500", false},
		{"http://localhost.:32500", false},
		{"http://LOCALHOST:32500", false},
		{"HTTP://localhost:32500", false},
		{"http://user@localhost:32500", false},
		{"http://localhost:32500/", false},
		{"http://localhost:32500?", false},
		{"http://localhost:32500?x=1", false},
		{"http://localhost:32500#x", false},
		{"http://localhost:", false},
		{"http://localhost:0", false},
		{"http://localhost:65536", false},
		{"http://localhost:032500", false},
		{"http://localhost:abc", false},
		{"https://server.example.test:65536", false},
		{"http://192.0.2.1:32500", false},
	}
	for _, c := range cases {
		t.Run(c.address, func(t *testing.T) {
			if strings.HasPrefix(c.address, "http://") && c.valid != SetupStartAddress(c.address) {
				t.Fatal("owner start and setup start disagree on", c.address)
			}
			key, claim, start, now := ownerStartFixture(t)
			claim.Address, start.Address = c.address, c.address
			raw, _ := json.Marshal(claim)
			hash := sha256.Sum256(raw)
			start.ClaimHash = base64.RawURLEncoding.EncodeToString(hash[:])
			_, _, e := VerifyOwnerStart(signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start), claim.Audience, now)
			if (e == nil) != c.valid {
				t.Fatal("wrong owner-start address acceptance", e)
			}
			if strings.HasPrefix(c.address, "http://") && serverreturn.Origin(c.address) {
				t.Fatal("HTTP address became an actual return registration")
			}
		})
	}
}
