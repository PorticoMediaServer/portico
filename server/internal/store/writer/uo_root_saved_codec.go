package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

// Closed native result, not a public DTO or authority/commit capability. The
// parent captures its complete public wire result through W before cutover.
// Entry order and generated entry identities survive source drift and retries.
type uoRootSavedEntry struct {
	status           uint8 // 1 added, 2 removed, 3 unchanged, 4 not_found
	reference, entry string
}
type uoRootSavedResult struct {
	root, resource                                   string
	kind                                             CommandID
	revision, generation                             int64
	selectionRevision, reclaimedRows, reclaimedBytes int64
	deleted, changed, entriesPresent                 bool
	entries                                          []uoRootSavedEntry
	undoToken                                        string
	undoUntilMS, appliedMS                           int64
}

func uoRootSavedKind(k CommandID) bool {
	switch k {
	case CreateSavedResource, PatchSavedResource, DeleteSavedResource, EditSavedResourceMembers, ReorderSavedResource, SetSavedResourceSharing:
		return true
	}
	return false
}
func uoRootSavedText(s string, cap int, empty bool) bool {
	return (empty || s != "") && len(s) <= cap && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func preflightUORootSaved(v uoRootSavedResult) error {
	if !uoRootSavedKind(v.kind) || len(v.root) != 22 || !uoRootSavedText(v.root, 22, false) || len(v.resource) != 22 || !uoRootSavedText(v.resource, 22, false) || v.revision < 1 || v.generation < 0 || v.selectionRevision < 0 || v.reclaimedRows < 0 || v.reclaimedBytes < 0 || (v.reclaimedRows == 0) != (v.reclaimedBytes == 0) || v.changed && (v.generation == 0 || v.selectionRevision == 0) || v.kind == CreateSavedResource && (!v.changed || v.revision != 1) || v.appliedMS <= 0 || len(v.entries) > 200 || !v.entriesPresent && len(v.entries) != 0 || v.entriesPresent != (v.kind == EditSavedResourceMembers) || v.deleted != (v.kind == DeleteSavedResource) || v.undoUntilMS < 0 || (v.undoToken == "") != (v.undoUntilMS == 0) || v.undoToken != "" && (v.kind != EditSavedResourceMembers || len(v.undoToken) != 22 || !uoRootSavedText(v.undoToken, 22, false) || v.undoUntilMS <= v.appliedMS) {
		return ErrInvalid
	}
	// Prove the whole prospective size before scanning or hashing payload text.
	// Every retained string is bounded; there is no schema-max multiplication
	// standing in for a native stored-payload point preflight.
	d, err := Describe(v.kind)
	if err != nil {
		return err
	}
	remaining := uoRootPayloadCap - (len(canonicalResultCodec) + 2 + 8 + len(d.Name) + 8 + len("root_saved_resource") + 8 + len(v.root) + 16 + 8 + 16 + 32 + 8 + len(v.resource) + 40 + 3 + 8 + 8 + len(v.undoToken) + 16)
	for _, e := range v.entries {
		if len(e.reference) > remaining-17 || len(e.entry) > remaining-17-len(e.reference) {
			return ErrInvalid
		}
		remaining -= 17 + len(e.reference) + len(e.entry)
	}
	for _, e := range v.entries {
		if e.status < 1 || e.status > 4 || !uoRootSavedText(e.reference, 128, false) || !uoRootSavedText(e.entry, 22, true) || e.entry != "" && len(e.entry) != 22 || (e.status == 1 || e.status == 2) && (e.entry == "" || !v.changed) {
			return ErrInvalid
		}
	}
	return nil
}
func writeUORootSaved(e *requestEncoder, i CommandIdentity, q RequestDigest, v uoRootSavedResult) error {
	d, err := Describe(v.kind)
	if err != nil {
		return err
	}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(d.Name)
	e.text("root_saved_resource")
	e.text(v.root)
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	e.text(v.resource)
	e.number(uint64(v.revision))
	e.number(uint64(v.generation))
	e.number(uint64(v.selectionRevision))
	e.number(uint64(v.reclaimedRows))
	e.number(uint64(v.reclaimedBytes))
	e.flag(v.deleted)
	e.flag(v.changed)
	e.flag(v.entriesPresent)
	e.number(uint64(len(v.entries)))
	for _, x := range v.entries {
		e.bytes([]byte{x.status})
		e.text(x.reference)
		e.text(x.entry)
	}
	e.text(v.undoToken)
	e.number(uint64(v.undoUntilMS))
	e.number(uint64(v.appliedMS))
	return e.err
}

// This decoder only projects this one closed W schema. Re-encoding through
// W's dispatcher verifies every prefix, binding and semantic representation.
type uoSavedResultReader struct {
	p   []byte
	bad bool
}

func (r *uoSavedResultReader) take(n int) []byte {
	if n < 0 || n > len(r.p) {
		r.bad = true
		return nil
	}
	p := r.p[:n]
	r.p = r.p[n:]
	return p
}
func (r *uoSavedResultReader) number() int64 {
	p := r.take(8)
	if len(p) != 8 {
		return 0
	}
	n := binary.BigEndian.Uint64(p)
	if n > uint64(^uint64(0)>>1) {
		r.bad = true
		return 0
	}
	return int64(n)
}
func (r *uoSavedResultReader) text(cap int) string {
	n := r.number()
	if r.bad || n < 0 || n > int64(cap) {
		r.bad = true
		return ""
	}
	return string(r.take(int(n)))
}
func (r *uoSavedResultReader) flag() bool {
	p := r.take(1)
	if len(p) != 1 {
		return false
	}
	if p[0] > 1 {
		r.bad = true
	}
	return p[0] == 1
}
func decodeUORootSaved(i CommandIdentity, q RequestDigest, root string, kind CommandID, p []byte, want ResultDigest) (uoRootSavedResult, error) {
	var zero uoRootSavedResult
	if len(p) == 0 || len(p) > uoRootPayloadCap || ResultDigest(sha256.Sum256(p)) != want || !uoRootSavedKind(kind) {
		return zero, ErrResolveRequired
	}
	d, err := Describe(kind)
	if err != nil {
		return zero, ErrResolveRequired
	}
	r := uoSavedResultReader{p: p}
	r.take(len(canonicalResultCodec) + 2 + 8 + len(d.Name) + 8 + len("root_saved_resource") + 8 + len(root) + 16 + 8 + 16 + 32)
	v := uoRootSavedResult{root: root, kind: kind}
	v.resource = r.text(22)
	v.revision = r.number()
	v.generation = r.number()
	v.selectionRevision = r.number()
	v.reclaimedRows = r.number()
	v.reclaimedBytes = r.number()
	v.deleted = r.flag()
	v.changed = r.flag()
	v.entriesPresent = r.flag()
	n := r.number()
	if r.bad || n < 0 || n > 200 {
		return zero, ErrResolveRequired
	}
	for count := int64(0); count < n; count++ {
		b := r.take(1)
		if len(b) != 1 {
			return zero, ErrResolveRequired
		}
		v.entries = append(v.entries, uoRootSavedEntry{status: b[0], reference: r.text(128), entry: r.text(22)})
	}
	v.undoToken = r.text(22)
	v.undoUntilMS = r.number()
	v.appliedMS = r.number()
	if r.bad || len(r.p) != 0 {
		return zero, ErrResolveRequired
	}
	encoded, digest, err := encodeUORootCanonical(i, q, v)
	if err != nil || digest != want || !bytes.Equal(encoded, p) {
		return zero, ErrResolveRequired
	}
	return v, nil
}

func persistUORootSaved(ctx context.Context, turn *commandTurn, lookup uoRootLookup, v uoRootSavedResult) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	return persistUORootCanonical(ctx, turn, lookup, v)
}
func restoreUORootSaved(ctx context.Context, turn *commandTurn, lookup uoRootLookup) (out uoRootSavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	p, d, err := readUORootPayload(ctx, turn, lookup)
	if err != nil {
		return out, err
	}
	q := lookup.reference
	out, err = decodeUORootSaved(q.identity, q.request, q.root, q.kind, p, d)
	if err != nil || int64(len(out.entries)) != lookup.resultCount || out.appliedMS != lookup.appliedMS {
		return uoRootSavedResult{}, ErrResolveRequired
	}
	return out, turn.checkLifetimes(ctx)
}
func acknowledgeUORootSaved(ctx context.Context, turn *commandTurn, lookup uoRootLookup, nowMS int64) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if !uoRootSavedKind(lookup.reference.kind) {
		return 0, ErrInvalid
	}
	if lookup.decision == receiptReplay {
		if _, err := restoreUORootSaved(ctx, turn, lookup); err != nil {
			return 0, err
		}
	}
	return acknowledgeUORootVerified(ctx, turn, lookup, nowMS)
}
