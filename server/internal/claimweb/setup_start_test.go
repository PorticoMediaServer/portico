package claimweb

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func setupStartFixture(t *testing.T) (ed25519.PrivateKey, Request, SetupStart, time.Time) {
	t.Helper()
	key, claim, owner, now := ownerStartFixture(t)
	claim.Purpose = "setup"
	raw, _ := json.Marshal(claim)
	digest := sha256.Sum256(raw)
	start := SetupStart{Kind: SetupStartKind, Audience: claim.Audience, ClaimHash: base64.RawURLEncoding.EncodeToString(digest[:]),
		OperationID: claim.OperationID, ServerID: claim.ServerID, PublicKey: claim.PublicKey, LocalGeneration: claim.LocalGeneration,
		Address: claim.Address, Purpose: "setup", SetupRequestID: "setup_request_vector", SetupRevision: 3,
		State: owner.State, CodeChallenge: owner.CodeChallenge, InstallationID: owner.InstallationID, ServerChallenge: owner.ServerChallenge,
		IssuedAt: owner.IssuedAt, ExpiresAt: owner.ExpiresAt}
	return key, claim, start, now
}

func TestSetupStartCanonicalSeventeenFieldVector(t *testing.T) {
	key, claim, start, now := setupStartFixture(t)
	original, signed := signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start)
	gotClaim, gotStart, e := VerifySetupStart(original, signed, claim.Audience, now)
	if e != nil || gotClaim != claim || gotStart != start {
		t.Fatal("canonical setup envelopes refused")
	}
	raw, _ := json.Marshal(start)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	var names []string
	for decoder.More() {
		name, _ := decoder.Token()
		names = append(names, name.(string))
		var value any
		if decoder.Decode(&value) != nil {
			t.Fatal("invalid vector")
		}
	}
	if len(names) != 17 || strings.Join(names, ",") != "kind,audience,claimHash,operationId,serverId,publicKey,localGeneration,address,purpose,setupRequestId,setupRevision,state,codeChallenge,installationId,serverChallenge,issuedAt,expiresAt" {
		t.Fatal("changed seventeen-field signed order")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", start, start), start.SetupRequestID) {
		t.Fatal("setup diagnostic leaked binding")
	}
	if _, _, e = VerifyOwnerStart(original, signed, claim.Audience, now); e == nil {
		t.Fatal("setup accepted as an owner switch")
	}
	_, switchClaim, owner, _ := ownerStartFixture(t)
	if _, _, e = VerifySetupStart(signOwnerFixture(t, key, switchClaim), signOwnerFixture(t, key, owner), claim.Audience, now); e == nil {
		t.Fatal("owner switch accepted as setup")
	}
}

func TestSetupStartRefusesChangedContextAndLifetime(t *testing.T) {
	key, claim, start, now := setupStartFixture(t)
	for _, field := range []string{"kind", "audience", "hash", "operation", "server", "key", "generation", "address", "purpose", "request", "revision", "state", "challenge", "installation", "server challenge", "expired", "past claim", "early", "future"} {
		t.Run(field, func(t *testing.T) {
			v := start
			switch field {
			case "kind":
				v.Kind = OwnerStartKind
			case "audience":
				v.Audience += "/other"
			case "hash":
				v.ClaimHash = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
			case "operation":
				v.OperationID += "x"
			case "server":
				v.ServerID += "x"
			case "key":
				v.PublicKey = "invalid"
			case "generation":
				v.LocalGeneration++
			case "address":
				v.Address = "http://private.example"
			case "purpose":
				v.Purpose = "switch"
			case "request":
				v.SetupRequestID = ""
			case "revision":
				v.SetupRevision = 0
			case "state":
				v.State = "short"
			case "challenge":
				v.CodeChallenge = "short"
			case "installation":
				v.InstallationID = "short"
			case "server challenge":
				v.ServerChallenge = "short"
			case "expired":
				v.ExpiresAt = now
			case "past claim":
				v.ExpiresAt = claim.ExpiresAt.Add(time.Nanosecond)
			case "early":
				v.IssuedAt = claim.IssuedAt.Add(-time.Nanosecond)
			case "future":
				v.IssuedAt = now.Add(time.Minute + time.Nanosecond)
			}
			if _, _, e := VerifySetupStart(signOwnerFixture(t, key, claim), signOwnerFixture(t, key, v), claim.Audience, now); e == nil {
				t.Fatal("changed setup context accepted")
			}
		})
	}
}

