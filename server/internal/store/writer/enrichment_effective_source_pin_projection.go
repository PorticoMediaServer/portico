package writer

// These decoders consume only the three CLOSED native copy projections after
// parent admission. They do not execute copies, mint proofs or decide authority.
// Actual Go types, NULLs, lengths and scalars must match same-snapshot preflight.
// The private copy entry requires the same-grant reservation created by the
// parent preflight; these pure decoders alone supply no such reservation.

type effectiveEvidenceBodyPin struct {
	evidence         string
	subject          int64
	parser, provider string
	ref              effectiveSourceSealRef
}
type effectivePublicationBodyPin struct {
	publication   string
	entity, count int64
	provider      string
	ref           effectiveSourceSealRef
}

type effectiveSealProofState struct {
	revision, pinCount   int64
	reclaimDueMS         EvidenceInteger
	cleanupAfterSequence int64
}
type effectivePinnedSealReceipt struct {
	saved savedSealReceipt        // immutable facts validated by the EXISTING single codec
	proof effectiveSealProofState // actual mutable state; never folded into saved
}

func effectiveReceiptPinShape(p effectivePinProjection, kind CommandID) error {
	if kind != EvidenceSeal && kind != CreditSourceSeal {
		return effectivePinMalformed()
	}
	for _, column := range []int{savedSealImage, savedSealImageRevision, savedSealDependencyOwner, savedSealDependencyIncarnation, savedSealDependencySequence, savedSealDependencyPublication, savedSealDependencyProvider} {
		if p.cells[column].kind != 0 {
			return effectivePinMalformed()
		}
	}
	for _, slot := range []struct {
		column int
		width  int64
	}{
		{savedSealIncarnation, 16}, {savedSealOperation, 16}, {savedSealRequest, 32},
		{savedSealResultDigest, 32}, {savedSealCommitment, 32},
	} {
		if p.cells[slot.column].kind != 'B' || p.cells[slot.column].octets != slot.width {
			return effectivePinMalformed()
		}
	}
	if p.cells[savedSealObservation].octets != 64 {
		return effectivePinMalformed()
	}
	if kind == EvidenceSeal {
		if p.cells[savedSealKind].octets != int64(len("evidence_seal")) || p.cells[savedSealPublication].kind != 0 || p.cells[savedSealBuild].kind != 'T' || p.cells[savedSealBuildGeneration].kind != 'I' || p.cells[savedSealUpstreamOwner].kind != 0 || p.cells[savedSealUpstreamIncarnation].kind != 0 || p.cells[savedSealUpstreamSequence].kind != 0 {
			return effectivePinMalformed()
		}
	} else {
		if p.cells[savedSealKind].octets != int64(len("credit_source_seal")) || p.cells[savedSealPublication].kind != 'T' || p.cells[savedSealPublication].octets != 32 || p.cells[savedSealBuild].kind != 0 || p.cells[savedSealBuildGeneration].kind != 0 || p.cells[savedSealUpstreamOwner].kind != 'T' || p.cells[savedSealUpstreamIncarnation].kind != 'B' || p.cells[savedSealUpstreamIncarnation].octets != 16 || p.cells[savedSealUpstreamSequence].kind != 'I' {
			return effectivePinMalformed()
		}
	}
	return nil
}

// Fixed numeric preflight prevents nonsensical counters/declared byte ranges
// from reaching payload copies. Complete result semantics remain in the codec.
func effectiveReceiptPinFixedShape(p effectivePinProjection, ref effectiveSourceSealRef) error {
	n := func(column int) int64 { return p.cells[column].integer }
	if n(savedSealSequence) != ref.sequence || n(savedSealAcceptedRevision) <= 0 || n(savedSealAppliedMS) < 0 || n(savedSealEntity) <= 0 ||
		n(savedSealAfterOrdinal) < 0 || n(savedSealResultCount) != 0 || n(savedSealVersion) != 1 || n(savedSealSubject) <= 0 ||
		n(savedSealOwnerGeneration) <= 0 || n(savedSealContentRevision) <= 0 || n(savedSealCanonicalBytes) <= 0 ||
		uint64(n(savedSealCanonicalBytes)) > maxCanonicalRequestBytes || n(savedSealRetainedBytes) <= 0 || n(savedSealRetainedBytes) > maxSealRetainedBytes ||
		n(savedSealAfterOrdinal) != n(savedSealVerifiedRows) {
		return effectivePinMalformed()
	}
	if ref.kind == EvidenceSeal {
		if n(savedSealBuildGeneration) <= 0 || n(savedSealVerifiedRows) < 1 || n(savedSealVerifiedRows) > 1+MaxCastCredits+MaxCrewCredits {
			return effectivePinMalformed()
		}
	} else if ref.kind != CreditSourceSeal || n(savedSealUpstreamSequence) <= 0 || n(savedSealVerifiedRows) < 0 || n(savedSealVerifiedRows) > MaxCastCredits+MaxCrewCredits {
		return effectivePinMalformed()
	}
	// Shape only; native pins/reclaim/cleanup compatibility is parent-owned.
	if n(44) <= 0 || n(45) < 0 || n(46) < 0 || n(47) < 0 {
		return effectivePinMalformed()
	}
	return nil
}

