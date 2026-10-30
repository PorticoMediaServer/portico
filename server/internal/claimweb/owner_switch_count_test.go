package claimweb

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func ownerCountFixture(t *testing.T) (ed25519.PrivateKey, Request, OwnerStart, OwnerSwitchCount, time.Time) {
	t.Helper()
	key, claim, start, now := ownerStartFixture(t)
	count := OwnerSwitchCount{Kind: OwnerSwitchCountKind, ServerID: claim.ServerID, PublicKey: claim.PublicKey,
		OperationID: claim.OperationID, Purpose: "switch", PreviewID: "preview_count_vector", PreviewRevision: 3,
		LocalGeneration: claim.LocalGeneration, ClaimHash: start.ClaimHash, OtherPeopleCount: 17,
		IssuedAt: start.IssuedAt, ExpiresAt: start.ExpiresAt}
	return key, claim, start, count, now
}

func TestOwnerSwitchCountCanonicalTwelveFieldVector(t *testing.T) {
	key, claim, start, count, now := ownerCountFixture(t)
	for _, people := range []int{0, 17, 10000} {
		count.OtherPeopleCount = people
		got, e := VerifyOwnerSwitchCount(signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start), signOwnerFixture(t, key, count), claim.Audience, now)
		if e != nil || got != count {
			t.Fatal("bounded captured count refused")
		}
	}
	raw, _ := json.Marshal(count)
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
	if strings.Join(names, ",") != "kind,serverId,publicKey,operationId,purpose,previewId,previewRevision,localGeneration,claimHash,otherPeopleCount,issuedAt,expiresAt" || len(names) != 12 {
		t.Fatal("changed twelve-field signed order")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", count, count), count.ServerID) {
		t.Fatal("count diagnostic leaked binding")
	}
}

func TestOwnerSwitchCountRefusesWrongBindingAndBounds(t *testing.T) {
	key, claim, start, original, now := ownerCountFixture(t)
	for name, change := range map[string]func(*OwnerSwitchCount){
		"kind":   func(v *OwnerSwitchCount) { v.Kind = "portico.setup-start.v1" },
		"server": func(v *OwnerSwitchCount) { v.ServerID += "x" },
		"key": func(v *OwnerSwitchCount) {
			v.PublicKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
		},
		"operation":  func(v *OwnerSwitchCount) { v.OperationID += "x" },
		"purpose":    func(v *OwnerSwitchCount) { v.Purpose = "setup" },
		"preview":    func(v *OwnerSwitchCount) { v.PreviewID = "" },
		"revision":   func(v *OwnerSwitchCount) { v.PreviewRevision = 0 },
		"generation": func(v *OwnerSwitchCount) { v.LocalGeneration++ },
		"claim hash": func(v *OwnerSwitchCount) {
			v.ClaimHash = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
		},
		"negative":     func(v *OwnerSwitchCount) { v.OtherPeopleCount = -1 },
		"overflow":     func(v *OwnerSwitchCount) { v.OtherPeopleCount = 10001 },
		"expired":      func(v *OwnerSwitchCount) { v.ExpiresAt = now },
		"start expiry": func(v *OwnerSwitchCount) { v.ExpiresAt = start.ExpiresAt.Add(time.Nanosecond) },
		"early":        func(v *OwnerSwitchCount) { v.IssuedAt = claim.IssuedAt.Add(-time.Nanosecond) },
		"future":       func(v *OwnerSwitchCount) { v.IssuedAt = now.Add(time.Minute + time.Nanosecond) },
	} {
		t.Run(name, func(t *testing.T) {
			v := original
			change(&v)
			if _, e := VerifyOwnerSwitchCount(signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start), signOwnerFixture(t, key, v), claim.Audience, now); e == nil {
				t.Fatal("invalid optional count accepted")
			}
		})
	}
}

func TestOwnerSwitchCountRequiresCanonicalProofAndBothAuthorities(t *testing.T) {
	key, claim, start, count, now := ownerCountFixture(t)
	original, owner := signOwnerFixture(t, key, claim), signOwnerFixture(t, key, start)
	raw, _ := json.Marshal(count)
	for _, malformed := range []string{
		strings.Replace(string(raw), `"previewRevision":"3"`, `"previewRevision":"03"`, 1),
		strings.Replace(string(raw), `"otherPeopleCount":17`, `"otherPeopleCount":17.0`, 1),
		strings.Replace(string(raw), `"otherPeopleCount":17,`, ``, 1),
		strings.Replace(string(raw), `"otherPeopleCount":17`, `"otherPeopleCount":null`, 1),
		strings.Replace(string(raw), `"kind":`, `"extra":true,"kind":`, 1),
		strings.Replace(string(raw), `"purpose":"switch"`, `"purpose":"switch","purpose":"switch"`, 1),
		strings.Replace(string(raw), `2030-01-02T03:04:06Z`, `2030-01-02T04:04:06+01:00`, 1),
	} {
		if _, e := VerifyOwnerSwitchCount(original, owner, signOwnerFixtureBytes(key, []byte(malformed)), claim.Audience, now); e == nil {
			t.Fatal("noncanonical optional count accepted")
		}
	}
	proof := signOwnerFixture(t, key, count)
	for _, tuple := range [][3]Signed{{{}, owner, proof}, {original, {}, proof}, {original, owner, {}}, {original, owner, {Payload: proof.Payload + "\n", Signature: proof.Signature}}, {original, owner, {Payload: proof.Payload, Signature: proof.Signature + "="}}} {
		if _, e := VerifyOwnerSwitchCount(tuple[0], tuple[1], tuple[2], claim.Audience, now); e == nil {
			t.Fatal("missing or noncanonical authority accepted")
		}
	}
	if _, e := VerifyOwnerSwitchCount(original, owner, proof, "https://other.example.test", now); e == nil {
		t.Fatal("foreign audience accepted")
	}
}
