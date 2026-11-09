package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/artwork"
)

// IP artwork material 223/224 records durable custody of one already-encoded
// image set for a claimed native artwork job. The intent (223) fixes the source
// facts and the expected 3/4 slot count before any file exists; the installed
// handoff (224) records the complete immutable manifest after the caller has
// physically renamed those files into place. Both commands run on the NORMAL
// SQLite turn only: no provider call, no filesystem access and no image decode
// happens inside the hold. The retained original upload in slot 3 is charged
// through artwork_material_slots, which (like artwork_material_intents.
// source_digest) sits OUTSIDE the selection/history closure; IP-20 publishes
// that custody into artwork_sources/artwork_blobs. IP-19 never writes either.
const (
	ipArtworkMaterialMaxIntent = artwork.MaterialMaxIntent
	ipArtworkMaterialMaxSlot   = artwork.MaterialMaxSlotBytes
	ipArtworkMaterialMaxBytes  = artwork.MaterialMaxTotalBytes
	ipArtworkMaterialMaxImage  = 4096
)

// The retained artwork subject job is the command owner. Entity and person IDs
// are separate namespaces; equal integers are not interchangeable. Lease facts
// are CAS witnesses supplied by the caller, never authority.
type ipArtworkMaterialOwner struct {
	id          int64
	incarnation [16]byte
	entityID    int64
	personID    int64
	// sourceIncarnation/sourceRevision fence the accepted artwork source.
	sourceIncarnation                                     [16]byte
	sourceRevision, revision, leaseOwner, leaseGeneration int64
	leaseUntilMS                                          int64
}

// The exact native image job. Its ID is also the material intent ID, so one
// job owns one immutable material generation; a replacement job needs a new
// native ID. The row stays the work owner; no second per-image owner is added.
type ipArtworkMaterialJob struct {
	id, role, choice, provider, imageID string
	selectionRevision                   int64
	revision, leaseGeneration, dueMS    int64
}

// The accepted source facts and the expected slot count. provider names the
// native job's origin only ("tmdb", "tvdb" or a locked upload); no provider
// vocabulary, enablement or consent is read here because no provider call is
// made. language/policyRevision are current-value witnesses re-derived by the
// writer from settings.
type ipArtworkMaterialSpec struct {
	provider, language                   string
	policyRevision                       int64
	intentID, sourceDigest, acceptedMIME string
	acceptedWidth, acceptedHeight        int64
	largeEdge, encoderRevision           int64
	expectedSlots                        int64
}

// One installed file. digest/mime/width/height/bytes are the caller's verified
// physical facts; the writer revalidates them and records them immutably.
type ipArtworkMaterialSlot struct {
	slot, width, height, bytes int64
	digest, mime               string
	outlier                    bool
}

// installed carries the intent's original generation/revision and the claimed
// job revision/generation so both CAS fencings are exact. Intent carries the
// claimable job row instead.
type ipArtworkMaterialRequest struct {
	owner                            ipArtworkMaterialOwner
	job                              ipArtworkMaterialJob
	spec                             ipArtworkMaterialSpec
	intentGeneration, intentRevision int64
	jobRevision, jobGeneration       int64
	slots                            []ipArtworkMaterialSlot
}

// The saved result is the durable intent head only: the manifest slots stay in
// their own table and are read back through the exact octet-preflighted point
// keys. Bytes is 0 before installation and the total installed bytes after.
type ipArtworkMaterialResult struct {
	IntentID      string
	Revision      int64
	Slots, Bytes  int64
	JobGeneration int64
}

type ipArtworkMaterialCommand struct {
	identity CommandIdentity
	request  ipArtworkMaterialRequest
	digest   RequestDigest
	valid    bool
}

type ipArtworkMaterialCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipArtworkMaterialResult
	Digest  ResultDigest
}

type ipArtworkMaterialOutcome struct {
	decision receiptDecision
	result   ipArtworkMaterialResult
	digest   ResultDigest
}

type ipArtworkMaterialSavedResult struct {
	Kind  CommandID
	Value ipArtworkMaterialResult
}