func effectivePinCopyMatches(p effectivePinProjection, layout string, values []any) error {
	if len(values) != len(layout) {
		return effectivePinMalformed()
	}
	for i := range layout {
		expected := p.cells[i]
		switch expected.kind {
		case 0:
			if values[i] != nil {
				return effectivePinMalformed()
			}
		case 'T':
			v, ok := values[i].(string)
			if !ok || int64(len(v)) != expected.octets {
				return effectivePinMalformed()
			}
		case 'B':
			v, ok := values[i].([]byte)
			if !ok || int64(len(v)) != expected.octets {
				return effectivePinMalformed()
			}
		case 'I':
			v, ok := values[i].(int64)
			if !ok || v != expected.integer {
				return effectivePinMalformed()
			}
		default:
			return effectivePinMalformed()
		}
	}
	return nil
}

func decodeEffectiveSourceEvidencePin(pre effectiveEvidencePinLengths, values []any) (effectiveEvidenceBodyPin, error) {
	if err := effectivePinCopyMatches(pre.projection, effectiveEvidencePinLayout, values); err != nil {
		return effectiveEvidenceBodyPin{}, err
	}
	if values[0].(string) != pre.evidence || values[1].(int64) <= 0 || values[4].(int64) != 1 || values[5].(string) == "" || len(values[6].([]byte)) != 16 || values[7].(int64) <= 0 || values[8].(string) != "evidence_seal" {
		return effectiveEvidenceBodyPin{}, effectivePinMalformed()
	}
	out := effectiveEvidenceBodyPin{evidence: values[0].(string), subject: values[1].(int64), parser: values[2].(string), provider: values[3].(string)}
	out.ref = effectiveSourceSealRef{owner: values[5].(string), sequence: values[7].(int64), kind: EvidenceSeal}
	copy(out.ref.incarnation[:], values[6].([]byte))
	return out, nil
}

func decodeEffectiveSourcePublicationPin(pre effectivePublicationPinLengths, values []any) (effectivePublicationBodyPin, error) {
	if err := effectivePinCopyMatches(pre.projection, effectivePublicationPinLayout, values); err != nil {
		return effectivePublicationBodyPin{}, err
	}
	if values[0].(string) != pre.publication || values[1].(int64) <= 0 || values[3].(int64) < 0 || values[3].(int64) > MaxCastCredits+MaxCrewCredits || values[4].(int64) != 1 || values[5].(string) == "" || len(values[6].([]byte)) != 16 || values[7].(int64) <= 0 || values[8].(string) != "credit_source_seal" {
		return effectivePublicationBodyPin{}, effectivePinMalformed()
	}
	out := effectivePublicationBodyPin{publication: values[0].(string), entity: values[1].(int64), provider: values[2].(string), count: values[3].(int64)}
	out.ref = effectiveSourceSealRef{owner: values[5].(string), sequence: values[7].(int64), kind: CreditSourceSeal}
	copy(out.ref.incarnation[:], values[6].([]byte))
	return out, nil
}

func decodeEffectiveSourcePinnedReceipt(pre effectiveReceiptPinLengths, values []any) (effectivePinnedSealReceipt, error) {
	if err := effectiveReceiptPinShape(pre.projection, pre.ref.kind); err != nil {
		return effectivePinnedSealReceipt{}, err
	}
	if err := effectiveReceiptPinFixedShape(pre.projection, pre.ref); err != nil {
		return effectivePinnedSealReceipt{}, err
	}
	if err := effectivePinCopyMatches(pre.projection, effectiveReceiptPinLayout, values); err != nil {
		return effectivePinnedSealReceipt{}, err
	}
	// No reconstruction, alternate result hash or digest implementation here.
	saved, err := decodeSavedSealReceipt(values[:savedSealColumnCount])
	if err != nil {
		return effectivePinnedSealReceipt{}, err
	}
	if saved.facts.ProviderOperationID != pre.ref.owner || saved.identity.command.Incarnation != pre.ref.incarnation || saved.identity.command.Sequence != pre.ref.sequence || saved.kind != pre.ref.kind {
		return effectivePinnedSealReceipt{}, effectivePinMalformed()
	}
	proof := effectiveSealProofState{revision: values[44].(int64), pinCount: values[45].(int64), cleanupAfterSequence: values[47].(int64)}
	if values[46] != nil {
		proof.reclaimDueMS = EvidenceInteger{Value: values[46].(int64), Present: true}
	}
	if proof.revision <= 0 || proof.pinCount < 0 || proof.reclaimDueMS.Value < 0 || proof.cleanupAfterSequence < 0 {
		return effectivePinnedSealReceipt{}, effectivePinMalformed()
	}
	// Actual pinCount=0 and scheduled cleanup are observable native facts, NOT
	// acceptance. Parent must reject or accept lifecycle compatibility itself.
	return effectivePinnedSealReceipt{saved: saved, proof: proof}, nil
}
