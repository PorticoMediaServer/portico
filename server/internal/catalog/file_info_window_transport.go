package catalog

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// File information window transport.
//
// This file is the only bounded readprep bridge for the FileInfo lanes. Every
// in-flight preparation lives in the one shared readprep store owned by
// FileInfoRuntime; nothing here caches text, labels, scalars, query rows or
// hydrated pages, queries a database, starts a goroutine or keeps a side index.
// Lane plan construction and lane hydration stay with the owner of each lane
// through the two parent-supplied callbacks. This bridge owns only the request
// descriptor, the canonical store key, the private query id and the signed
// continuation cursors.
//
// Invariants:
//   - A query id is <opening> "." <signed anchor>. The opening is the original
//     accepted Detail capture; the anchor names the private store handle, the
//     opening digest, the root item, the lane target asset when the lane needs
//     one, row zero and forward direction. Both parts are fixed width, so a
//     query id is rejected before decoding when it is not exactly one opening
//     plus one anchor.
//   - Every anchor and query lifetime is the actual private store lease expiry.
//     Submission, polling and continuation never mint a fresh lifetime: a
//     continuation inherits the original lease and can only narrow it.
//   - The canonical store key is the full JSON of {caller, descriptor}. It is
//     re-derived from Result.Key and must match the opening, lane and private
//     caller exactly before any ready or pending response.
//   - The original opening and the window witness are reauthenticated before
//     and after every ready or pending response. An authority failure cancels
//     the private handle and returns the raw readprep error.
//   - The descriptor carries no current content revision: it names the original
//     scope through the opening token, and lane callbacks hydrate scalars
//     transiently after ready.
//
// HTTP adapters map readprep.ErrCapacity and readprep.ErrStale; this transport
// preserves them unwrapped.

// fileInfoWindowRequest is the private request shape the public HTTP adapter
// will build. It is intentionally not wired to a route here: the adapter maps
// these raw strings and the explicit limit/presence flag to a descriptor, then
// calls windowSubmit or windowContinue. LimitPresent distinguishes "absent" from
// an explicit zero so the default and the continuation limit stay exact.
type fileInfoWindowRequest struct {
	Opening      string
	Lane         byte
	Limit        int
	LimitPresent bool
	Cursor       string
	Around       string
	Occurrence   string
	Group        string
}

// fileInfoWindowDescriptor is the complete canonical metadata of one bounded
// window interest. It holds only fixed-width coordinates, the original opening
// token and the separate FileInfo witness; no label, path, technical string or
// content scalar can enter the store key. Opening is the original accepted
// Detail capture, so the descriptor keeps the original scope even when the
// current revision has moved on.
type fileInfoWindowDescriptor struct {
	Opening   string                `json:"opening"`
	Witness   fileInfoWindowWitness `json:"witness"`
	Lane      byte                  `json:"lane"`
	Limit     int                   `json:"limit"`
	Anchor    fileInfoOccurrence    `json:"anchor"`
	HasAnchor bool                  `json:"hasAnchor"`
	Direction byte                  `json:"direction"`
	Row       int64                 `json:"row"`
	Target    fileInfoOccurrence    `json:"target"`
	Group     fileInfoGroupNumeric  `json:"group"`
}

// fileInfoWindowPlan builds the lane plan after admission, with the exact
// original proof and the verified window admission. The parent owns every
// lane-specific canonical check and the bounded computation.
type fileInfoWindowPlan func(context.Context, readprep.Caller, fileInfoOpeningProof, FileInfoAdmission, fileInfoWindowDescriptor) (readprep.Plan, error)

// fileInfoWindowHydrate turns a ready summary into the typed page for the exact
// query id. The parent owns lane-specific hydration and must reserve delivery
// bytes before allocating. It must not retain scalar content after return.
type fileInfoWindowHydrate func(context.Context, readprep.Caller, fileInfoOpeningProof, FileInfoAdmission, fileInfoWindowDescriptor, readprep.Result, string) (FileInfoWindowResult, error)

const (
	// fileInfoWindowNamespace isolates this family's entries and cancels from
	// every other preparation in the one shared readprep store.
	fileInfoWindowNamespace = "file-info-v1"
	// The anchor payload is written by fileInfoAnchorCodec; its size is checked
	// here before any decode so an attacker-sized cursor is never decoded.
	fileInfoAnchorRawSize        = 115
	fileInfoAnchorVersion        = 1
	fileInfoWindowCanonicalLimit = 4096
	fileInfoWindowKindLimit      = 64
	fileInfoWindowRetryMS        = 1000
)

