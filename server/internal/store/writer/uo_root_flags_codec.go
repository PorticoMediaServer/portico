package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"unicode/utf8"
)

func persistUORootFlags(ctx context.Context, turn *commandTurn, lookup uoRootLookup, public [16]byte, effect uoPersonalFlagsEffect, appliedMS int64) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	return persistUORootCanonical(ctx, turn, lookup, uoRootFlagsResult{root: lookup.reference.root, public: public, effect: effect, appliedMS: appliedMS})
}

func restoreUORootFlags(ctx context.Context, turn *commandTurn, lookup uoRootLookup) (out uoRootFlagsResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if lookup.reference.kind != SetPersonalFlags {
		return out, ErrInvalid
	}
	payload, digest, err := readUORootPayload(ctx, turn, lookup)
	if err != nil {
		return out, err
	}
	q := lookup.reference
	out, err = decodeUORootFlags(q.identity, q.request, q.root, payload, digest)
	if err != nil || lookup.resultCount != 0 || out.appliedMS != lookup.appliedMS {
		return uoRootFlagsResult{}, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}

func acknowledgeUORootFlags(ctx context.Context, turn *commandTurn, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if lookup.reference.kind != SetPersonalFlags {
		return 0, ErrInvalid
	}
	if lookup.decision == receiptReplay {
		if _, err := restoreUORootFlags(ctx, turn, lookup); err != nil {
			return 0, err
		}
	}
	return acknowledgeUORootVerified(ctx, turn, lookup, nowMS)
}

// Complete scalar effect result for native purpose600. A public wire wrapper
// additionally captures its own complete DTO through W before cutover; this
// value grants no current authority or Applied/COMMIT evidence.
type uoRootFlagsResult struct {
	root      string
	public    [16]byte
	effect    uoPersonalFlagsEffect
	appliedMS int64
}

func preflightUORootFlags(v uoRootFlagsResult) error {
	s, e := v.effect.state, v.effect
	if len(v.root) != 22 || !utf8.ValidString(v.root) || v.public == [16]byte{} || v.appliedMS <= 0 || s.revision < 0 || s.rating.Valid && (s.rating.Int64 < 0 || s.rating.Int64 > 10) || !s.rating.Valid && s.rating.Int64 != 0 || e.viewerRevision < 0 || e.selectionRevision < 0 || e.changed && (s.revision == 0 || e.viewerRevision == 0 || e.selectionRevision == 0) || !e.changed && (e.viewerRevision != 0 || e.selectionRevision != 0) {
		return ErrInvalid
	}
	return nil
}
func writeUORootFlags(e *requestEncoder, i CommandIdentity, q RequestDigest, v uoRootFlagsResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("uo_set_personal_flags")
	e.text("root_flags")
	e.text(v.root)
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	e.bytes(v.public[:])
	e.number(uint64(v.effect.state.revision))
	e.flag(v.effect.state.watchlisted)
	e.flag(v.effect.state.favorite)
	e.flag(v.effect.state.notInterested)
	e.flag(v.effect.state.rating.Valid)
	e.number(uint64(v.effect.state.rating.Int64))
	e.flag(v.effect.changed)
	e.number(uint64(v.effect.viewerRevision))
	e.number(uint64(v.effect.selectionRevision))
	e.number(uint64(v.appliedMS))
	return e.err
}
func decodeUORootFlags(i CommandIdentity, q RequestDigest, root string, p []byte, want ResultDigest) (uoRootFlagsResult, error) {
	var zero uoRootFlagsResult
	if len(p) < 61 || len(p) > uoRootPayloadCap || ResultDigest(sha256.Sum256(p)) != want {
		return zero, ErrResolveRequired
	}
	tail := p[len(p)-61:]
	v := uoRootFlagsResult{root: root}
	copy(v.public[:], tail[:16])
	for _, bit := range []byte{tail[24], tail[25], tail[26], tail[27], tail[36]} {
		if bit > 1 {
			return zero, ErrResolveRequired
		}
	}
	v.effect.state.watchlisted, v.effect.state.favorite, v.effect.state.notInterested, v.effect.state.rating.Valid, v.effect.changed = tail[24] == 1, tail[25] == 1, tail[26] == 1, tail[27] == 1, tail[36] == 1
	for _, field := range []struct {
		offset int
		dst    *int64
	}{{16, &v.effect.state.revision}, {28, &v.effect.state.rating.Int64}, {37, &v.effect.viewerRevision}, {45, &v.effect.selectionRevision}, {53, &v.appliedMS}} {
		n := binary.BigEndian.Uint64(tail[field.offset : field.offset+8])
		if n > uint64(^uint64(0)>>1) {
			return zero, ErrResolveRequired
		}
		*field.dst = int64(n)
	}
	encoded, sum, err := encodeUORootCanonical(i, q, v)
	if err != nil || sum != want || !bytes.Equal(p, encoded) {
		return zero, ErrResolveRequired
	}
	return v, nil
}
