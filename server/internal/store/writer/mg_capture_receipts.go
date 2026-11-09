package writer

import (
	"errors"
	"unicode/utf8"
)

const (
	mgCaptureResultFamily  = "mg_capture"
	mgCaptureResultVariant = "custody"
)

// The complete captured-custody payload is the canonical result encoding of the
// ORIGINAL command: the post-effect claim/lease/allocation/launch/source tuple,
// native captured progress and the row revision/sequence. It is never a current
// DTO, an artifact proof or a physical exit.
func preflightMGCaptureResult(v mgCaptureResult) error {
	if !v.valid() {
		return ErrResolveRequired
	}
	for _, s := range []string{v.id, v.owner, v.source, v.channel, v.programme, v.generation, v.state,
		v.token, v.ownerInstance, v.allocationID, v.allocationToken, v.sourceFence, v.artifactID} {
		if !mgRecordingText(s, false, 256) || !utf8.ValidString(s) {
			return ErrResolveRequired
		}
	}
	return nil
}

func writeMGCaptureResult(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgCaptureResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(mgCaptureResultFamily)
	e.text(mgCaptureResultVariant)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	for _, s := range []string{v.id, v.owner, v.source, v.channel, v.programme, v.generation, v.state,
		v.token, v.ownerInstance, v.allocationID, v.allocationToken, v.sourceFence, v.artifactID} {
		e.text(s)
	}
	for _, n := range []int64{v.claimGeneration, v.cancelGeneration, v.leaseMS, v.allocationGeneration,
		v.sourceRevision, v.revision, v.sequence} {
		e.number(uint64(n))
	}
	e.bytes(v.launch[:])
	for _, n := range []int64{v.capturedStartMS, v.capturedEndMS, v.gapCount, v.producerSucceeded, v.boundaryReached} {
		e.number(uint64(n))
	}
	return e.err
}

func mgCaptureResultPayload(id CommandIdentity, q RequestDigest, v mgCaptureResult) ([]byte, ResultDigest, error) {
	d, err := canonicalResultDigest(id, q, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	b := &mpCodecBuffer{}
	e := requestEncoder{h: b}
	if err = encodeCanonicalResult(&e, id, q, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if b.Len() < 1 || b.Len() > uoRootPayloadCap {
		return nil, ResultDigest{}, ErrCap
	}
	return b.Bytes(), d, nil
}

func decodeMGCaptureResult(id CommandIdentity, q RequestDigest, payload []byte, digest ResultDigest) (out mgCaptureResult, err error) {
	r := &mgRecordingResultReader{b: payload}
	p, err := r.take(len(canonicalResultCodec))
	if err != nil || string(p) != canonicalResultCodec {
		return out, ErrResolveRequired
	}
	p, err = r.take(2)
	if err != nil || p[0] != 0 || p[1] != 1 {
		return out, ErrResolveRequired
	}
	name, err := r.text()
	if err != nil || name != mgCaptureResultFamily {
		return out, ErrResolveRequired
	}
	name, err = r.text()
	if err != nil || name != mgCaptureResultVariant {
		return out, ErrResolveRequired
	}
	p, err = r.take(16)
	if err != nil || string(p) != string(id.Incarnation[:]) {
		return out, ErrResolveRequired
	}
	n, err := r.number()
	if err != nil || n != id.Sequence {
		return out, ErrResolveRequired
	}
	p, err = r.take(16)
	if err != nil || string(p) != string(id.OperationID[:]) {
		return out, ErrResolveRequired
	}
	p, err = r.take(32)
	if err != nil || string(p) != string(q[:]) {
		return out, ErrResolveRequired
	}
	for _, dst := range []*string{&out.id, &out.owner, &out.source, &out.channel, &out.programme,
		&out.generation, &out.state, &out.token, &out.ownerInstance, &out.allocationID,
		&out.allocationToken, &out.sourceFence, &out.artifactID} {
		if *dst, err = r.text(); err != nil {
			return out, err
		}
	}
	for _, dst := range []*int64{&out.claimGeneration, &out.cancelGeneration, &out.leaseMS,
		&out.allocationGeneration, &out.sourceRevision, &out.revision, &out.sequence} {
		if *dst, err = r.number(); err != nil {
			return out, err
		}
	}
	p, err = r.take(16)
	if err != nil {
		return out, err
	}
	copy(out.launch[:], p)
	for _, dst := range []*int64{&out.capturedStartMS, &out.capturedEndMS, &out.gapCount,
		&out.producerSucceeded, &out.boundaryReached} {
		if *dst, err = r.number(); err != nil {
			return out, err
		}
	}
	if r.at != len(payload) {
		return mgCaptureResult{}, ErrResolveRequired
	}
	want, err := canonicalResultDigest(id, q, out)
	if err != nil || want != digest {
		return mgCaptureResult{}, errors.Join(ErrResolveRequired, err)
	}
	if err = preflightMGCaptureResult(out); err != nil {
		return mgCaptureResult{}, err
	}
	again, _, err := mgCaptureResultPayload(id, q, out)
	if err != nil || string(again) != string(payload) {
		return mgCaptureResult{}, ErrResolveRequired
	}
	return out, nil
}