var (
	fileInfoAnchorTokenSize  = base64.RawURLEncoding.EncodedLen(fileInfoAnchorRawSize)
	fileInfoOpeningTokenSize = base64.RawURLEncoding.EncodedLen(fileInfoOpeningSize)
	fileInfoQueryIDSize      = fileInfoOpeningTokenSize + 1 + fileInfoAnchorTokenSize
)

// fileInfoWindowLaneAsset reports whether a lane addresses one exact child
// asset, mirroring the shared anchor codec's exact-occurrence rule.
func fileInfoWindowLaneAsset(lane byte) bool {
	switch lane {
	case fileInfoLaneAudio, fileInfoLaneSubtitles, fileInfoLaneDisc, fileInfoLaneMembers:
		return true
	}
	return false
}

// fileInfoWindowQueryAsset is the lane target asset a signed root anchor must
// carry. Lanes that do not address one exact asset sign zero.
func fileInfoWindowQueryAsset(desc fileInfoWindowDescriptor) int64 {
	if fileInfoWindowLaneAsset(desc.Lane) {
		return desc.Target.Asset
	}
	return 0
}

func (d fileInfoWindowDescriptor) valid() bool {
	var zeroScope [32]byte
	var zeroOccurrence fileInfoOccurrence
	if len(d.Opening) != fileInfoOpeningTokenSize || d.Lane < fileInfoLaneFiles || d.Lane > fileInfoLaneMembers ||
		d.Limit < 1 || d.Limit > FileInfoMaximumLimit || d.Row < 0 ||
		d.Witness.Scope == zeroScope || d.Witness.Kind == "" || len(d.Witness.Kind) > fileInfoWindowKindLimit ||
		d.Target.Item < 0 || d.Target.Asset < 0 {
		return false
	}
	// Only the exact-occurrence lanes need a target asset: it becomes the asset
	// in the signed root anchor. Everything else is parent-owned lane metadata.
	if fileInfoWindowLaneAsset(d.Lane) && d.Target.Asset <= 0 {
		return false
	}
	if !d.HasAnchor {
		return d.Direction == fileInfoForward && d.Row == 0 && d.Anchor == zeroOccurrence
	}
	if d.Direction < fileInfoForward || d.Direction > fileInfoReload || d.Anchor.Item <= 0 || d.Anchor.Asset < 0 {
		return false
	}
	return !fileInfoWindowLaneAsset(d.Lane) || d.Anchor.Asset > 0
}

// fileInfoWindowCanonicalRecord is the exact canonical key payload: the private
// caller plus the full descriptor. encoding/json emits struct fields in
// declaration order, so the bytes are deterministic for this fixed shape.
type fileInfoWindowCanonicalRecord struct {
	Caller     readprep.Caller          `json:"caller"`
	Descriptor fileInfoWindowDescriptor `json:"descriptor"`
}

func fileInfoWindowCanonicalJSON(caller readprep.Caller, desc fileInfoWindowDescriptor) (string, error) {
	raw, err := json.Marshal(fileInfoWindowCanonicalRecord{Caller: caller, Descriptor: desc})
	if err != nil || len(raw) > fileInfoWindowCanonicalLimit {
		return "", readprep.ErrInvalid
	}
	return string(raw), nil
}

