package networking

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestOwnerStartActionStrictProofBindings(t *testing.T) {
	state := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	valid := `{"expected":{"operationId":"actual_op","localGeneration":"0","revision":"1"},"ownerStart":{"state":"` + state + `","codeChallenge":"` + state + `","installationId":"d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8"}}`
	var q ClaimActionRequest
	if err := json.Unmarshal([]byte(valid), &q); err != nil || q.OwnerStart == nil || q.OwnerStart.InstallationID != "d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8" {
		t.Fatal("actual UUID/state rejected", err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"ownerStart":{`, `"ownerStart":null,"ownerStart":{`, 1),
		`{"expected":{"operationId":"actual_op","localGeneration":"0","revision":"1"},"approvalEnvelope":null}`,
		`{"expected":{"operationId":"actual_op","localGeneration":"0","revision":"1"},"ownerStart":null}`,
		strings.Replace(valid, `"state":`, `"State":`, 1),
		strings.Replace(valid, `"localGeneration":"0"`, `"localGeneration":"0","LocalGeneration":"0"`, 1),
		strings.Replace(valid, `"revision":"1"`, `"revision":"1","unknown":true`, 1),
		strings.Replace(valid, `"ownerStart":{`, `"ownerStart":{"callback":"https://other.example",`, 1),
		strings.Replace(valid, state, strings.Repeat("s", 42), 1),
		strings.Replace(valid, `"state":"`+state+`"`, `"state":null`, 1),
		strings.Replace(valid, `"codeChallenge":"`+state+`",`, "", 1),
		valid + `{}`, `null`,
	} {
		if err := json.Unmarshal([]byte(raw), &q); err == nil {
			t.Fatal("non-strict owner start accepted")
		}
	}
}

func TestSetupStartActionIsPurposeDisjoint(t *testing.T) {
	state := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	valid := `{"expected":{"operationId":"setup_op","localGeneration":"0","revision":"1"},"setupStart":{"state":"` + state + `","codeChallenge":"` + state + `","installationId":"d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8"}}`
	var q ClaimActionRequest
	if json.Unmarshal([]byte(valid), &q) != nil || q.SetupStart == nil || q.OwnerStart != nil {
		t.Fatal("setup transport not distinct")
	}
	for _, extra := range []string{`,"ownerStart":{"state":"` + state + `","codeChallenge":"` + state + `","installationId":"d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8"}`, `,"approvalEnvelope":"mixed"`, `,"approvalEnvelope":""`, `,"setupStart":null`} {
		if json.Unmarshal([]byte(valid[:len(valid)-1]+extra+"}"), &q) == nil || q.SetupStart != nil || q.OwnerStart != nil {
			t.Fatal("mixed setup transport retained or accepted")
		}
	}
}
