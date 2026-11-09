package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
)

// Descriptive bulk is ENTITY-rooted and remains private preparation. Command
// 243 births ONE repair-cascade owner with a complete immutable header and
// captures the ORIGINAL field/target cohort in the same transaction; it never
// mutates a target and never re-reads an uncaptured remainder. Page apply and
// the truthful terminal belong to later commands. The operation id is derived
// from the command identity, never a caller-supplied key.
const (
	ipEditBulkManifest = "portico-ip-descriptive-input-sha256-v1"
	ipEditBulkIntent   = "descriptive-bulk-v1"

	ipEditBulkTargetBytes = 40 // exact retained logical bytes per target
	ipEditBulkFieldPad    = 32 // fixed per-field manifest overhead
	ipEditBulkMaxFields   = 11 // loose nested cap; the closed field set is smaller
	ipEditBulkMaxTargets  = 4096
	ipEditBulkActorMax    = 4096
	ipEditBulkBaseMax     = 128
)

// ipEditBulkField is one normalized requested field. Only the finite bulk-safe
// text fields below are admitted; item-specific title/description edits keep
// their own single-owner command.
type ipEditBulkField struct {
	Field fieldmeta.TextField
	Edit  fieldmeta.TextEdit
}

type ipEditBulkTarget struct {
	Ordinal, EntityID int64
	Incarnation       [16]byte
	Revision          int64
}

// ipEditBulkRoot is the FULL declared birth header. ExpectedCount/ExpectedBytes
// and Manifest are original request inputs; preparation recomputes them and
// refuses any mismatch rather than trusting a partial declaration.
type ipEditBulkRoot struct {
	Operation, Kind, Library, Actor, BaseRevision string
	LibraryID                                     int64
	RootEntity, ExpectedCount, ExpectedBytes      int64
	Manifest                                      [32]byte
	Fields                                        []ipEditBulkField
}

type ipEditBulkRequest struct {
	Kind CommandID
	Root ipEditBulkRoot
	// Revision is the expected cascade job revision; birth requires zero.
	Revision int64
	Targets  []ipEditBulkTarget
}

type ipEditBulkCommand struct {
	identity  CommandIdentity
	request   ipEditBulkRequest
	digest    RequestDigest
	seed      [32]byte
	manifest  [32]byte
	fieldByte int64
	valid     bool
}

// ipEditBulkResult is the COMPLETE immutable saved birth/capture result. It
// carries counts, the chained manifest and one accepted revision only; the
// original field/target rows remain the input proof.
type ipEditBulkResult struct {
	OwnerID                          string
	Revision, AfterOrdinal, Captured int64
	Processed, Failed, AppliedMS     int64
	Complete                         bool
	Manifest                         [32]byte
}

type ipEditBulkSavedResult struct {
	Kind  CommandID
	Value ipEditBulkResult
}

type ipEditBulkApplied struct {
	value  ipEditBulkResult
	digest ResultDigest
}

type ipEditBulkCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEditBulkResult
	Digest  ResultDigest
}

func (ipEditBulkCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEditBulkCommand")
}

func (ipEditBulkCommandResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEditBulkCommandResult")
}

// ipEditBulkOperationID is the ONE deterministic owner id for a command
// identity. The caller cannot choose or recycle a repair-cascade primary key.
func ipEditBulkOperationID(operation [16]byte) string {
	return hex.EncodeToString(operation[:])
}

func ipEditBulkFieldAction(e fieldmeta.TextEdit) string {
	switch e.Action {
	case fieldmeta.SetText:
		return "set"
	case fieldmeta.LockText:
		return "lock"
	case fieldmeta.UnlockText:
		return "unlock"
	case fieldmeta.RestoreAutomatic:
		return "restore"
	}
	return ""
}

// ipEditBulkFieldAllowed is the closed bulk-able descriptive set. These fields
// apply across a same-kind entity cohort; identity-bearing and item-specific
// fields stay in the typed single-owner edit.
func ipEditBulkFieldAllowed(field fieldmeta.TextField) bool {
	switch field {
	case fieldmeta.MetadataLanguage, fieldmeta.Studio, fieldmeta.Network, fieldmeta.Country:
		return true
	}
	return false
}

func ipEditBulkValidText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value)
}

