package writer

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// MP-09 seam. MP owns the actual logical session row, its independent physical
// session/incarnation, source/queue/device permission and the first-response
// disclosure window. UO never reselects a session, reissues a token/URL/expiry,
// promotes a 703 transfer, or reconstructs a current view from mutable rows.
// This narrow input is the only birth evidence UO-24 consumes.
type uoBirthSessionSource struct {
	root, session, physical, device, startKey, startBody string
	logical, physicalIncarnation                         [16]byte
	physicalGeneration, createdMS, deadlineMS            int64
	tokenExpiresMS, appliedMS                            int64
}

func (uoBirthSessionSource) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoBirthSessionSource")
}

// Complete original 710 result. The public SessionView wire is sealed from the
// EXISTING playbackv1 type before any effect, so retry returns that exact first
// response after the logical or physical row is mutated or deleted. It is not a
// second session DTO, a fresh presentation, or an opaque caller JSON blob: the
// sole codec only accepts bytes that round-trip through the complete existing
// typed SessionView.
type uoBirthSessionResult struct {
	source uoBirthSessionSource
	wire   []byte
}

func (uoBirthSessionResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoBirthSessionResult")
}

// The result holds a slice, so the commit check compares the closed header and
// the exact sealed wire rather than relying on struct identity.
func (v uoBirthSessionResult) equal(other uoBirthSessionResult) bool {
	return v.source == other.source && bytes.Equal(v.wire, other.wire)
}

// Seal the complete existing session view once, before effects. The writer
// retains the product's own wire; it never re-renders the view from current
// source, queue, presentation or observation state.
func sealUOBirthSessionView(view playbackv1.SessionView) ([]byte, error) {
	wire, err := json.Marshal(view)
	if err != nil || len(wire) == 0 || len(wire) > uoRootPayloadCap {
		return nil, ErrInvalid
	}
	return wire, nil
}

// Closed decode: the wire must round-trip through the complete existing typed
// view byte-for-byte. Unknown fields, duplicate members, or any shape the
// product cannot represent are refused instead of being retained opaquely.
func decodeUOBirthSessionView(wire []byte) (playbackv1.SessionView, error) {
	var view playbackv1.SessionView
	if len(wire) == 0 || len(wire) > uoRootPayloadCap {
		return view, ErrResolveRequired
	}
	if err := json.Unmarshal(wire, &view); err != nil {
		return view, ErrResolveRequired
	}
	again, err := json.Marshal(view)
	if err != nil || !bytes.Equal(again, wire) {
		return view, ErrResolveRequired
	}
	return view, nil
}

func uoBirthSessionLeaseMS(view playbackv1.SessionView) (int64, bool) {
	t, err := time.Parse(time.RFC3339Nano, view.Lease.ExpiresAt)
	if err != nil || t.UnixMilli() <= 0 {
		return 0, false
	}
	return t.UnixMilli(), true
}

// The exact born logical/physical facts and the sealed original must agree
// before any effect. A deadline window is the MP-09 disclosure window, bounded
// by the current session token and auth horizon in the native check.
func preflightUOBirthSession(v uoBirthSessionResult) error {
	s := v.source
	if len(s.root) != 22 || s.logical == [16]byte{} || s.physicalIncarnation == [16]byte{} || s.logical == s.physicalIncarnation || s.physicalGeneration <= 0 || !usHex(s.startKey, 64) || !usHex(s.startBody, 64) || s.createdMS <= 0 || s.deadlineMS <= s.createdMS || s.tokenExpiresMS < s.deadlineMS || s.appliedMS < s.createdMS || s.appliedMS >= s.deadlineMS {
		return ErrInvalid
	}
	for _, text := range []string{s.session, s.physical, s.device} {
		if len(text) == 0 || len(text) > 256 || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return ErrInvalid
		}
	}
	if len(v.wire) == 0 || len(v.wire) > uoRootPayloadCap {
		return ErrInvalid
	}
	view, err := decodeUOBirthSessionView(v.wire)
	if err != nil || view.ID != s.session || view.Revision != "1" || view.Lease.ReportEveryMs < 1 || view.Presentation.Generation < 0 {
		return ErrInvalid
	}
	if _, ok := uoBirthSessionLeaseMS(view); !ok {
		return ErrInvalid
	}
	return nil
}

