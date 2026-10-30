package playbackv1

import (
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestSonicWindowCannotChangeTheSeededOccurrenceSetOrExceed256(t *testing.T) {
	ordinary := make([]int64, 256)
	reordered := make([]int64, 256)
	for n := range ordinary {
		ordinary[n] = int64(1000 + n*3)
		reordered[255-n] = ordinary[n]
	}
	raw, err := sonicWindowBytes(ordinary, reordered)
	if err != nil || len(raw) != 2048 {
		t.Fatal(len(raw), err)
	}
	got, err := sonicWindowOrdinals(raw, ordinary)
	if err != nil || !reflect.DeepEqual(got, reordered) {
		t.Fatal(got, err)
	}
	for _, bad := range [][]int64{append(append([]int64(nil), reordered...), 99), {1000, 1000}, {-1}, {999999}} {
		if _, err := sonicWindowBytes(ordinary, bad); err == nil {
			t.Fatal("different occurrence set accepted", bad)
		}
	}
	corrupt := append([]byte(nil), raw...)
	copy(corrupt[8:16], corrupt[:8])
	if _, err := sonicWindowOrdinals(corrupt, ordinary); err == nil {
		t.Fatal("persisted duplicate accepted")
	}
	binary.LittleEndian.PutUint64(corrupt[:8], ^uint64(0))
	if _, err := sonicWindowOrdinals(corrupt, ordinary); err == nil {
		t.Fatal("overflowing source ordinal accepted")
	}
	for _, bad := range [][]byte{nil, raw[:len(raw)-1], append(append([]byte(nil), raw...), make([]byte, 8)...)} {
		if _, err := sonicWindowOrdinals(bad, ordinary); err == nil {
			t.Fatal("invalid persisted row bound accepted", len(bad))
		}
	}
}

func TestSonicWindowIdentityBindsActualOccurrencesWithoutAmbiguousJoins(t *testing.T) {
	left, right := sha256.New(), sha256.New()
	sonicWindowMember(left, 0, "ab")
	sonicWindowMember(left, 1, "c")
	sonicWindowMember(right, 0, "a")
	sonicWindowMember(right, 1, "bc")
	if reflect.DeepEqual(left.Sum(nil), right.Sum(nil)) {
		t.Fatal("different captured occurrences share an ambiguous identity")
	}
}
