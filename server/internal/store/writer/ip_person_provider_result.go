package writer

// Parent adds exactly TWO cases to result_digest.go (owned by W/parent):
// preflightCanonicalResult: case ipPersonSavedResult: return preflightIPPersonResult(v)
// writeCanonicalResult: case ipPersonSavedResult: return writeIPPersonResult(e,identity,request,v)
// Until composed, canonicalResultDigest rejects this type; bodies roll back.
// Never bypass that closed registry with a local result hash implementation.
func preflightIPPersonResult(v ipPersonSavedResult) error {
	r := v.value
	switch v.kind {
	case IPProviderClaim, IPProviderRenew, IPProviderFinish, IPEvidenceBegin, IPEvidenceAppend, IPEvidenceSeal, IPProviderAccept, IPProviderDelta:
	default:
		return ErrInvalid
	}
	if r.revision <= 0 || r.appliedMS < 0 || r.after < 0 || r.count < 0 || r.count > ipPersonMaxChildren+1 || r.after != r.count || !ipPersonText(r.evidenceID, 128, false) || !ipPersonText(r.buildID, 128, false) || !ipPersonText(r.publicationID, 128, false) {
		return ErrInvalid
	}
	if (r.evidenceID == "") != (r.buildID == "") {
		return ErrInvalid
	}
	if v.kind == IPProviderClaim || v.kind == IPProviderRenew || v.kind == IPProviderFinish {
		if r.evidenceID != "" || r.count != 0 {
			return ErrInvalid
		}
	} else if r.evidenceID == "" || r.count <= 0 {
		return ErrInvalid
	}
	if (v.kind == IPProviderAccept || v.kind == IPProviderDelta) != (r.publicationID != "") {
		return ErrInvalid
	}
	if v.kind == IPEvidenceSeal {
		if r.sealAlgorithm != ipPersonManifest || r.sealVersion != 1 || r.sealCommitment == [32]byte{} || r.sealRows != r.count || r.sealBytes <= 0 || r.sealBytes > ipPersonMaxBytes {
			return ErrInvalid
		}
	} else if r.sealAlgorithm != "" || r.sealVersion != 0 || r.sealCommitment != [32]byte{} || r.sealRows != 0 || r.sealBytes != 0 {
		return ErrInvalid
	}
	return nil
}

// Wire: shared result codec/version, LP purpose ip_person_provider, original
// identity/request, uint16 command kind, revision/after/count/appliedMS BE64,
// LP evidence/build/publication; seal-present byte and, only for seal, LP
// algorithm, version BE64, commitment32, rows/bytes BE64. Uses W primitives.
func writeIPPersonResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipPersonSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_person_provider")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	e.bytes([]byte{byte(v.kind >> 8), byte(v.kind)})
	r := v.value
	for _, n := range []int64{r.revision, r.after, r.count, r.appliedMS} {
		e.number(uint64(n))
	}
	e.text(r.evidenceID)
	e.text(r.buildID)
	e.text(r.publicationID)
	e.flag(v.kind == IPEvidenceSeal)
	if v.kind == IPEvidenceSeal {
		e.text(r.sealAlgorithm)
		e.number(uint64(r.sealVersion))
		e.bytes(r.sealCommitment[:])
		e.number(uint64(r.sealRows))
		e.number(uint64(r.sealBytes))
	}
	return e.err
}