func (c *ipArtworkMaterialCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipArtworkMaterialKind(kind CommandID) bool {
	return kind == IPArtworkMaterialIntent || kind == IPArtworkMaterialInstalled
}

func ipArtworkMaterialKindName(kind CommandID) string {
	switch kind {
	case IPArtworkMaterialIntent:
		return "intent"
	case IPArtworkMaterialInstalled:
		return "installed"
	}
	return ""
}

// Frozen command_kind labels: the E enum has no material_intent/material_install
// value, so 223 stores "job_claim" (it claims the exact native job and declares
// the intent) and 224 stores "material_publish" (the material handoff family).
// IP-20's publish (225) reads its own receipt statement over the same table.
func ipArtworkMaterialCommandKind(kind CommandID) string {
	switch kind {
	case IPArtworkMaterialIntent:
		return "job_claim"
	case IPArtworkMaterialInstalled:
		return "job_renew"
	}
	return ""
}

func ipArtworkMaterialProvider(provider string) bool {
	switch provider {
	case "tmdb", "tvdb", "upload":
		return true
	}
	return false
}

func ipArtworkMaterialChoice(choice string) bool {
	return choice == "matched" || choice == "locked"
}

// Every string in the retained request must be bounded before any digest or
// hold: a malformed page is refused whole, never truncated.
func ipArtworkMaterialRequestShape(kind CommandID, identity CommandIdentity, q ipArtworkMaterialRequest) error {
	if !ipArtworkMaterialKind(kind) || !ipPolicyCommandIdentityValid(identity) {
		return ErrInvalid
	}
	o, s := q.owner, q.spec
	if o.id <= 0 || o.incarnation != identity.Incarnation || (o.entityID > 0) == (o.personID > 0) ||
		o.entityID < 0 || o.personID < 0 || o.sourceIncarnation == [16]byte{} || o.sourceRevision <= 0 ||
		o.revision <= 0 || o.leaseOwner <= 0 || o.leaseGeneration <= 0 || o.leaseUntilMS <= 0 ||
		!ipArtworkDiscoveryText(s.provider, 64) || !ipArtworkMaterialProvider(s.provider) ||
		!ipArtworkDiscoveryText(s.language, 64) || s.policyRevision <= 0 ||
		!ipArtworkDiscoveryText(s.intentID, ipArtworkMaterialMaxIntent) || s.intentID == "" ||
		!ipArtworkDiscoveryText(s.sourceDigest, 64) || s.acceptedWidth <= 0 || s.acceptedHeight <= 0 ||
		s.largeEdge != 1280 && s.largeEdge != 1920 && s.largeEdge != 2560 ||
		s.encoderRevision <= 0 || s.expectedSlots != 3 && s.expectedSlots != 4 {
		return ErrInvalid
	}
	if len(q.slots) > 0 && int64(len(q.slots)) > s.expectedSlots {
		return ErrInvalid
	}
	if kind == IPArtworkMaterialIntent {
		j := q.job
		if j.id != s.intentID || q.intentGeneration != 0 || q.intentRevision != 0 ||
			q.jobRevision != 0 || q.jobGeneration != 0 || len(q.slots) != 0 ||
			!ipArtworkDiscoveryText(j.role, 64) || !ipArtworkDiscoveryRole(j.role) ||
			!ipArtworkMaterialChoice(j.choice) || !ipArtworkMaterialProvider(j.provider) ||
			!ipArtworkDiscoveryText(j.imageID, ipArtworkMaterialMaxImage) || j.imageID == "" ||
			j.selectionRevision < 0 || j.revision <= 0 || j.leaseGeneration < 0 || j.dueMS < 0 {
			return ErrInvalid
		}
	} else {
		if q.job != (ipArtworkMaterialJob{}) || q.intentGeneration <= 0 || q.intentRevision != 1 ||
			q.jobRevision <= 1 || q.jobGeneration <= 0 ||
			int64(len(q.slots)) != s.expectedSlots {
			return ErrInvalid
		}
	}
	// One pure preparation validates the declaration and (when present) the
	// complete manifest against the accepted source; the writer revalidates the
	// same bytes again under its own hold.
	prepared, err := artwork.PrepareMaterial(ipArtworkMaterialPreparedSpec(s), ipArtworkMaterialPreparedSlots(q.slots),
		artwork.MaterialLimits{Slots: artwork.MaterialMaxSlots, TextBytes: 16 << 10,
			TotalBytes: ipArtworkMaterialMaxBytes})
	if err != nil || !prepared.Valid() {
		return ErrInvalid
	}
	return nil
}

func ipArtworkMaterialPreparedSpec(s ipArtworkMaterialSpec) artwork.MaterialSpec {
	return artwork.MaterialSpec{IntentID: s.intentID, SourceDigest: s.sourceDigest,
		AcceptedMIME: s.acceptedMIME, AcceptedWidth: s.acceptedWidth, AcceptedHeight: s.acceptedHeight,
		LargeEdge: artwork.MaterialEdge(s.largeEdge), EncoderRevision: s.encoderRevision,
		ExpectedSlots: s.expectedSlots}
}

func ipArtworkMaterialPreparedSlots(slots []ipArtworkMaterialSlot) []artwork.MaterialSlot {
	if len(slots) == 0 {
		return nil
	}
	out := make([]artwork.MaterialSlot, len(slots))
	for n, slot := range slots {
		out[n] = artwork.MaterialSlot{Slot: slot.slot, Digest: slot.digest, MIME: slot.mime,
			Width: slot.width, Height: slot.height, Bytes: slot.bytes, Outlier: slot.outlier}
	}
	return out
}

// Called under W preparation custody before any hold. No SQL, no allocation
// beyond the retained request copy and its canonical digest.
func newIPArtworkMaterialCommand(kind CommandID, identity CommandIdentity, in ipArtworkMaterialRequest) (*ipArtworkMaterialCommand, error) {
	if err := ipArtworkMaterialRequestShape(kind, identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipArtworkMaterialCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func ipArtworkMaterialJobKind(q ipArtworkMaterialRequest) (string, int64) {
	if q.owner.entityID > 0 {
		return "entity", q.owner.entityID
	}
	return "person", q.owner.personID
}
