package writer

import (
	"math"
	"strings"
)

// Private draft components for 223..231. These are not authority or an Applied
// surface. W must compose current permission/disclosure, actual governor and
// physical-memory admission, clock, safe COMMIT, Capture and revision/event
// integration before use. All file IO, file hashing and image encoding is outside SQL.
const (
	ipArtPrepare          int64 = 1
	ipArtInstalled        int64 = 2
	ipArtPublish          int64 = 3
	ipArtSource           int64 = 1
	ipArtBlob             int64 = 2
	ipArtIntent           int64 = 3
	ipArtMaxFileBytes     int64 = 64 << 20
	ipArtMaxManifestBytes int64 = 4 * ipArtMaxFileBytes
)

type ipArtworkMaterialSlot struct {
	Digest, MIME         string
	Width, Height, Bytes int64
	Outlier              bool
}

// Accepted values from the existing external image pipeline, never an encoder.
// Slots0/1/2 are small/large/ambient; optional slot3 is the original source.
// Intent ID is EXACTLY its artwork_jobs.id: no unfenced job-to-intent alias.
type ipArtworkMaterial struct {
	ID, SourceDigest, MIME                                          string
	Generation, Revision, Width, Height, LargeEdge, EncoderRevision int64
	SlotCount                                                       int64
	Slots                                                           [4]ipArtworkMaterialSlot
	SourceRevision                                                  int64 // 0=new immutable source; positive=exact reencode CAS
	Previous                                                        ipArtworkMaterialSource
}
type ipArtworkMaterialSource struct {
	Small, Large, Ambient                                                 string
	LargeEdge, SmallOutlier, LargeOutlier, EncoderRevision, OriginalBytes int64
}
type ipArtworkMaterialJob struct {
	ID, Role, Choice, ImageID, Origin, Attribution, Locale, SourceFence, Actor string
	Revision, State, LeaseOwner, LeaseGeneration, LeaseUntilMS                 int64
	NextMS                                                                     EvidenceInteger
	SelectionRevision                                                          int64
	// Native object identity fence is nullable as a pair, never coalesced with "".
	ObjectID, ObjectRevision EvidenceText
}
type ipArtworkMaterialPolicy struct {
	Provider, Language string
	Revision           int64
}
type ipArtworkSelectionValue struct {
	Present                                                                              bool
	Revision                                                                             int64
	Digest, Provider, ImageID, Origin, Attribution, Locale, Language, SourceFence, Actor string
	ObjectID, ObjectRevision                                                             EvidenceText
}
type ipArtworkSelectionRequest struct {
	CandidateStageID, CandidateOrdinal int64 // ready native observation for a changed matched selection
	Role, Choice, Actor                string
	Before, After                      ipArtworkSelectionValue
	// Captured current locked choice is checked separately even when changing
	// matched. An absent locked choice is distinct from a locked empty role.
	Locked ipArtworkSelectionValue
}
type ipArtworkGCRequest struct {
	Target   int64
	Digest   string
	Revision int64
	Token    [16]byte
	// Exact original-file custody. An original shares source_digest; originals
	// are not artwork_blobs and may be removed only after source retirement mark.
	OriginalBytes int64
}
type ipArtworkMaterialRequest struct {
	Identity  CommandIdentity
	Owner     ipArtworkDiscoveryOwner
	Policy    ipArtworkMaterialPolicy
	Job       ipArtworkMaterialJob
	Material  ipArtworkMaterial
	Selection ipArtworkSelectionRequest
	GC        ipArtworkGCRequest
	Phase     int64
}

// One closed result schema, persisted entirely in artwork_command_receipts.
// Count is changed rows/terminal disposition, Action is phase or GC target.
// Empty strings/zero token/zero scalar are required SQL NULL sentinels.
type ipArtworkMaterialResult struct {
	Kind                                                                       CommandID
	OwnerID, Revision, AppliedMS, Count, Action, LeaseGeneration, LeaseUntilMS int64
	MaterialID, Role, Choice, Selected, Previous, Blob                         string
	SelectionRevision, StorageRevision, HistoryID                              int64
	Token                                                                      [16]byte
}
type ipArtworkMaterialOutcome struct {
	decision receiptDecision
	result   ipArtworkMaterialResult
}

func ipArtDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func ipArtPositive(n int64) bool { return n > 0 && n < math.MaxInt64 }
func ipArtRole(s string) bool {
	return s != "" && s == strings.ToLower(strings.TrimSpace(s)) && s != "thumb" && s != "thumbnail" && ipArtworkDiscoveryText(s, 64)
}
func ipArtChoice(s string) bool { return s == "matched" || s == "locked" }
func ipArtOptional(v EvidenceText) bool {
	return (!v.Present && v.Value == "") || v.Present && v.Value != "" && ipArtworkDiscoveryText(v.Value, 4096)
}
func ipArtSelectionValue(v ipArtworkSelectionValue) bool {
	if !v.Present {
		return v == (ipArtworkSelectionValue{})
	}
	if !ipArtPositive(v.Revision) || !ipArtDigest(v.Digest) || v.Actor == "" || v.ImageID == "" || !ipArtOptional(v.ObjectID) || !ipArtOptional(v.ObjectRevision) || v.ObjectID.Present != v.ObjectRevision.Present {
		return false
	}
	for _, s := range []string{v.Provider, v.ImageID, v.Origin, v.Attribution, v.Locale, v.Language, v.SourceFence, v.Actor} {
		if !ipArtworkDiscoveryText(s, 16384) {
			return false
		}
	}
	return true
}

// Actual original text bytes, including every inactive variant field. This
// bounds digest work but is NOT physical-memory admission; W funds/copies the
// complete typed root and bounded audit buffers under its real memory scope.
func ipArtworkMaterialInputBytes(q ipArtworkMaterialRequest) (int64, error) {
	type text struct {
		s string
		n int
	}
	p, j, m, s := q.Policy, q.Job, q.Material, q.Selection
	values := []text{{p.Provider, 64}, {p.Language, 64}, {j.ID, 256}, {j.Role, 64}, {j.Choice, 7}, {j.ImageID, 4096}, {j.Origin, 4096}, {j.Attribution, 16384}, {j.Locale, 64}, {j.SourceFence, 4096}, {j.Actor, 256}, {j.ObjectID.Value, 4096}, {j.ObjectRevision.Value, 4096}, {m.ID, 256}, {m.SourceDigest, 64}, {m.MIME, 10}, {m.Previous.Small, 64}, {m.Previous.Large, 64}, {m.Previous.Ambient, 64}, {s.Role, 64}, {s.Choice, 7}, {s.Actor, 256}, {q.GC.Digest, 64}}
	for _, slot := range m.Slots {
		values = append(values, text{slot.Digest, 64}, text{slot.MIME, 10})
	}
	for _, v := range []ipArtworkSelectionValue{s.Before, s.After, s.Locked} {
		values = append(values, text{v.Digest, 64}, text{v.Provider, 64}, text{v.ImageID, 4096}, text{v.Origin, 4096}, text{v.Attribution, 16384}, text{v.Locale, 64}, text{v.Language, 64}, text{v.SourceFence, 4096}, text{v.Actor, 256}, text{v.ObjectID.Value, 4096}, text{v.ObjectRevision.Value, 4096})
	}
	var bytes int64
	for _, v := range values {
		if !ipArtworkDiscoveryText(v.s, v.n) {
			return 0, ErrInvalid
		}
		bytes += int64(len(v.s))
	}
	if bytes > 256<<10 {
		return 0, ErrInvalid
	}
	return bytes, nil
}

