package writer

import (
	"crypto/sha256"
	"encoding/binary"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"hash"
	"math"
	"strings"
)

// Descriptive bulk is ENTITY-rooted. Targets retain catalogue identities; a
// person-only operation never borrows an entity. The operation owner is born
// by the outer W driver, not by a caller-supplied UUID or this private body.
const ipEditBulkManifest = "portico-ip-descriptive-input-sha256-v1"
const ipEditBulkChunk = 16
const ipEditBulkIntent = "descriptive-bulk-v1"

type ipEditBulkAction uint8

const (
	ipEditBulkBegin   ipEditBulkAction = 1
	ipEditBulkCapture ipEditBulkAction = 2
	ipEditBulkSeal    ipEditBulkAction = 3
	ipEditBulkApply   ipEditBulkAction = 4
	ipEditBulkFinish  ipEditBulkAction = 5
)

func (k ipEditBulkAction) command() CommandID {
	switch k {
	case ipEditBulkBegin, ipEditBulkCapture, ipEditBulkSeal:
		return IPEditBulkBegin
	case ipEditBulkApply:
		return IPEditBulkPage
	case ipEditBulkFinish:
		return IPEditBulkFinish
	}
	return 0
}
func (k ipEditBulkAction) text() string {
	switch k {
	case ipEditBulkBegin:
		return "begin"
	case ipEditBulkCapture:
		return "capture"
	case ipEditBulkSeal:
		return "seal"
	case ipEditBulkApply:
		return "page"
	case ipEditBulkFinish:
		return "finish"
	}
	return ""
}