func TestSetupStartRequiresExactCanonicalEnvelopes(t *testing.T) {
	key, claim, start, now := setupStartFixture(t)
	original := signOwnerFixture(t, key, claim)
	raw, _ := json.Marshal(start)
	for _, invalid := range []string{
		strings.Replace(string(raw), `"setupRevision":"3"`, `"setupRevision":"03"`, 1),
		strings.Replace(string(raw), `"setupRevision":"3"`, `"setupRevision":3`, 1),
		strings.Replace(string(raw), `"setupRequestId":"setup_request_vector",`, "", 1),
		strings.Replace(string(raw), `"purpose":"setup"`, `"purpose":"setup","purpose":"setup"`, 1),
		strings.Replace(string(raw), `"kind":`, `"ownerStart":null,"kind":`, 1),
		strings.Replace(string(raw), `2030-01-02T03:04:06Z`, `2030-01-02T04:04:06+01:00`, 1),
	} {
		if _, _, e := VerifySetupStart(original, signOwnerFixtureBytes(key, []byte(invalid)), claim.Audience, now); e == nil {
			t.Fatal("noncanonical setup proof accepted")
		}
	}
	proof := signOwnerFixture(t, key, start)
	proof.Payload += "\n"
	if _, _, e := VerifySetupStart(original, proof, claim.Audience, now); e == nil {
		t.Fatal("noncanonical base64 accepted")
	}
	if _, _, e := VerifySetupStart(Signed{}, signOwnerFixture(t, key, start), claim.Audience, now); e == nil {
		t.Fatal("missing original claim accepted")
	}
}

// PLEX-SETUP-NOTES S1/S3/S5: first-run setup may start from and return to the local
// network over plain HTTP, never a public IP over HTTP.
func TestSetupStartAddressIsLocalOverPlainHTTP(t *testing.T) {
	for _, ok := range []string{
		"https://demo.example", "https://203.0.113.9:32400",
		"http://localhost:32500", "http://127.0.0.1:32500", "http://[::1]:32500",
		"http://10.0.0.5:32400", "http://172.20.1.2:32400", "http://192.168.1.20:32400",
		"http://100.101.102.103:32400", "http://169.254.10.1:32400", "http://[fd00::5]:32400", "http://[fe80::1]:32400",
		"http://tower.local:32400", "http://nas:32400",
	} {
		if !SetupStartAddress(ok) {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{
		"http://198.51.100.7:32400", "http://8.8.8.8", "http://[2001:db8::1]:32400", "http://[::ffff:192.168.1.2]:32400",
		"http://[fe80::1%25eth0]:32400", "http://192.168.1.20:32400/", "http://192.168.1.20:32400?x=1", "http://user@192.168.1.20",
		"http://192.168.1.20:0", "http://192.168.1.20:070", "HTTP://192.168.1.20", "http://Tower.local", "ftp://192.168.1.20", "",
		"http://0xc6336401", "http://0x7f.1", "http://3325256705", "http://198.51.0x64.1", "http://nas.0x1",
	} {
		if SetupStartAddress(bad) {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSetupLifetimeIsThirtyMinutesAndOnlyShortened(t *testing.T) {
	if SetupLifetime() != 30*time.Minute || RequestLifetime("setup") != 30*time.Minute || RequestLifetime("switch") != 10*time.Minute {
		t.Fatal("setup window is not thirty minutes, or other purposes changed")
	}
	SetDevelopmentSetupLifetime(time.Hour)
	if SetupLifetime() != 30*time.Minute {
		t.Fatal("the development knob lengthened the window")
	}
	SetDevelopmentSetupLifetime(20 * time.Second)
	defer setupLifetime.Store(0)
	if SetupLifetime() != 20*time.Second {
		t.Fatal("the development knob did not shorten the window")
	}
}