func fileInfoWindowBasis(opening string) string {
	digest := fileInfoOpeningDigest(opening)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func fileInfoWindowKey(caller readprep.Caller, desc fileInfoWindowDescriptor) (readprep.Key, error) {
	canonical, err := fileInfoWindowCanonicalJSON(caller, desc)
	if err != nil {
		return readprep.Key{}, err
	}
	return readprep.Key{Namespace: fileInfoWindowNamespace, Basis: fileInfoWindowBasis(desc.Opening), Canonical: canonical}, nil
}

func fileInfoWindowSplitQueryID(queryID string) (string, string, error) {
	if len(queryID) != fileInfoQueryIDSize || queryID[fileInfoOpeningTokenSize] != '.' {
		return "", "", readprep.ErrStale
	}
	return queryID[:fileInfoOpeningTokenSize], queryID[fileInfoOpeningTokenSize+1:], nil
}

// windowNow mirrors the shared codec clock so an unset test clock is never a
// nil call.
func (c *fileInfoAnchorCodec) windowNow() time.Time {
	if c == nil || c.now == nil {
		return time.Now()
	}
	return c.now()
}

// decodeWindowAnchor authenticates an anchor for one exact caller and opening
// without assuming its lane, which the caller reads from the payload. It is used
// where the lane is not known yet (a query id) and returns the same fixed
// coordinate validation as the shared decoder.
func (c *fileInfoAnchorCodec) decodeWindowAnchor(caller readprep.Caller, token string, opening [32]byte) (fileInfoAnchor, error) {
	var a fileInfoAnchor
	if c == nil || caller == "" || len(token) != fileInfoAnchorTokenSize {
		return a, readprep.ErrStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != fileInfoAnchorRawSize || raw[0] != fileInfoAnchorVersion || base64.RawURLEncoding.EncodeToString(raw) != token || !hmac.Equal(raw[99:], c.mac(caller, raw[:99])) {
		return fileInfoAnchor{}, readprep.ErrStale
	}
	a.Lane, a.Direction = raw[1], raw[2]
	a.Expires = time.Unix(0, int64(binary.BigEndian.Uint64(raw[3:11])))
	a.Handle = readprep.Handle(base64.RawURLEncoding.EncodeToString(raw[11:43]))
	copy(a.Opening[:], raw[43:75])
	a.Item = int64(binary.BigEndian.Uint64(raw[75:83]))
	a.Asset = int64(binary.BigEndian.Uint64(raw[83:91]))
	a.Row = int64(binary.BigEndian.Uint64(raw[91:99]))
	if a.Opening != opening || !a.valid(c.windowNow()) {
		return fileInfoAnchor{}, readprep.ErrStale
	}
	return a, nil
}

// windowDescriptorFromKey recovers the exact descriptor a private handle was
// submitted under and rejects any key that is not the canonical JSON of this
// caller, opening and lane. Re-marshalling and comparing the whole payload also
// rejects whitespace, reordered or unknown fields.
func (r *FileInfoRuntime) windowDescriptorFromKey(caller readprep.Caller, opening string, lane byte, key readprep.Key) (fileInfoWindowDescriptor, error) {
	var desc fileInfoWindowDescriptor
	if caller == "" || key.Namespace != fileInfoWindowNamespace || key.Basis != fileInfoWindowBasis(opening) ||
		len(key.Canonical) == 0 || len(key.Canonical) > fileInfoWindowCanonicalLimit {
		return desc, readprep.ErrStale
	}
	var record fileInfoWindowCanonicalRecord
	if err := json.Unmarshal([]byte(key.Canonical), &record); err != nil {
		return desc, readprep.ErrStale
	}
	if record.Caller != caller || record.Descriptor.Opening != opening || record.Descriptor.Lane != lane || !record.Descriptor.valid() {
		return desc, readprep.ErrStale
	}
	canonical, err := fileInfoWindowCanonicalJSON(record.Caller, record.Descriptor)
	if err != nil || canonical != key.Canonical {
		return desc, readprep.ErrStale
	}
	return record.Descriptor, nil
}

// windowRootAnchor signs the private query id anchor. Only the root item and the
// lane's target asset are named; row zero and forward direction identify the
// first window. The expiry is the store's actual lease expiry.
func (r *FileInfoRuntime) windowRootAnchor(caller readprep.Caller, desc fileInfoWindowDescriptor, proof fileInfoOpeningProof, h readprep.Handle, expiry time.Time) (string, error) {
	token, err := r.anchors.encode(caller, fileInfoAnchor{
		Handle:    h,
		Opening:   fileInfoOpeningDigest(desc.Opening),
		Item:      proof.Item,
		Asset:     fileInfoWindowQueryAsset(desc),
		Row:       0,
		Expires:   expiry,
		Lane:      desc.Lane,
		Direction: fileInfoForward,
	})
	if err != nil {
		return "", err
	}
	return desc.Opening + "." + token, nil
}

// signFileInfoWindowAnchor mints one continuation cursor under an existing query
// id. It reuses that query's private handle and its ACTUAL store lease expiry, so
// a cursor can never acquire a fresh lifetime. Lane-specific coordinate meaning
// stays with the parent: this helper only enforces the fixed anchor schema.
func (r *FileInfoRuntime) signFileInfoWindowAnchor(caller readprep.Caller, opening, queryID string, occurrence fileInfoOccurrence, row int64, direction byte) (string, error) {
	if r == nil || caller == "" {
		return "", readprep.ErrInvalid
	}
	open, token, err := fileInfoWindowSplitQueryID(queryID)
	if err != nil || open != opening {
		return "", readprep.ErrStale
	}
	signed, err := r.anchors.decodeWindowAnchor(caller, token, fileInfoOpeningDigest(opening))
	if err != nil {
		return "", err
	}
	if occurrence.Item <= 0 || occurrence.Asset < 0 || row < 0 || direction < fileInfoForward || direction > fileInfoReload {
		return "", readprep.ErrInvalid
	}
	if fileInfoWindowLaneAsset(signed.Lane) && occurrence.Asset <= 0 {
		// An exact-occurrence cursor must name its child asset; a zero asset is
		// not a valid row for this lane.
		return "", readprep.ErrInvalid
	}
	// The lease lookup is a non-blocking memory read; it does not honor a
	// request deadline and never extends the captured lifetime.
	expiry, err := r.store.LeaseExpiry(context.Background(), caller, signed.Handle)
	if err != nil {
		return "", err
	}
	return r.anchors.encode(caller, fileInfoAnchor{
		Handle:    signed.Handle,
		Opening:   fileInfoOpeningDigest(opening),
		Item:      occurrence.Item,
		Asset:     occurrence.Asset,
		Row:       row,
		Expires:   expiry,
		Lane:      signed.Lane,
		Direction: direction,
	})
}

// windowSubmit admits one bounded lane preparation and returns either its first
// ready page or a pending carrier. The opening is the original accepted Detail;
// the witness must still describe the live FileInfo audience. The store lease
// uses the opening's original expiry and a continuation may only narrow it.
func (r *FileInfoRuntime) windowSubmit(ctx context.Context, caller readprep.Caller, desc fileInfoWindowDescriptor, planFactory fileInfoWindowPlan, hydrate fileInfoWindowHydrate, until time.Time) (FileInfoWindowResult, error) {
	if r == nil || caller == "" || planFactory == nil || hydrate == nil || !desc.valid() {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	proof, ad, err := r.opening(ctx, caller, desc.Opening)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	if fileInfoWitness(ad) != desc.Witness {
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	expiry := proof.Expires
	if !until.IsZero() && until.Before(expiry) {
		expiry = until
	}
	key, err := fileInfoWindowKey(caller, desc)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	plan, err := planFactory(ctx, caller, proof, ad, desc)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	if plan.New == nil || plan.InitialBytes < 0 {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	h, err := r.store.SubmitUntil(ctx, caller, key, plan, expiry)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	lease, err := r.store.LeaseExpiry(ctx, caller, h)
	if err != nil {
		_ = r.store.CancelNamespace(ctx, caller, h, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	queryID, err := r.windowRootAnchor(caller, desc, proof, h, lease)
	if err != nil {
		_ = r.store.CancelNamespace(ctx, caller, h, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	out, err := r.windowPoll(ctx, caller, queryID, hydrate)
	if err != nil {
		_ = r.store.CancelNamespace(ctx, caller, h, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	return out, nil
}

// windowPoll transfers the delivery reservation only for a ready page. Pending
// carriers and every error release it here, because a pending response never
// carries content that must outlive the call. Metadata is re-derived from the
// exact private key and the original basis is reauthenticated before and after
// the response.
func (r *FileInfoRuntime) windowPoll(ctx context.Context, caller readprep.Caller, queryID string, hydrate fileInfoWindowHydrate) (FileInfoWindowResult, error) {
	if r == nil || caller == "" || hydrate == nil {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	opening, token, err := fileInfoWindowSplitQueryID(queryID)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	signed, err := r.anchors.decodeWindowAnchor(caller, token, fileInfoOpeningDigest(opening))
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	proof, err := r.openings.decodeOpening(caller, opening)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	result, err := r.store.Poll(ctx, caller, signed.Handle)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	transferred := false
	defer func() {
		if !transferred {
			result.Release()
		}
	}()
	desc, err := r.windowDescriptorFromKey(caller, opening, signed.Lane, result.Key)
	if err != nil {
		_ = r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	// A query id always names the root form: the opening's root item, the lane's
	// target asset when it needs one, row zero and forward direction.
	if signed.Item != proof.Item || signed.Asset != fileInfoWindowQueryAsset(desc) || signed.Row != 0 || signed.Direction != fileInfoForward {
		_ = r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	ad, err := r.checkWindowBasis(ctx, caller, proof, desc.Witness)
	if err != nil {
		_ = r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	out := FileInfoWindowResult{}
	pending := result.State != readprep.Ready
	if !pending {
		out, err = hydrate(ctx, caller, proof, ad, desc, result, queryID)
		if err != nil {
			out.Release()
			_ = r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
			return FileInfoWindowResult{}, err
		}
	} else {
		// The pending carrier echoes the original Detail capture only. It never
		// carries witness state, counts, rows or any later revision.
		out.Preparing = &FileInfoPreparing{
			Kind:         "file-info-preparing",
			State:        "preparing",
			QueryID:      queryID,
			RetryAfterMs: fileInfoWindowRetryMS,
			Detail:       &FileInfoDetailCapture{Scope: ad.DetailScope, Revision: proof.Revision},
		}
	}
	if _, err = r.checkWindowBasis(ctx, caller, proof, desc.Witness); err != nil {
		out.Release()
		_ = r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	if pending {
		result.Release()
		transferred = true
		return out, nil
	}
	out.release = result.Release
	transferred = true
	return out, nil
}

// windowContinue resumes one lane at a signed cursor. The old private handle must
// still be ready, the recovered descriptor must be the exact key that handle was
// submitted under, the cursor must sign the same opening and lane, and the new
// preparation inherits the original store expiry. Lane-specific anchor meaning
// is the parent callback's responsibility.
func (r *FileInfoRuntime) windowContinue(ctx context.Context, caller readprep.Caller, opening, cursor string, explicitLimit int, limitPresent bool, planFactory fileInfoWindowPlan, hydrate fileInfoWindowHydrate) (FileInfoWindowResult, error) {
	if r == nil || caller == "" || cursor == "" || planFactory == nil || hydrate == nil {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	if limitPresent && (explicitLimit < 1 || explicitLimit > FileInfoMaximumLimit) {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	signed, err := r.anchors.decodeWindowAnchor(caller, cursor, fileInfoOpeningDigest(opening))
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	proof, err := r.openings.decodeOpening(caller, opening)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	old, err := r.store.Poll(ctx, caller, signed.Handle)
	if err != nil {
		return FileInfoWindowResult{}, err
	}
	desc, err := r.windowDescriptorFromKey(caller, opening, signed.Lane, old.Key)
	if err != nil {
		old.Release()
		_ = r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	if old.State != readprep.Ready {
		old.Release()
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	until := old.OriginalExpiry
	old.Release()
	if until.IsZero() {
		// A poll always reports the captured lease; a missing ceiling must
		// never fall through to a fresh configured lifetime.
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	if _, err = r.checkWindowBasis(ctx, caller, proof, desc.Witness); err != nil {
		_ = r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
		return FileInfoWindowResult{}, err
	}
	// The signed cursor fixes the new anchor; the inherited descriptor keeps the
	// opening, witness, lane, target and group coordinates exactly.
	desc.Anchor = fileInfoOccurrence{Item: signed.Item, Asset: signed.Asset}
	desc.HasAnchor = true
	desc.Row = signed.Row
	desc.Direction = signed.Direction
	if limitPresent && explicitLimit != desc.Limit {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	if !desc.valid() {
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	return r.windowSubmit(ctx, caller, desc, planFactory, hydrate, until)
}

// windowCancel releases only this caller's demand in this family's namespace.
// Unknown, foreign, tampered and expired query ids are indistinguishable and
// report success, so a retry is always idempotent.
func (r *FileInfoRuntime) windowCancel(ctx context.Context, caller readprep.Caller, queryID string) error {
	if r == nil || caller == "" {
		return nil
	}
	opening, token, err := fileInfoWindowSplitQueryID(queryID)
	if err != nil {
		return nil
	}
	signed, err := r.anchors.decodeWindowAnchor(caller, token, fileInfoOpeningDigest(opening))
	if err != nil {
		return nil
	}
	return r.store.CancelNamespace(ctx, caller, signed.Handle, fileInfoWindowNamespace)
}
