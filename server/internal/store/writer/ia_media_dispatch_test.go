package writer

import (
	"crypto/sha256"
	"testing"
)

func iaMediaCompiledResult(k CommandID, v iaMediaResult) any {
	switch k {
	case IAAcceptEpisodeInventory:
		return iaEpisodeInventoryResult{v}
	case IAAcceptAudioInventory:
		return iaAudioInventoryResult{v}
	case IAAcceptReadingInventory:
		return iaReadingInventoryResult{v}
	case IAAcceptExtraInventory:
		return iaExtraInventoryResult{v}
	case IAAcceptMembership:
		return iaMembershipResult{v}
	}
	return v
}

func TestIAMediaCanonicalDispatchClosedPurposes(t *testing.T) {
	for _, k := range []CommandID{IAAcceptEpisodeInventory, IAAcceptAudioInventory, IAAcceptReadingInventory, IAAcceptExtraInventory, IAAcceptMembership} {
		id, request, v := iaMediaFixture(k)
		name, err := iaMediaResultKindName(k)
		if err != nil {
			t.Fatal(err)
		}
		got, err := canonicalResultDigest(id, request, iaMediaCompiledResult(k, v))
		want := sha256.Sum256(iaMediaReference(name, id, request, v))
		if err != nil || got != want {
			t.Fatal("compiled purpose/reference mismatch", k, got, want, err)
		}
		e := requestEncoder{h: sha256.New()}
		if err := encodeCanonicalResult(&e, id, request, iaMediaCompiledResult(k, v)); err != nil || e.written != len(iaMediaReference(name, id, request, v)) {
			t.Fatal("compiled encoder differs", k, e.written, err)
		}
		// A malformed complete body must fail before the canonical encoder hashes
		// even its framing. The five wrappers are not generic codec registration.
		v.memberBytes--
		e = requestEncoder{h: sha256.New()}
		if err := encodeCanonicalResult(&e, id, request, iaMediaCompiledResult(k, v)); err == nil || e.written != 0 {
			t.Fatal("malformed body reached hash", k, e.written, err)
		}
		if _, err := canonicalResultDigest(id, request, v); err == nil {
			t.Fatal("untyped media body registered a schema", k)
		}
	}
	// The same accepted episode membership is valid for110 and114; purpose and
	// its actual retained-key charge still produce different immutable digests.
	id, request, v := iaMediaFixture(IAAcceptEpisodeInventory)
	episode, err := canonicalResultDigest(id, request, iaEpisodeInventoryResult{v})
	if err != nil {
		t.Fatal(err)
	}
	v.memberBytes, err = iaMediaMemberRetainedBytes(IAAcceptMembership, v.runID, v.members[0])
	if err != nil {
		t.Fatal(err)
	}
	membership, err := canonicalResultDigest(id, request, iaMembershipResult{v})
	if err != nil || membership == episode {
		t.Fatal("membership borrowed the episode purpose", err)
	}
}
