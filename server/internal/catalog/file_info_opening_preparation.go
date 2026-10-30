package catalog

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const fileInfoOpeningNamespace = "file-info-opening-v1"

// FileInfoOpeningReady is an internal transport instruction, not a wire body.
// HTTP composes the requested document in an admitted snapshot, re-proves this
// epoch and mints its opening there. A pending opening has no original Detail.
type FileInfoOpeningReady struct {
	Identity   FileInfoIdentity
	Kind       string
	Related    string
	Epoch      int64
	descriptor fileInfoOpeningDescriptor
}

type fileInfoOpeningDescriptor struct {
	Caller        readprep.Caller
	Identity      FileInfoIdentity
	Incarnation   [16]byte
	Configuration int64
	Detail        [32]byte
	Witness       fileInfoWindowWitness
	Kind, Related string
	Desired       int64
}

func (r *FileInfoRuntime) openingAdmission(ctx context.Context, caller readprep.Caller, d fileInfoOpeningDescriptor) (FileInfoAdmission, error) {
	ad, e := r.check(ctx, caller, d.Identity)
	if e != nil {
		return ad, e
	}
	if caller != d.Caller || ad.Identity != d.Identity || !ad.validScope() || ad.ItemIncarnation != d.Incarnation || ad.ConfigurationRevision != d.Configuration || fileInfoScopeDigest(ad.DetailScope) != d.Detail || fileInfoWitness(ad) != d.Witness {
		return ad, readprep.ErrStale
	}
	return ad, nil
}