// ipEditBulkCanonicalRevision accepts only the canonical decimal form of a
// positive entity revision, so base_revision is one exact retained text fact.
func ipEditBulkCanonicalRevision(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != value {
		return 0, ErrInvalid
	}
	return n, nil
}

// newIPEditBulkCommand freezes the complete original request under W
// preparation custody, before any hold and without SQL.
func newIPEditBulkCommand(identity CommandIdentity, in ipEditBulkRequest) (*ipEditBulkCommand, error) {
	if !ipTextEditIdentityValid(identity) || in.Kind != IPEditBulkBegin || in.Revision != 0 {
		return nil, ErrInvalid
	}
	r := in.Root
	if r.Operation != ipEditBulkOperationID(identity.OperationID) ||
		!ipTextEditEntityKind(r.Kind) || !ipEditBulkValidText(r.Kind, 64) ||
		!ipEditBulkValidText(r.Library, 512) || r.LibraryID <= 0 ||
		!ipEditBulkValidText(r.Actor, ipEditBulkActorMax) ||
		!ipEditBulkValidText(r.BaseRevision, ipEditBulkBaseMax) ||
		r.RootEntity <= 0 || r.ExpectedCount <= 0 || r.ExpectedCount > ipEditBulkMaxTargets ||
		r.ExpectedBytes <= 0 || len(r.Fields) == 0 || len(r.Fields) > ipEditBulkMaxFields ||
		len(in.Targets) != int(r.ExpectedCount) || r.Manifest == [32]byte{} {
		return nil, ErrInvalid
	}
	base, err := ipEditBulkCanonicalRevision(r.BaseRevision)
	if err != nil || base <= 0 {
		return nil, ErrInvalid
	}
	var fieldByte int64
	last := ""
	for _, f := range r.Fields {
		if string(f.Field) <= last || !ipEditBulkFieldAllowed(f.Field) {
			return nil, ErrInvalid
		}
		last = string(f.Field)
		name := ipEditBulkFieldAction(f.Edit)
		if name == "" || f.Edit.Lock > fieldmeta.ForceUnlocked ||
			(f.Edit.Action != fieldmeta.SetText && (f.Edit.Value != "" || f.Edit.Lock != fieldmeta.KeepDefaultLock)) {
			return nil, ErrInvalid
		}
		if _, err = fieldmeta.PrepareTextEdit(f.Field, fieldmeta.TextState{Effective: fieldmeta.Text{Present: true}}, f.Edit); err != nil {
			return nil, ErrInvalid
		}
		value := ""
		if f.Edit.Action == fieldmeta.SetText {
			value = f.Edit.Value
		}
		fieldByte += ipEditBulkFieldPad + int64(len(f.Field)) + int64(len(value))
	}
	if fieldByte > math.MaxInt64-r.ExpectedCount*ipEditBulkTargetBytes ||
		r.ExpectedBytes != fieldByte+r.ExpectedCount*ipEditBulkTargetBytes {
		return nil, ErrInvalid
	}
	seen := make(map[int64]bool, len(in.Targets))
	for j, t := range in.Targets {
		if t.Ordinal != int64(j) || t.EntityID <= 0 || t.Incarnation == [16]byte{} || t.Revision <= 0 || t.Revision == math.MaxInt64 || seen[t.EntityID] {
			return nil, ErrInvalid
		}
		seen[t.EntityID] = true
	}
	// Entity-rooted means the declared root is part of its own immutable cohort
	// at the declared base revision; a bulk operation never edits unseen rows.
	if !seen[r.RootEntity] {
		return nil, ErrInvalid
	}
	for _, t := range in.Targets {
		if t.EntityID == r.RootEntity && t.Revision != base {
			return nil, ErrInvalid
		}
	}
	seed := ipEditBulkSeed(r)
	manifest := seed
	for _, t := range in.Targets {
		manifest = ipEditBulkChain(manifest, t)
	}
	if manifest != r.Manifest {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(in.Kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	// Freeze every retained string and slice even for a saved-result replay.
	r.Operation = strings.Clone(r.Operation)
	r.Kind = strings.Clone(r.Kind)
	r.Library = strings.Clone(r.Library)
	r.Actor = strings.Clone(r.Actor)
	r.BaseRevision = strings.Clone(r.BaseRevision)
	r.Fields = append([]ipEditBulkField(nil), r.Fields...)
	for j := range r.Fields {
		r.Fields[j].Field = fieldmeta.TextField(strings.Clone(string(r.Fields[j].Field)))
		r.Fields[j].Edit.Value = strings.Clone(r.Fields[j].Edit.Value)
	}
	in.Root = r
	in.Targets = append([]ipEditBulkTarget(nil), in.Targets...)
	return &ipEditBulkCommand{identity: identity, request: in, digest: digest, seed: seed, manifest: manifest, fieldByte: fieldByte, valid: true}, nil
}

func ipBulkPut(h hash.Hash, n int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	_, _ = h.Write(b[:])
}

func ipBulkText(h hash.Hash, s string) {
	ipBulkPut(h, int64(len(s)))
	_, _ = h.Write([]byte(s))
}

// Manifest v1 (exact draft domain, byte-identical): seed =
// SHA256(domain NUL, LP operation/kind/library/actor/base,
// U64 root/count/bytes/field-count, then each sorted field as LPname, U64action,
// LPvalue, U64lock). Each target prefix = SHA256(previous32, U64ordinal/entity,
// incarnation16, U64revision). One target grain is exactly 40 retained logical
// bytes. The manifest is completeness data, never authority or an EN seal.
func ipEditBulkSeed(r ipEditBulkRoot) (out [32]byte) {
	h := sha256.New()
	_, _ = h.Write([]byte(ipEditBulkManifest + "\x00"))
	for _, s := range []string{r.Operation, r.Kind, r.Library, r.Actor, r.BaseRevision} {
		ipBulkText(h, s)
	}
	for _, n := range []int64{r.RootEntity, r.ExpectedCount, r.ExpectedBytes, int64(len(r.Fields))} {
		ipBulkPut(h, n)
	}
	for _, f := range r.Fields {
		ipBulkText(h, string(f.Field))
		ipBulkPut(h, int64(f.Edit.Action))
		ipBulkText(h, f.Edit.Value)
		ipBulkPut(h, int64(f.Edit.Lock))
	}
	copy(out[:], h.Sum(nil))
	return
}

func ipEditBulkChain(previous [32]byte, t ipEditBulkTarget) (out [32]byte) {
	h := sha256.New()
	_, _ = h.Write(previous[:])
	ipBulkPut(h, t.Ordinal)
	ipBulkPut(h, t.EntityID)
	_, _ = h.Write(t.Incarnation[:])
	ipBulkPut(h, t.Revision)
	copy(out[:], h.Sum(nil))
	return
}

func ipEditBulkResultDigest(identity CommandIdentity, request RequestDigest, value ipEditBulkResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipEditBulkSavedResult{IPEditBulkBegin, value})
}

func preflightIPEditBulkResult(v ipEditBulkSavedResult) error {
	if v.Kind != IPEditBulkBegin {
		return ErrInvalid
	}
	r := v.Value
	if len(r.OwnerID) != 32 || r.Revision <= 0 || r.AfterOrdinal < 0 || r.Captured < 0 ||
		r.Processed < 0 || r.Failed < 0 || r.Processed > r.Captured ||
		r.Failed > r.Captured-r.Processed || r.AppliedMS < 0 || !r.Complete ||
		r.Manifest == [32]byte{} || r.AfterOrdinal != r.Captured {
		return ErrInvalid
	}
	for j := 0; j < len(r.OwnerID); j++ {
		if !(r.OwnerID[j] >= '0' && r.OwnerID[j] <= '9' || r.OwnerID[j] >= 'a' && r.OwnerID[j] <= 'f') {
			return ErrInvalid
		}
	}
	return nil
}

func writeIPEditBulkResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v ipEditBulkSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1}) // typed result version, independent of CommandID ordinals
	e.text("ip_edit_bulk")
	e.text("begin")
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	r := v.Value
	e.text(r.OwnerID)
	for _, n := range []int64{r.Revision, r.AfterOrdinal, r.Captured, r.Processed, r.Failed} {
		e.number(uint64(n))
	}
	e.flag(r.Complete)
	e.bytes(r.Manifest[:])
	e.number(uint64(r.AppliedMS))
	return e.err
}

func ipEditBulkResultEqual(a, b ipEditBulkResult) bool { return a == b }
