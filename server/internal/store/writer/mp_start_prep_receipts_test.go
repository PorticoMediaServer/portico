package writer

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func mpStartPrepTestValue(kind CommandID) mpStartPrepValue {
	v := mpStartPrepValue{Preparation: "prep", State: "pending", Revision: 1, DeadlineMS: 900, AppliedMS: 100,
		Session: "session", LegacySession: "legacy", LegacyGeneration: 9, Snapshot: "snapshot"}
	switch kind {
	case MPStartPrepClaim:
		v.State, v.Execution, v.Sealed, v.Revision = "preparing", "execution", true, 2
	case MPStartPrepSeal:
		v.State, v.Execution, v.Sealed, v.Revision = "prepared", "execution", true, 3
	}
	return v
}

func TestMPStartPrepPayloadIsACompleteOriginal(t *testing.T) {
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}
	request := RequestDigest{3}
	for _, kind := range []CommandID{MPStartPrepBegin, MPStartPrepClaim, MPStartPrepSeal} {
		value := mpStartPrepTestValue(kind)
		payload, digest, err := mpStartPrepPayload(id, request, value)
		if err != nil || len(payload) == 0 || len(payload) > mpStartPrepPayloadCap || digest == (ResultDigest{}) {
			t.Fatal(kind, len(payload), digest, err)
		}
		decoded, err := decodeMPStartPrepPayload(id, request, payload, digest)
		if err != nil || decoded != value {
			t.Fatal("original bytes changed", decoded, err)
		}
		// Every identity/request/payload mutation must be refused.
		other := id
		other.Sequence++
		if _, err := decodeMPStartPrepPayload(other, request, payload, digest); err == nil {
			t.Fatal("sequence drift accepted")
		}
		other = id
		other.OperationID[0] ^= 1
		if _, err := decodeMPStartPrepPayload(other, request, payload, digest); err == nil {
			t.Fatal("operation drift accepted")
		}
		if _, err := decodeMPStartPrepPayload(id, RequestDigest{4}, payload, digest); err == nil {
			t.Fatal("request drift accepted")
		}
		if _, err := decodeMPStartPrepPayload(id, request, append(append([]byte{}, payload...), 0), digest); err == nil {
			t.Fatal("trailing bytes accepted")
		}
		forged := value
		forged.Sealed = false
		if _, _, err := mpStartPrepPayload(id, request, forged); err == nil {
			t.Fatal("state/flag mismatch encoded")
		}
		if bytes.Equal(payload, []byte(strings.Repeat("x", len(payload)))) {
			t.Fatal("empty codec")
		}
	}
	// A result outside the declared state/flag relationship is never canonical.
	for _, v := range []mpStartPrepValue{
		{Preparation: "prep", State: "pending", Revision: 1, DeadlineMS: 1, AppliedMS: 1, Sealed: true, Execution: "e"},
		{Preparation: "prep", State: "preparing", Revision: 2, DeadlineMS: 1, AppliedMS: 1},
		{Preparation: "prep", State: "prepared", Revision: 3, DeadlineMS: 1, AppliedMS: 1, Sealed: true},
		{Preparation: "", State: "pending", Revision: 1, DeadlineMS: 1, AppliedMS: 1},
	} {
		if err := preflightMPStartPrepValue(v); err == nil {
			t.Fatal("invalid result encoded", v)
		}
	}
	if mpStartPrepValueKind("retired") != 0 || mpStartPrepValueKind("pending") != MPStartPrepBegin ||
		mpStartPrepValueKind("preparing") != MPStartPrepClaim || mpStartPrepValueKind("prepared") != MPStartPrepSeal {
		t.Fatal("result state lost its command")
	}
	if !reflect.DeepEqual(mpStartPrepTestValue(MPStartPrepSeal), mpStartPrepTestValue(MPStartPrepSeal)) {
		t.Fatal("nondeterministic fixture")
	}
}