func ipArtValidate(kind CommandID, q ipArtworkMaterialRequest) error {
	if _, err := ipArtworkMaterialInputBytes(q); err != nil {
		return err
	}
	o, i := q.Owner, q.Identity
	if kind < IPArtworkJobClaim || kind > IPArtworkReencode || !ipArtPositive(o.id) || !ipArtPositive(o.revision) || i.Incarnation != o.incarnation || i.Incarnation == ([16]byte{}) || i.OperationID == ([16]byte{}) || i.Sequence <= 0 || i.Sequence == math.MaxInt64 || (o.entityID <= 0) == (o.personID <= 0) || o.entityID < 0 || o.personID < 0 {
		return ErrInvalid
	}
	if kind == IPArtworkGCMark || kind == IPArtworkGCFinish {
		g := q.GC
		if g.Target < ipArtSource || g.Target > ipArtIntent || !ipArtPositive(g.Revision) || g.Token == ([16]byte{}) || g.OriginalBytes < 0 || g.OriginalBytes > ipArtMaxFileBytes {
			return ErrInvalid
		}
		if g.Target != ipArtSource && g.OriginalBytes != 0 || g.Target == ipArtIntent && g.Digest != "" {
			return ErrInvalid
		}
		if g.Target != ipArtIntent && !ipArtDigest(g.Digest) {
			return ErrInvalid
		}
		if g.Target == ipArtIntent && (q.Material.ID == "" || !ipArtPositive(q.Material.Generation) || q.Material.Revision != g.Revision || !ipArtworkDiscoveryText(q.Material.ID, 256)) {
			return ErrInvalid
		}
		return nil
	}
	if !ipArtPositive(o.sourceRevision) || !ipArtPositive(o.leaseOwner) || !ipArtPositive(o.leaseGeneration) || o.leaseUntilMS <= 0 || o.sourceIncarnation == ([16]byte{}) || !ipArtPositive(q.Policy.Revision) || !ipArtworkDiscoveryText(q.Policy.Provider, 64) || q.Policy.Provider == "" || !ipArtworkDiscoveryText(q.Policy.Language, 64) {
		return ErrInvalid
	}
	if kind == IPArtworkSelect || kind == IPArtworkClear || kind == IPArtworkUploadAccept {
		s := q.Selection
		if s.Actor == "" || !ipArtworkDiscoveryText(s.Actor, 256) || !ipArtRole(s.Role) || !ipArtChoice(s.Choice) || !ipArtSelectionValue(s.Before) || !ipArtSelectionValue(s.After) || !ipArtSelectionValue(s.Locked) || s.Before.Present && s.Before.Revision == math.MaxInt64-1 {
			return ErrInvalid
		}
		if s.Choice == "locked" && s.Before != s.Locked {
			return ErrInvalid
		}
		if s.After.Present && (s.After.Actor != s.Actor || s.After.Language != q.Policy.Language || s.Choice == "matched" && s.After.Provider != q.Policy.Provider) {
			return ErrInvalid
		}
		if s.CandidateStageID < 0 || s.CandidateOrdinal < 0 {
			return ErrInvalid
		}
		if kind == IPArtworkClear && s.After.Present || kind != IPArtworkClear && !s.After.Present {
			return ErrInvalid
		}
		if s.After.Present && s.After.Revision != s.Before.Revision+1 {
			return ErrInvalid
		}
		if kind == IPArtworkUploadAccept && s.Choice != "locked" {
			return ErrInvalid
		}
		if kind == IPArtworkUploadAccept && q.Phase != ipArtPublish {
			return ErrInvalid
		}
		if kind != IPArtworkUploadAccept {
			return nil
		}
		if s.Role != q.Job.Role || s.Choice != q.Job.Choice || s.After.Digest != q.Material.SourceDigest {
			return ErrInvalid
		}
	}
	j := q.Job
	if j.ID == "" || !ipArtworkDiscoveryText(j.ID, 256) || !ipArtRole(j.Role) || !ipArtChoice(j.Choice) || !ipArtPositive(j.Revision) || j.State < 1 || j.State > 4 || j.LeaseGeneration < 0 || j.LeaseGeneration >= math.MaxInt64-1 || j.LeaseUntilMS < 0 || j.NextMS.Value < 0 || !j.NextMS.Present && j.NextMS.Value != 0 || !ipArtOptional(j.ObjectID) || !ipArtOptional(j.ObjectRevision) || j.ObjectID.Present != j.ObjectRevision.Present || j.SelectionRevision < 0 {
		return ErrInvalid
	}
	for _, s := range []string{j.ImageID, j.Origin, j.Attribution, j.Locale, j.SourceFence, j.Actor} {
		if !ipArtworkDiscoveryText(s, 16384) {
			return ErrInvalid
		}
	}
	if j.ImageID == "" {
		return ErrInvalid
	}
	if kind == IPArtworkJobClaim || kind == IPArtworkJobRenew {
		if q.Phase <= 0 || q.Phase > 120000 {
			return ErrInvalid
		}
		return nil // lease duration in milliseconds, not authority
	}
	m := q.Material
	if m.ID != j.ID || m.Generation != j.LeaseGeneration || !ipArtPositive(m.Generation) || !ipArtPositive(m.Revision) || !ipArtDigest(m.SourceDigest) || !ipArtPositive(m.EncoderRevision) || m.SourceRevision < 0 || m.SourceRevision == math.MaxInt64 || q.Phase < ipArtPrepare || q.Phase > ipArtPublish || kind == IPArtworkReencode && q.Phase != ipArtPublish {
		return ErrInvalid
	}
	if m.Width <= 0 || m.Height <= 0 || m.Width > 10000 || m.Height > 10000 || m.Width*m.Height > 24000000 || (m.LargeEdge != 1280 && m.LargeEdge != 1920 && m.LargeEdge != 2560) || (m.MIME != "image/jpeg" && m.MIME != "image/png" && m.MIME != "image/webp") || (m.SlotCount != 3 && m.SlotCount != 4) {
		return ErrInvalid
	}
	var total int64
	for n, s := range m.Slots {
		if int64(n) >= m.SlotCount {
			if s != (ipArtworkMaterialSlot{}) {
				return ErrInvalid
			}
			continue
		}
		if !ipArtDigest(s.Digest) || s.Width <= 0 || s.Height <= 0 || s.Width > 10000 || s.Height > 10000 || s.Width*s.Height > 24000000 || s.Bytes <= 0 || s.Bytes > ipArtMaxFileBytes || (s.MIME != "image/jpeg" && s.MIME != "image/png" && (n != 3 || s.MIME != "image/webp")) {
			return ErrInvalid
		}
		if n == 3 && (s.Digest != m.SourceDigest || s.MIME != m.MIME || s.Width != m.Width || s.Height != m.Height || s.Outlier) {
			return ErrInvalid
		}
		total += s.Bytes
	}
	if m.SourceRevision > 0 {
		old := m.Previous
		if !ipArtDigest(old.Small) || !ipArtDigest(old.Large) || !ipArtDigest(old.Ambient) || !ipArtPositive(old.EncoderRevision) || (old.LargeEdge != 1280 && old.LargeEdge != 1920 && old.LargeEdge != 2560) || (old.SmallOutlier != 0 && old.SmallOutlier != 1) || (old.LargeOutlier != 0 && old.LargeOutlier != 1) || old.OriginalBytes < 0 || old.OriginalBytes > ipArtMaxFileBytes {
			return ErrInvalid
		}
	} else if m.Previous != (ipArtworkMaterialSource{}) {
		return ErrInvalid
	}
	if total > ipArtMaxManifestBytes {
		return ErrInvalid
	}
	if kind == IPArtworkReencode && m.SourceRevision <= 0 || kind != IPArtworkReencode && q.Phase == ipArtPublish && m.SourceRevision != 0 {
		return ErrInvalid
	}
	return nil
}

