package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Typed original value projection. The caller supplies the ORIGINAL full header in
// the same fresh turn. No default, current-row lookup, or scalar effect alone
// can reconstruct a public DTO or mint receipt authority.
type mpQueueReceiptHeader struct {
	repeat, retirement                                        string
	shuffle, durationKnown                                    bool
	seed, domain, first, total, remaining, automatic, updated int64
	duration                                                  sql.NullInt64
}
type mpQueueReceiptSegment struct {
	id, snapshot, kind, label, sourceRevision, state string
	count                                            int64
}

// This immutable value is handed to W, not stored as a result child.
// ACK/reclaim of this copy must not release the physical root/stop obligation.
type mpReceiptRetireMedia struct {
	media, requestID string
	generation       int64
	pending          bool
}
type mpQueueSavedResult struct {
	kind          CommandID
	identity      CommandIdentity
	requestDigest RequestDigest
	queue         string
	appliedMS     int64
	effect        mpQueueEffect
	header        mpQueueReceiptHeader
	segments      []mpQueueReceiptSegment
	retireMedia   []mpReceiptRetireMedia
	digest        [32]byte
}

func mpQueueResultDefinitions() []stmt.Definition { return mpQueueWorkerPayloadDefinitions() }

// Pure typed value commitment used for validation tests, never a persistence
// codec. W alone supplies the saved payload and digest.
type mpReceiptEncoder struct {
	bytes []byte
	err   error
}

func (e *mpReceiptEncoder) text(s string, max int) {
	if len(s) > max || !utf8.ValidString(s) {
		e.err = ErrInvalid
		return
	}
	e.integer(int64(len(s)))
	e.bytes = append(e.bytes, s...)
}
func (e *mpReceiptEncoder) integer(n int64) {
	e.bytes = binary.BigEndian.AppendUint64(e.bytes, uint64(n))
}
func (e *mpReceiptEncoder) boolean(b bool) { e.integer(mpBool(b)) }
func (e *mpReceiptEncoder) optional(n sql.NullInt64) {
	e.boolean(n.Valid)
	if n.Valid {
		e.integer(n.Int64)
	}
}
func mpReceiptIdentityValid(identity CommandIdentity, request RequestDigest) bool {
	return identity.Sequence > 0 && identity.Incarnation != [16]byte{} && identity.OperationID != [16]byte{} && request != RequestDigest{}
}
func mpEncodeReceiptIdentity(e *mpReceiptEncoder, owner string, identity CommandIdentity, request RequestDigest, kind CommandID, at int64) {
	e.text(owner, 128)
	e.bytes = append(e.bytes, identity.Incarnation[:]...)
	e.integer(identity.Sequence)
	e.bytes = append(e.bytes, identity.OperationID[:]...)
	e.bytes = append(e.bytes, request[:]...)
	e.integer(int64(kind))
	e.integer(1)
	e.integer(at)
}
func mpRetireResultsValid(values []mpReceiptRetireMedia) bool {
	if len(values) > 2 {
		return false
	}
	for i, v := range values {
		if !mpID(v.media) || v.generation < 1 || v.requestID == "" || len(v.requestID) > 512 || !utf8.ValidString(v.requestID) {
			return false
		}
		for _, p := range values[:i] {
			if p.media == v.media && p.generation == v.generation {
				return false
			}
		}
	}
	return true
}
func mpEncodeRetireResults(e *mpReceiptEncoder, values []mpReceiptRetireMedia) {
	e.integer(int64(len(values)))
	for i, v := range values {
		e.integer(int64(i))
		e.text(v.media, 128)
		e.integer(v.generation)
		e.text(v.requestID, 512)
		e.boolean(v.pending)
	}
}

func mpQueueSavedCommitment(v mpQueueSavedResult) ([32]byte, error) {
	var zero [32]byte
	e, h := v.effect, v.header
	if !mpQueueRootPurpose(v.kind) || !mpReceiptIdentityValid(v.identity, v.requestDigest) || v.appliedMS < 0 || v.queue != e.queue || e.token != "" || len(v.segments) > 40 || !mpRetireResultsValid(v.retireMedia) {
		return zero, ErrInvalid
	}
	if h.repeat != "off" && h.repeat != "one" && h.repeat != "all" || h.retirement != "live" && h.retirement != "draining" && h.retirement != "retired" || h.seed < 0 || h.domain < 0 || h.first < 0 || h.total < 0 || h.remaining < 0 || h.remaining > h.total || h.automatic < 0 || h.updated < 0 || h.duration.Valid && h.duration.Int64 < 0 || h.durationKnown && !h.duration.Valid {
		return zero, ErrInvalid
	}
	effect, err := mpResultBytes(v.kind, e)
	if err != nil {
		return zero, err
	}
	out := mpReceiptEncoder{bytes: []byte("MP-F-queue-receipt-v1\x00")}
	mpEncodeReceiptIdentity(&out, v.queue, v.identity, v.requestDigest, v.kind, v.appliedMS)
	out.bytes = append(out.bytes, effect...)
	out.integer(1 + int64(len(v.segments)+len(v.retireMedia)))
	out.text(h.repeat, 3)
	out.text(h.retirement, 8)
	out.boolean(h.shuffle)
	out.integer(h.seed)
	out.integer(h.domain)
	out.integer(h.first)
	out.integer(h.total)
	out.integer(h.remaining)
	out.integer(h.automatic)
	out.integer(h.updated)
	out.optional(h.duration)
	out.boolean(h.durationKnown)
	out.integer(int64(len(v.segments)))
	for i, s := range v.segments {
		if !mpID(s.id) || !mpSnapshotID(s.snapshot) || s.count < 0 {
			return zero, ErrInvalid
		}
		out.integer(int64(i))
		out.text(s.id, 128)
		out.text(s.snapshot, 128)
		out.text(s.kind, 128)
		out.text(s.label, 1024)
		out.text(s.sourceRevision, 256)
		out.integer(s.count)
		out.text(s.state, 32)
	}
	mpEncodeRetireResults(&out, v.retireMedia)
	if out.err != nil {
		return zero, out.err
	}
	return sha256.Sum256(out.bytes), nil
}

// Pure effect-only commitment remains a component test utility, never the F
// receipt digest. Only W encodes and verifies the persisted payload.
func mpQueueEffectCommitment(kind CommandID, e mpQueueEffect, appliedMS int64) ([32]byte, error) {
	var zero [32]byte
	b, err := mpResultBytes(kind, e)
	if err != nil || appliedMS < 0 {
		if err == nil {
			err = ErrInvalid
		}
		return zero, err
	}
	b = binary.BigEndian.AppendUint64(b, uint64(appliedMS))
	return sha256.Sum256(b), nil
}
func mpQueueRootPurpose(kind CommandID) bool {
	switch kind {
	case mpQueueStage, mpQueueSeal, mpQueueEdit, mpQueueSettings, mpQueueRetire, mpQueueSave, mpQueueCleanup, mpQueueSnapshotBegin, mpQueueAdvance:
		return true
	}
	return false
}
