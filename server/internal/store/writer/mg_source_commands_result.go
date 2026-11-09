package writer

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
)

// One complete saved source-command result. It is the exact post-command row
// (or the exact row returned by the original enable/access/retire), frozen at
// the original applied time. It is never rebuilt from live source on replay.
const (
	mgSourceCommandResultVersion = 1
	mgSourceCommandResultCap     = 4096
	mgSourceCommandResultMarker  = "mg_source_result"
	mgSourceCommandMaxText       = 4096
)

type mgSourceCommandResult struct {
	kind      CommandID
	source    mgSourceFacts
	appliedMS int64
}

func (mgSourceCommandResult) String() string   { return "mgSourceCommandResult{redacted}" }
func (mgSourceCommandResult) GoString() string { return "mgSourceCommandResult{redacted}" }
func (v mgSourceCommandResult) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("mgSourceCommandResult{redacted}"))
}

func mgSourceCommandKind(kind CommandID) bool {
	switch kind {
	case MGConfigureTuner, MGSetSourceEnabled, MGRetireSource, MGSetSourceViewerAccess:
		return true
	}
	return false
}

// Exact field rules. A saved row is the original native facts, so every field
// is bounded here instead of trusting a decoder or a later check.
func preflightMGSourceCommandResult(v mgSourceCommandResult) error {
	if !mgSourceCommandKind(v.kind) || v.appliedMS < 0 {
		return invalid("mg_source_command_result")
	}
	s := v.source
	if !mpWorkerID(s.id) || !mgSourceText(s.name, true, 256) || !mgSourceText(s.generation, true, 256) ||
		!mgSourceText(s.published, false, 128) || (s.state != "active" && s.state != "disabled") ||
		(s.viewerAccess != "owner-only" && s.viewerAccess != "server-members") ||
		s.revision <= 0 || s.tuners < 0 || s.tuners > 256 || s.physical < 0 || s.physical > 256 ||
		s.ownerLimit < 0 || s.ownerLimit > 256 || (s.removed != 0 && s.removed != 1) {
		return invalid("mg_source_command_result")
	}
	if s.removed == 1 {
		if !s.removalRequest.Valid || !mgSourceText(s.removalRequest.String, true, 256) ||
			!s.removalRevision.Valid || s.removalRevision.Int64 != s.revision ||
			!s.removedAt.Valid || !mgSourceText(s.removedAt.String, true, 128) {
			return invalid("mg_source_command_result_removal")
		}
	} else if s.removalRequest.Valid || s.removalRevision.Valid || s.removedAt.Valid {
		return invalid("mg_source_command_result_removal")
	}
	switch v.kind {
	case MGConfigureTuner, MGSetSourceEnabled, MGSetSourceViewerAccess:
		if s.removed != 0 {
			return invalid("mg_source_command_result_removed")
		}
	case MGRetireSource:
		if s.removed != 1 || s.state != "disabled" {
			return invalid("mg_source_command_result_retire")
		}
	}
	total := len(s.id) + len(s.name) + len(s.state) + len(s.generation) + len(s.published) + len(s.viewerAccess) +
		len(s.removalRequest.String) + len(s.removedAt.String)
	return checkMGSourceCommandCap(total)
}

func checkMGSourceCommandCap(n int) error {
	if n < 0 || n > mgSourceCommandResultCap {
		return &Refusal{Code: RefusalCapacity}
	}
	return nil
}

func writeMGSourceCommandResult(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgSourceCommandResult) error {
	s := v.source
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, byte(mgSourceCommandResultVersion)})
	e.text(mgSourceCommandResultMarker)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	e.number(uint64(v.kind))
	e.text(s.id)
	e.text(s.name)
	e.text(s.state)
	e.text(s.generation)
	e.text(s.published)
	e.text(s.viewerAccess)
	e.number(uint64(s.revision))
	e.number(uint64(s.tuners))
	e.number(uint64(s.physical))
	e.number(uint64(s.ownerLimit))
	e.number(uint64(s.removed))
	e.text(s.removalRequest.String)
	e.number(uint64(s.removalRevision.Int64))
	e.text(s.removedAt.String)
	e.number(uint64(v.appliedMS))
	return e.err
}

func mgSourceCommandPayload(id CommandIdentity, q RequestDigest, v mgSourceCommandResult) ([]byte, ResultDigest, error) {
	d, err := canonicalResultDigest(id, q, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	b := &mpCodecBuffer{}
	e := requestEncoder{h: b}
	if err = encodeCanonicalResult(&e, id, q, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if b.Len() > mgSourceCommandResultCap {
		return nil, ResultDigest{}, &Refusal{Code: RefusalCapacity}
	}
	return b.Bytes(), d, nil
}

// Bounded closed decoder with an exact W re-encoding check: identity, request,
// command kind, native fields, nullable removal intent and applied time are
// all covered. There is no JSON or generic result registry.
func decodeMGSourceCommandResult(id CommandIdentity, q RequestDigest, p []byte, d ResultDigest) (out mgSourceCommandResult, err error) {
	if len(p) == 0 || len(p) > mgSourceCommandResultCap {
		return out, ErrResolveRequired
	}
	r := bytes.NewReader(p)
	prefix := len(canonicalResultCodec) + 2 + 8 + len(mgSourceCommandResultMarker) + 16 + 8 + 16 + 32
	if _, err = r.Seek(int64(prefix), io.SeekStart); err != nil {
		return out, ErrResolveRequired
	}
	var kind uint64
	if binary.Read(r, binary.BigEndian, &kind) != nil {
		return out, ErrResolveRequired
	}
	out.kind = CommandID(kind)
	text := func() (string, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > mgSourceCommandMaxText || n > uint64(r.Len()) {
			return "", ErrResolveRequired
		}
		b := make([]byte, int(n))
		_, e := io.ReadFull(r, b)
		return string(b), e
	}
	number := func() (int64, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > uint64(^uint64(0)>>1) {
			return 0, ErrResolveRequired
		}
		return int64(n), nil
	}
	s := &out.source
	if s.id, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if s.name, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if s.state, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if s.generation, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if s.published, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if s.viewerAccess, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	for _, dst := range []*int64{&s.revision, &s.tuners, &s.physical, &s.ownerLimit, &s.removed} {
		if *dst, err = number(); err != nil {
			return mgSourceCommandResult{}, ErrResolveRequired
		}
	}
	var removalRequest, removedAt string
	var removalRevision int64
	if removalRequest, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if removalRevision, err = number(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if removedAt, err = text(); err != nil {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if out.appliedMS, err = number(); err != nil || r.Len() != 0 {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	if s.removed == 1 {
		s.removalRequest = sql.NullString{String: removalRequest, Valid: true}
		s.removalRevision = sql.NullInt64{Int64: removalRevision, Valid: true}
		s.removedAt = sql.NullString{String: removedAt, Valid: true}
	} else if removalRequest != "" || removalRevision != 0 || removedAt != "" {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	expected, actual, e := mgSourceCommandPayload(id, q, out)
	if e != nil || actual != d || !bytes.Equal(expected, p) {
		return mgSourceCommandResult{}, ErrResolveRequired
	}
	return out, nil
}
