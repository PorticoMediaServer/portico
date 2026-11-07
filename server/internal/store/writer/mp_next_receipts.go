package writer

import (
	"crypto/sha256"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type mpNextBirthEffect struct {
	workerIncarnation [16]byte
	result            mpQueueEffect
	prepared          mpNextPrepared
}

// Exact original session/presentation scalars supplied by the parent's core
// recipe. No current session or physical exit lookup may fill missing fields.
type mpNextReceiptHeader struct {
	state, media, presentationURL                                string
	sessionGeneration, sessionRevision, mediaGeneration, expires int64
	physicalPending                                              bool
}
type mpNextSavedResult struct {
	token         string
	identity      CommandIdentity
	requestDigest RequestDigest
	kind          CommandID
	appliedMS     int64
	digest        [32]byte
	effect        mpQueueEffect
	header        mpNextReceiptHeader
	retireMedia   []mpReceiptRetireMedia
}

func mpNextResultDefinitions() []stmt.Definition { return mpNextWorkerPayloadDefinitions() }
func mpNextRootPurpose(kind CommandID) bool {
	return kind == mpCommitNext || kind == mpCancelNext || kind == mpNextPhysicalExit
}
func mpNextSavedCommitment(v mpNextSavedResult) ([32]byte, error) {
	var zero [32]byte
	e, h := v.effect, v.header
	if !mpNextRootPurpose(v.kind) || !mpReceiptIdentityValid(v.identity, v.requestDigest) || v.appliedMS < 0 || v.token != e.token || !mpID(v.token) || !e.done || e.cursor != 0 || e.count != 0 || !mpRetireResultsValid(v.retireMedia) || h.state != "prepared" && h.state != "committed" && h.state != "canceled" || h.sessionGeneration < 1 || h.sessionRevision < 1 || h.mediaGeneration < 1 || h.expires < 1 || !mpID(h.media) || !mpID(e.session) {
		return zero, ErrInvalid
	}
	effect, err := mpResultBytes(v.kind, e)
	if err != nil {
		return zero, err
	}
	out := mpReceiptEncoder{bytes: []byte("MP-F-next-receipt-v1\x00")}
	mpEncodeReceiptIdentity(&out, v.token, v.identity, v.requestDigest, v.kind, v.appliedMS)
	out.bytes = append(out.bytes, effect...)
	out.integer(1 + int64(len(v.retireMedia)))
	out.text(h.state, 9)
	out.text(h.media, 128)
	out.text(h.presentationURL, 16384)
	out.integer(h.sessionGeneration)
	out.integer(h.sessionRevision)
	out.integer(h.mediaGeneration)
	out.integer(h.expires)
	out.boolean(h.physicalPending)
	mpEncodeRetireResults(&out, v.retireMedia)
	if out.err != nil {
		return zero, out.err
	}
	return sha256.Sum256(out.bytes), nil
}
