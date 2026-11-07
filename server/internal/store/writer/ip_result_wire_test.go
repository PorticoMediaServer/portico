package writer

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestIPSharedResultIndependentWires(t *testing.T) {
	var identity CommandIdentity
	var request RequestDigest
	var policyInc [16]byte
	for n := range identity.Incarnation {
		identity.Incarnation[n] = byte(n)
		identity.OperationID[n] = byte(n + 16)
		policyInc[n] = byte(n + 64)
	}
	identity.Sequence = 31
	for n := range request {
		request[n] = byte(n + 32)
	}
	// Independent Python struct.pack/hashlib answers from the written wire
	// orders. No result implementation was called to generate these hashes.
	for _, tc := range []struct {
		name     string
		value    any
		expected string
	}{
		{"policy249", ipPolicyResult{incarnation: policyInc, revision: 17, appliedMS: 23, values: ipPolicyValues{enabled: 1, priority: -7, creditEnabled: 1, region: "", algorithm: "v1.é", refreshMode: "replace_unlocked"}, creditAfterItem: sql.NullInt64{Int64: 0, Valid: true}, creditAfterProvider: sql.NullString{String: "tmdb", Valid: true}}, "d19c18f6744c3b3b635bd495244f313e74e63baf0d35311e626e8d060d9dfd66"},
		{"refresh196", ipRefreshSavedResult{IPRefreshPage, ipRefreshResult{revision: 17, generation: 19, epochMS: 23, phase: "people", afterID: 29, scheduled: 3, waiting: 0, appliedMS: 37}}, "e23760a1b8690e4eb15388bbf0c75336437198119cc7814ce1cc595943e3c360"},
		{"artwork168", ipArtworkDiscoverySavedResult{IPArtworkDiscoveryFinish, ipArtworkDiscoveryResult{11, 13, 17, 17}}, "d1339130a40c6674d8a95f5134360eaa549c1ad4837080d93dd028004c62f715"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalResultDigest(identity, request, tc.value)
			if err != nil || fmt.Sprintf("%x", got) != tc.expected {
				t.Fatal(got, err)
			}
			other := identity
			other.OperationID[1] ^= 1
			changed, err := canonicalResultDigest(other, request, tc.value)
			if err != nil || changed == got {
				t.Fatal("result omitted identity", err)
			}
			q := request
			q[3] ^= 1
			changed, err = canonicalResultDigest(identity, q, tc.value)
			if err != nil || changed == got {
				t.Fatal("result omitted original request", err)
			}
		})
	}
}

func TestIPPolicyRetiredResultDoesNotDecodeOrReexecute(t *testing.T) {
	w := ipPolicyFixture(t)
	r := ipPolicyTestRequest()
	turn := ipPolicyTestTurn(t, w, IPPolicySet)
	if _, err := ipPolicySetBody(t.Context(), turn, r, ipPolicyTestDecode()); err != nil {
		t.Fatal(err)
	}
	ipPolicyTestCommit(t, turn)
	ipPolicyTestExec(t, w, `UPDATE metadata_provider_policy SET acknowledged_command_sequence=1,revision=99`)
	ipPolicyTestExec(t, w, `DELETE FROM metadata_policy_receipts`)
	turn = ipPolicyTestTurn(t, w, IPPolicySet)
	got, err := ipPolicySetBody(t.Context(), turn, r, &ipPolicyDecode{})
	if err != nil || got != (ipPolicyResult{retired: true}) {
		t.Fatal(got, err)
	}
}