// PrepareOpening waits only for this item's accepted FI1 head. Neither the
// request nor a catalogue-wide revision is retained by the worker.
func (r *FileInfoRuntime) PrepareOpening(ctx context.Context, caller readprep.Caller, public, kind, related string) (FileInfoWindowResult, error) {
	if r == nil || caller == "" || (kind != "item" && kind != "detail") || (related != "" && related != "all" && related != "none") {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	raw, ok := entityid.Decode(public)
	if !ok {
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	var id FileInfoIdentity
	copy(id.PublicItem[:], raw)
	if e := fileInfoQuery(ctx, r.db).QueryRowContext(ctx, `SELECT id,library_id FROM catalog_entities WHERE public_id=? AND retired=0`, raw).Scan(&id.Item, &id.Library); e != nil {
		return FileInfoWindowResult{}, e
	}
	ad, e := r.check(ctx, caller, id)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	if ad.Identity != id || !ad.validScope() {
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	d := fileInfoOpeningDescriptor{Caller: caller, Identity: id, Incarnation: ad.ItemIncarnation, Configuration: ad.ConfigurationRevision, Detail: fileInfoScopeDigest(ad.DetailScope), Witness: fileInfoWitness(ad), Kind: kind, Related: related, Desired: ad.Head.Desired}
	if ad.Head.Ready() {
		return FileInfoWindowResult{Opening: &FileInfoOpeningReady{Identity: id, Kind: kind, Related: related, Epoch: ad.Head.Applied, descriptor: d}}, nil
	}
	encoded, e := json.Marshal(d)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	key := readprep.Key{Namespace: fileInfoOpeningNamespace, Basis: "item-head-v1", Canonical: string(encoded)}
	guard, ok := ctx.Value(fileInfoAuthorityContextKey{}).(FileInfoAuthorityCheck)
	if !ok {
		guard = r.check
	}
	charge := int64(8192+2*len(encoded)+len(caller)) + ad.CaptureBytes
	plan := readprep.Plan{InitialBytes: charge, ReuseHandle: false, New: func(c context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if e := b.Resize(charge); e != nil {
			return nil, e
		}
		return &fileInfoOpeningScan{runtime: r, guard: guard, desc: d, bytes: charge}, nil
	}}
	h, e := r.store.Submit(ctx, caller, key, plan)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	out, e := r.pollOpening(ctx, caller, "open."+string(h))
	if e != nil {
		_ = r.store.CancelNamespace(ctx, caller, h, fileInfoOpeningNamespace)
	}
	return out, e
}

type fileInfoOpeningScan struct {
	runtime *FileInfoRuntime
	guard   FileInfoAuthorityCheck
	desc    fileInfoOpeningDescriptor
	bytes   int64
}

func (s *fileInfoOpeningScan) AccountedBytes() int64 { return s.bytes }
func (s *fileInfoOpeningScan) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	ad, e := s.runtime.openingAdmission(WithFileInfoAuthority(ctx, s.guard), s.desc.Caller, s.desc)
	if e != nil {
		return readprep.StepResult{}, e
	}
	if !ad.Head.Ready() {
		return readprep.StepResult{RetryAfter: time.Second}, nil
	}
	if e = b.Resize(s.bytes + 8); e != nil {
		return readprep.StepResult{}, e
	}
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, uint64(ad.Head.Applied))
	s.bytes += 8
	return readprep.StepResult{Done: true, Payload: payload}, nil
}

func fileInfoOpeningHandle(id string) (readprep.Handle, error) {
	h, ok := strings.CutPrefix(id, "open.")
	if !ok || len(h) != 43 {
		return "", readprep.ErrStale
	}
	for _, c := range h {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", readprep.ErrStale
		}
	}
	return readprep.Handle(h), nil
}
func (r *FileInfoRuntime) pollOpening(ctx context.Context, caller readprep.Caller, id string) (FileInfoWindowResult, error) {
	h, e := fileInfoOpeningHandle(id)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	result, e := r.store.Poll(ctx, caller, h)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	transfer := false
	defer func() {
		if !transfer {
			result.Release()
		}
	}()
	var d fileInfoOpeningDescriptor
	if result.Key.Namespace != fileInfoOpeningNamespace || result.Key.Basis != "item-head-v1" || len(result.Key.Canonical) > 4096 || json.Unmarshal([]byte(result.Key.Canonical), &d) != nil {
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	canonical, _ := json.Marshal(d)
	if string(canonical) != result.Key.Canonical || d.Caller != caller || (d.Kind != "item" && d.Kind != "detail") {
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	ad, e := r.openingAdmission(ctx, caller, d)
	if e != nil {
		_ = r.store.CancelNamespace(ctx, caller, h, fileInfoOpeningNamespace)
		return FileInfoWindowResult{}, e
	}
	if result.State == readprep.Preparing {
		return FileInfoWindowResult{Preparing: &FileInfoPreparing{Kind: "file-info-preparing", State: "preparing", QueryID: id, RetryAfterMs: 1000}}, nil
	}
	if len(result.Payload) != 8 || !ad.Head.Ready() || ad.Head.Applied != int64(binary.BigEndian.Uint64(result.Payload)) {
		_ = r.store.CancelNamespace(ctx, caller, h, fileInfoOpeningNamespace)
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	transfer = true
	return FileInfoWindowResult{Opening: &FileInfoOpeningReady{Identity: d.Identity, Kind: d.Kind, Related: d.Related, Epoch: ad.Head.Applied, descriptor: d}, release: func() {
		result.Release()
		_ = r.store.CancelNamespace(context.Background(), caller, h, fileInfoOpeningNamespace)
	}}, nil
}

func (r *FileInfoRuntime) AcceptPreparedOpening(ctx context.Context, caller readprep.Caller, ready FileInfoOpeningReady, revision ContentRevision) (string, error) {
	ad, e := r.openingAdmission(ctx, caller, ready.descriptor)
	if e != nil {
		return "", e
	}
	if !ad.Head.Ready() || ad.Head.Applied != ready.Epoch {
		return "", readprep.ErrStale
	}
	token, e := r.AcceptDetailOpening(ctx, caller, ready.Identity, revision)
	if e != nil {
		return "", e
	}
	proof, e := r.openings.decodeOpening(caller, token)
	if e != nil {
		return "", e
	}
	// Stable document tokens use twenty-five-minute buckets, with five minutes
	// of overlap. A new opening thus has five to thirty minutes to complete its
	// first lane request; neither a token nor any continuation exceeds the
	// original thirty-minute maximum.
	proof.Expires = r.now().Truncate(25 * time.Minute).Add(30 * time.Minute)
	return r.openings.encodeOpening(caller, proof)
}
func (r *FileInfoRuntime) MatchOpeningItem(caller readprep.Caller, opening, public string) error {
	p, e := r.openings.decodeOpening(caller, opening)
	if e != nil {
		return e
	}
	if entityid.Encode(p.PublicItem[:]) != public {
		return readprep.ErrStale
	}
	return nil
}