type ipEditBulkField struct {
	Field fieldmeta.TextField
	Edit  fieldmeta.TextEdit
}
type ipEditBulkTarget struct {
	Ordinal, EntityID int64
	Incarnation       [16]byte
	Revision          int64
}
type ipEditBulkRoot struct {
	Operation, Kind, Library, Actor, BaseRevision string
	RootEntity, ExpectedCount, ExpectedBytes      int64
	Manifest                                      [32]byte
	Fields                                        []ipEditBulkField
}
type ipEditBulkRequest struct {
	Identity               CommandIdentity
	Action                 ipEditBulkAction
	Root                   ipEditBulkRoot
	Revision, AfterOrdinal int64
	Targets                []ipEditBulkTarget
}
type ipEditBulkCommand struct {
	request    ipEditBulkRequest
	digest     RequestDigest
	seed       [32]byte
	fieldBytes int64
}
type ipEditBulkResult struct {
	Revision, AfterOrdinal, Captured, Processed, Failed, AppliedMS int64
	Complete                                                       bool
	Manifest                                                       [32]byte
}
type ipEditBulkNative struct {
	decision receiptDecision
	value    ipEditBulkResult
	digest   ResultDigest
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
func newIPEditBulkCommand(q ipEditBulkRequest) (ipEditBulkCommand, error) {
	r := q.Root
	switch r.Kind {
	case "item", "show", "season", "album", "artist", "book", "collection":
	default:
		return ipEditBulkCommand{}, ErrInvalid
	}
	if !ipPolicyValidIdentity(q.Identity) || q.Action.command() == 0 || q.Revision <= 0 || q.Revision == math.MaxInt64 || q.AfterOrdinal < 0 || q.AfterOrdinal > r.ExpectedCount ||
		r.RootEntity <= 0 || r.ExpectedCount < 0 || r.ExpectedCount > math.MaxInt64/40 || r.ExpectedBytes < 0 || r.Manifest == [32]byte{} || len(r.Fields) == 0 || len(r.Fields) > 11 ||
		!ipPolicyValidText(r.Operation, 512) || !ipPolicyValidText(r.Kind, 64) || !ipPolicyValidText(r.Library, 512) || !ipPolicyValidText(r.Actor, 4096) || !ipPolicyValidText(r.BaseRevision, 128) {
		return ipEditBulkCommand{}, ErrInvalid
	}
	if q.Action != ipEditBulkCapture && len(q.Targets) != 0 || q.Action == ipEditBulkCapture && (len(q.Targets) == 0 || len(q.Targets) > ipEditBulkChunk || int64(len(q.Targets)) > r.ExpectedCount-q.AfterOrdinal) {
		return ipEditBulkCommand{}, ErrInvalid
	}
	var bytes int64
	last := ""
	for _, f := range r.Fields {
		// These are the current pure text BULK fields. Other public field types
		// need their finite native projection/effect variants before admission.
		switch f.Field {
		case fieldmeta.MetadataLanguage, fieldmeta.Studio, fieldmeta.Network, fieldmeta.Country:
		default:
			return ipEditBulkCommand{}, ErrInvalid
		}
		if string(f.Field) <= last || ipEditBulkFieldAction(f.Edit) == "" {
			return ipEditBulkCommand{}, ErrInvalid
		}
		last = string(f.Field)
		if _, err := fieldmeta.PrepareTextEdit(f.Field, fieldmeta.TextState{Effective: fieldmeta.Text{Present: true}}, f.Edit); err != nil {
			return ipEditBulkCommand{}, ErrInvalid
		}
		bytes += 32 + int64(len(f.Field)+len(f.Edit.Value))
	}
	if bytes > math.MaxInt64-r.ExpectedCount*40 || r.ExpectedBytes != bytes+r.ExpectedCount*40 {
		return ipEditBulkCommand{}, ErrInvalid
	}
	for j, t := range q.Targets {
		if t.Ordinal != q.AfterOrdinal+int64(j) || t.EntityID <= 0 || t.Incarnation == [16]byte{} || t.Revision <= 0 {
			return ipEditBulkCommand{}, ErrInvalid
		}
	}
	if q.Action == ipEditBulkBegin && (q.AfterOrdinal != 0 || q.Identity.Sequence != 1) {
		return ipEditBulkCommand{}, ErrInvalid
	}
	d, err := canonicalRequestDigest(q.Action.command(), 1, q.Identity, q)
	if err != nil {
		return ipEditBulkCommand{}, err
	}
	r.Fields = append([]ipEditBulkField(nil), r.Fields...)
	for j := range r.Fields {
		r.Fields[j].Field = fieldmeta.TextField(strings.Clone(string(r.Fields[j].Field)))
		r.Fields[j].Edit.Value = strings.Clone(r.Fields[j].Edit.Value)
	}
	r.Operation = strings.Clone(r.Operation)
	r.Kind = strings.Clone(r.Kind)
	r.Library = strings.Clone(r.Library)
	r.Actor = strings.Clone(r.Actor)
	r.BaseRevision = strings.Clone(r.BaseRevision)
	q.Root = r
	q.Targets = append([]ipEditBulkTarget(nil), q.Targets...)
	return ipEditBulkCommand{q, d, ipEditBulkSeed(r), bytes}, nil
}
func ipBulkPut(h hash.Hash, n int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	_, _ = h.Write(b[:])
}
func ipBulkText(h hash.Hash, s string) { ipBulkPut(h, int64(len(s))); _, _ = h.Write([]byte(s)) }

// Manifest v1: seed = SHA256(domain NUL, LP operation/kind/library/actor/base,
// U64 root/count/bytes/field-count, then each sorted field as LPname, U64action,
// LPvalue, U64lock). Each target prefix = SHA256(previous32, U64ordinal/entity,
// incarnation16,U64revision). Target grain is exactly40 retained logical bytes.
// The manifest is completeness data, never writer authority or an EN seal.
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

type ipEditBulkSavedResult struct {
	Kind  ipEditBulkAction
	Value ipEditBulkResult
}

func preflightIPEditBulkResult(v ipEditBulkSavedResult) error {
	r := v.Value
	if v.Kind.command() == 0 || r.Revision <= 0 || r.AfterOrdinal < 0 || r.Captured < 0 || r.Processed < 0 || r.Failed < 0 || r.Processed > r.Captured || r.Failed > r.Captured-r.Processed || r.AppliedMS < 0 || r.Manifest == [32]byte{} {
		return ErrInvalid
	}
	return nil
}
func writeIPEditBulkResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipEditBulkSavedResult) error {
	writeIPResultIdentity(e, "ip_descriptive_bulk", i, q)
	e.number(uint64(v.Kind))
	r := v.Value
	for _, n := range []int64{r.Revision, r.AfterOrdinal, r.Captured, r.Processed, r.Failed, r.AppliedMS} {
		e.number(uint64(n))
	}
	e.flag(r.Complete)
	e.bytes(r.Manifest[:])
	return e.err
}