func writeUOBirthSession(e *requestEncoder, id CommandIdentity, q RequestDigest, v uoBirthSessionResult) error {
	s := v.source
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_session_start")
	e.text("root_birth_session_view")
	e.text(s.root)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	for _, text := range []string{s.session, s.physical, s.device, s.startKey, s.startBody} {
		e.text(text)
	}
	e.bytes(s.logical[:])
	e.bytes(s.physicalIncarnation[:])
	for _, n := range []int64{s.physicalGeneration, s.createdMS, s.deadlineMS, s.tokenExpiresMS, s.appliedMS} {
		e.number(uint64(n))
	}
	e.number(uint64(len(v.wire)))
	e.bytes(v.wire)
	return e.err
}

// Parse only this closed tail, then verify ALL prefix/identity/root/request and
// the complete retained wire by re-encoding through W's sole result dispatcher.
func decodeUOBirthSession(id CommandIdentity, q RequestDigest, root string, p []byte, want ResultDigest) (out uoBirthSessionResult, err error) {
	if len(p) == 0 || len(p) > uoRootPayloadCap {
		return out, ErrResolveRequired
	}
	r := bytes.NewReader(p)
	prefix := make([]byte, len(canonicalResultCodec)+2)
	if _, err = io.ReadFull(r, prefix); err != nil || !bytes.Equal(prefix, append([]byte(canonicalResultCodec), 0, 1)) {
		return out, ErrResolveRequired
	}
	text := func() (string, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > 256 || n > uint64(r.Len()) {
			return "", ErrResolveRequired
		}
		b := make([]byte, int(n))
		if _, err = io.ReadFull(r, b); err != nil {
			return "", ErrResolveRequired
		}
		return string(b), nil
	}
	name, err := text()
	if err != nil || name != "mp_session_start" {
		return out, ErrResolveRequired
	}
	tag, err := text()
	if err != nil || tag != "root_birth_session_view" {
		return out, ErrResolveRequired
	}
	gotRoot, err := text()
	if err != nil || gotRoot != root {
		return out, ErrResolveRequired
	}
	var incarnation, operation [16]byte
	var sequence uint64
	var request [32]byte
	if _, err = io.ReadFull(r, incarnation[:]); err != nil {
		return out, ErrResolveRequired
	}
	if binary.Read(r, binary.BigEndian, &sequence) != nil {
		return out, ErrResolveRequired
	}
	if _, err = io.ReadFull(r, operation[:]); err != nil {
		return out, ErrResolveRequired
	}
	if _, err = io.ReadFull(r, request[:]); err != nil {
		return out, ErrResolveRequired
	}
	if incarnation != id.Incarnation || operation != id.OperationID || sequence != uint64(id.Sequence) || request != [32]byte(q) {
		return out, ErrResolveRequired
	}
	out.source.root = root
	for _, dst := range []*string{&out.source.session, &out.source.physical, &out.source.device, &out.source.startKey, &out.source.startBody} {
		if *dst, err = text(); err != nil {
			return uoBirthSessionResult{}, ErrResolveRequired
		}
	}
	if _, err = io.ReadFull(r, out.source.logical[:]); err != nil {
		return out, ErrResolveRequired
	}
	if _, err = io.ReadFull(r, out.source.physicalIncarnation[:]); err != nil {
		return out, ErrResolveRequired
	}
	number := func() (int64, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > math.MaxInt64 {
			return 0, ErrResolveRequired
		}
		return int64(n), nil
	}
	for _, dst := range []*int64{&out.source.physicalGeneration, &out.source.createdMS, &out.source.deadlineMS, &out.source.tokenExpiresMS, &out.source.appliedMS} {
		if *dst, err = number(); err != nil {
			return uoBirthSessionResult{}, ErrResolveRequired
		}
	}
	var wireBytes uint64
	if binary.Read(r, binary.BigEndian, &wireBytes) != nil || wireBytes < 1 || wireBytes > uoRootPayloadCap || wireBytes != uint64(r.Len()) {
		return uoBirthSessionResult{}, ErrResolveRequired
	}
	out.wire = make([]byte, int(wireBytes))
	if _, err = io.ReadFull(r, out.wire); err != nil || r.Len() != 0 {
		return uoBirthSessionResult{}, ErrResolveRequired
	}
	encoded, digest, err := encodeUORootCanonical(id, q, out)
	if err != nil || digest != want || !bytes.Equal(encoded, p) {
		return uoBirthSessionResult{}, ErrResolveRequired
	}
	return out, nil
}