// Sole production digest route. Parent must add the two type-switch cases in
// result_digest.go described in ip_artwork_material_handoff.go before receipt
// acceptance. No alternate encoder/hash is supplied when that gate is absent.
func ipArtResultDigest(i CommandIdentity, q RequestDigest, r ipArtworkMaterialResult) (ResultDigest, error) {
	return canonicalResultDigest(i, q, r)
}
func preflightIPArtworkMaterialResult(r ipArtworkMaterialResult) error {
	if r.Kind < IPArtworkJobClaim || r.Kind > IPArtworkReencode || r.OwnerID <= 0 || r.Revision <= 0 || r.AppliedMS < 0 || r.Count < 0 || r.Count > 4 || r.Action < 0 || r.Action > ipArtPublish || r.LeaseGeneration < 0 || r.LeaseUntilMS < 0 || r.SelectionRevision < 0 || r.StorageRevision < 0 || r.HistoryID < 0 {
		return ErrInvalid
	}
	for _, s := range []string{r.MaterialID, r.Role, r.Choice, r.Selected, r.Previous, r.Blob} {
		if !ipArtworkDiscoveryText(s, 256) {
			return ErrInvalid
		}
	}
	for _, s := range []string{r.Selected, r.Previous, r.Blob} {
		if s != "" && !ipArtDigest(s) {
			return ErrInvalid
		}
	}
	if (r.Role == "") != (r.Choice == "") || r.Role != "" && (!ipArtRole(r.Role) || !ipArtChoice(r.Choice) || r.SelectionRevision <= 0) {
		return ErrInvalid
	}
	if (r.Kind == IPArtworkSelect || r.Kind == IPArtworkClear || r.Kind == IPArtworkUploadAccept) && r.Role == "" {
		return ErrInvalid
	}
	if r.Kind == IPArtworkSelect && r.Selected == "" {
		return ErrInvalid
	}
	if r.Kind == IPArtworkGCMark && (r.Token == ([16]byte{}) || r.StorageRevision <= 0) {
		return ErrInvalid
	}
	// Each command has one closed historical shape; unused fields must be zero.
	noRefs := r.MaterialID == "" && r.Role == "" && r.Choice == "" && r.Selected == "" && r.Previous == "" && r.Blob == "" && r.SelectionRevision == 0 && r.HistoryID == 0
	noLease := r.LeaseGeneration == 0 && r.LeaseUntilMS == 0
	switch r.Kind {
	case IPArtworkJobClaim, IPArtworkJobRenew:
		if !noRefs || r.Count != 1 || r.Action != 0 || r.LeaseGeneration <= 0 || r.LeaseUntilMS <= r.AppliedMS || r.StorageRevision != 0 || r.Token != ([16]byte{}) {
			return ErrInvalid
		}
	case IPArtworkMaterialPublish, IPArtworkReencode:
		if !noLease || r.MaterialID == "" || r.Role != "" || r.Choice != "" || r.Previous != "" || r.Blob != "" || r.SelectionRevision != 0 || r.HistoryID != 0 || r.Token != ([16]byte{}) || r.Action < ipArtPrepare || r.Action > ipArtPublish {
			return ErrInvalid
		}
		if r.Action == ipArtPublish {
			if r.Count > 1 || r.Selected == "" || r.StorageRevision <= 0 {
				return ErrInvalid
			}
		} else if (r.Count != 3 && r.Count != 4) || r.Selected != "" || r.StorageRevision != 0 {
			return ErrInvalid
		}
		if r.Kind == IPArtworkReencode && (r.Action != ipArtPublish || r.StorageRevision <= 0) {
			return ErrInvalid
		}
	case IPArtworkSelect, IPArtworkClear, IPArtworkUploadAccept:
		if !noLease || r.Count > 1 || (r.Count == 1) != (r.HistoryID > 0) || r.Token != ([16]byte{}) || r.Blob != "" {
			return ErrInvalid
		}
		if r.Kind == IPArtworkClear && r.Selected != "" {
			return ErrInvalid
		}
		if r.Kind == IPArtworkUploadAccept {
			if r.Selected == "" || r.Choice != "locked" || r.Count == 1 && (r.MaterialID == "" || r.Action != ipArtPublish || r.StorageRevision <= 0) {
				return ErrInvalid
			}
		} else if r.MaterialID != "" || r.Action != 0 || r.StorageRevision != 0 {
			return ErrInvalid
		}
	case IPArtworkGCMark, IPArtworkGCFinish:
		if !noLease || r.Count != 1 || r.Action < ipArtSource || r.Action > ipArtIntent || r.Token == ([16]byte{}) || r.StorageRevision <= 0 || r.MaterialID != "" || r.Role != "" || r.Choice != "" || r.Selected != "" || r.Previous != "" || r.SelectionRevision != 0 || r.HistoryID != 0 {
			return ErrInvalid
		}
		if r.Action != ipArtIntent && r.Blob == "" || r.Action == ipArtIntent && r.Blob != "" {
			return ErrInvalid
		}
	}
	return nil
}

// Exact v1 order: canonicalResultCodec, uint16(1), LP ip_artwork_material,
// U64kind, identity16/U64sequence/operation16/request32, then U64 owner/revision/
// applied/count/action/leaseGeneration/leaseUntil, LP material/role/choice/
// selected/previous/blob, U64 selectionRevision/storageRevision/historyID,
// raw token16. All primitives are W's existing requestEncoder.
func writeIPArtworkMaterialResult(e *requestEncoder, i CommandIdentity, q RequestDigest, r ipArtworkMaterialResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_material")
	e.number(uint64(r.Kind))
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	for _, n := range []int64{r.OwnerID, r.Revision, r.AppliedMS, r.Count, r.Action, r.LeaseGeneration, r.LeaseUntilMS} {
		e.number(uint64(n))
	}
	for _, s := range []string{r.MaterialID, r.Role, r.Choice, r.Selected, r.Previous, r.Blob} {
		e.text(s)
	}
	for _, n := range []int64{r.SelectionRevision, r.StorageRevision, r.HistoryID} {
		e.number(uint64(n))
	}
	e.bytes(r.Token[:])
	return e.err
}
