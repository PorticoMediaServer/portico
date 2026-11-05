package writer

import (
	"crypto/sha256"
	"encoding/binary"
)

const effectiveCreditManifestAlgorithm = "portico-credit-manifest-sha256-chain-v1"

// Declared build intent, not an opaque verified image/delta plan. Sealing must
// independently verify all actual tuples and the complete winner/source proof.
type effectiveBeginHeader struct {
	entityID, sourceRevision, identityRevision, metadataRevision, ownerRevision int64
	entityIncarnation, policyIncarnation                                        [16]byte
	policyRevision, consentRevision                                             int64
	language                                                                    string
	sourceManifest, declaredInput, expectedManifest                             [32]byte
	expectedCount, expectedBytes                                                int64
}

// This is only the INT materialized-credit chain SEED, derived by the writer
// from declared intent. It is not the request/result digest or verified root.
// The command constructor must validate native ranges/UTF-8 before this call.
func effectiveBeginBuildSeed(h effectiveBeginHeader) [32]byte {
	digest := sha256.New()
	digest.Write([]byte(effectiveCreditManifestAlgorithm + "\x00seed\x00"))
	var word [8]byte
	binary.BigEndian.PutUint64(word[:], uint64(h.entityID))
	digest.Write(word[:])
	digest.Write(h.declaredInput[:])
	digest.Write(h.sourceManifest[:])
	binary.BigEndian.PutUint64(word[:], uint64(len(h.language)))
	digest.Write(word[:])
	digest.Write([]byte(h.language))
	for _, value := range []int64{h.expectedCount, h.expectedBytes} {
		binary.BigEndian.PutUint64(word[:], uint64(value))
		digest.Write(word[:])
	}
	var seed [32]byte
	copy(seed[:], digest.Sum(nil))
	return seed
}

func effectiveBeginHeaderValues(h effectiveBeginHeader, request RequestDigest, seed [32]byte, retirementIncarnation [16]byte) []any {
	// Every initial control value is handler-owned. None is request-selectable,
	// nor a claimed future accepted image revision; RETURNING supplies that.
	return []any{
		h.entityID, h.entityIncarnation[:], h.sourceRevision, h.identityRevision,
		h.metadataRevision, h.ownerRevision, h.policyIncarnation[:], h.policyRevision,
		h.consentRevision, h.language, h.sourceManifest[:], request[:],
		effectiveCreditManifestAlgorithm, h.expectedCount, h.expectedBytes, h.expectedManifest[:],
		int64(0), int64(0), int64(0), int64(0), seed[:], seed[:], int64(-1),
		int64(0), int64(0), nil, h.declaredInput[:], int64(0), int64(0), int64(0),
		"held", int64(-1), retirementIncarnation[:], int64(0), int64(0), int64(1),
		int64(0), int64(0), int64(0), int64(0), nil,
	}
}
