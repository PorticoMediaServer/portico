package writer

import (
	"math"
)

// MG-13 captured-custody claim, renewal and checkpoint. 830 claims one
// recording occurrence for an actual launch, 831 extends that exact claim's
// logical lease and 832 records native captured progress. None of them starts a
// worker, acquires/releases a tuner allocation, performs provider or filesystem
// IO, stores an immutable artifact, retires the writer or proves a physical
// exit: those remain MG-14/15/16 custody on the same recording namespace.
const (
	MGCaptureClaim      CommandID = 830
	MGRenewCapture      CommandID = 831
	MGCheckpointCapture CommandID = 832
)

// NORMAL logical custody only. A FULL, foreign or unverified frame cannot borrow
// these purposes, and no caller boolean selects a command.
func mgCaptureDescriptors() []Descriptor {
	return []Descriptor{
		{MGCaptureClaim, "mg.capture.claim", Normal},
		{MGRenewCapture, "mg.capture.renew", Normal},
		{MGCheckpointCapture, "mg.capture.checkpoint", Normal},
	}
}

// The launch is a 16-byte native launch identity. It is comparison data, never
// a capability: the allocation fence and the recording row must agree with it
// in the same turn.
type mgCaptureLaunch [16]byte

func (v mgCaptureLaunch) valid() bool { return v != (mgCaptureLaunch{}) }

type mgCaptureKind string

const (
	mgCaptureKindClaim      mgCaptureKind = "claim"
	mgCaptureKindRenew      mgCaptureKind = "renew"
	mgCaptureKindCheckpoint mgCaptureKind = "checkpoint"
)

func (k mgCaptureKind) valid() bool {
	switch k {
	case mgCaptureKindClaim, mgCaptureKindRenew, mgCaptureKindCheckpoint:
		return true
	}
	return false
}

// One typed command binds the original recording identity and immutable request
// digest. The row's command incarnation is its birth incarnation; no capture
// command renames that namespace.
type mgCaptureCommand struct {
	recordingID string
	identity    CommandIdentity
	kind        mgCaptureKind
	digest      RequestDigest
}

func (c mgCaptureCommand) valid() bool {
	return mgRecordingHex(c.recordingID, 64) && mgRecordingIdentityValid(c.identity) &&
		c.kind.valid() && c.digest != (RequestDigest{})
}

// The recording row has its own capture sequence, advanced one per accepted
// capture command. It is not the root receipt sequence; the fence carries the
// exact pre-effect value, so a concurrent or stale writer refuses.
func (c mgCaptureCommand) next(s mgCaptureRow) bool {
	return c.valid() && c.recordingID == s.id && c.identity.Incarnation == [16]byte(s.incarnation) &&
		s.lastSequence == s.ackSequence && s.lastSequence < math.MaxInt64
}

// The tuner allocation is another command's custody. This witness re-checks the
// exact current row; it neither acquires nor releases it.
type mgCaptureAllocationFence struct {
	id, token  string
	generation int64
	leaseMS    int64
}

func (a mgCaptureAllocationFence) valid() bool {
	return mgCaptureText(a.id) && mgCaptureText(a.token) && a.generation > 0 && a.leaseMS > 0
}

type mgCaptureAllocationIdentity struct {
	id, token  string
	generation int64
}

func (a mgCaptureAllocationIdentity) valid() bool {
	return mgCaptureText(a.id) && mgCaptureText(a.token) && a.generation > 0
}

type mgCaptureClaimRequest struct {
	owner          mgRecordingOwner
	id             string
	expected       int64
	token          string
	ownerInstance  string
	artifactID     string
	allocation     mgCaptureAllocationFence
	leaseMS        int64
	launch         mgCaptureLaunch
	sourceRevision int64
	sourceFence    string
}

type mgCaptureRenewRequest struct {
	owner           mgRecordingOwner
	id              string
	expected        int64
	claimGeneration int64
	token           string
	ownerInstance   string
	launch          mgCaptureLaunch
	allocation      mgCaptureAllocationFence
	leaseMS         int64
}

type mgCaptureCheckpointRequest struct {
	owner             mgRecordingOwner
	id                string
	expected          int64
	claimGeneration   int64
	token             string
	ownerInstance     string
	launch            mgCaptureLaunch
	allocation        mgCaptureAllocationIdentity
	capturedStartMS   int64
	capturedEndMS     int64
	gapCount          int64
	producerSucceeded bool
	boundaryReached   bool
}

const mgCaptureRequestVersion uint16 = 1

func (r mgCaptureClaimRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.id, 64) || r.expected < 1 || !mgCaptureText(r.token) ||
		!mgCaptureText(r.ownerInstance) || !mgCaptureText(r.artifactID) || !r.allocation.valid() ||
		r.leaseMS <= 0 || !r.launch.valid() || r.sourceRevision < 1 || !mgCaptureFence(r.sourceFence) {
		return ErrInvalid
	}
	return nil
}

func (r mgCaptureRenewRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.id, 64) || r.expected < 1 || r.claimGeneration < 1 ||
		!mgCaptureText(r.token) || !mgCaptureText(r.ownerInstance) || !r.launch.valid() ||
		!r.allocation.valid() || r.leaseMS <= 0 {
		return ErrInvalid
	}
	return nil
}

func (r mgCaptureCheckpointRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.id, 64) || r.expected < 1 || r.claimGeneration < 1 ||
		!mgCaptureText(r.token) || !mgCaptureText(r.ownerInstance) || !r.launch.valid() ||
		!r.allocation.valid() || r.capturedStartMS < 0 || r.capturedEndMS < 0 || r.gapCount < 0 {
		return ErrInvalid
	}
	// Only native progress scalars are accepted. There is deliberately no
	// writer-retired, physical-exit, allocation or artifact assertion here; a
	// caller boolean or callback cannot mint one.
	if r.capturedStartMS == 0 && r.capturedEndMS != 0 {
		return ErrInvalid
	}
	if r.capturedStartMS != 0 && r.capturedEndMS != 0 && r.capturedEndMS < r.capturedStartMS {
		return ErrInvalid
	}
	return nil
}

type mgCaptureClaimCommand struct {
	identity CommandIdentity
	request  mgCaptureClaimRequest
	digest   RequestDigest
	valid    bool
}
type mgCaptureRenewCommand struct {
	identity CommandIdentity
	request  mgCaptureRenewRequest
	digest   RequestDigest
	valid    bool
}
type mgCaptureCheckpointCommand struct {
	identity CommandIdentity
	request  mgCaptureCheckpointRequest
	digest   RequestDigest
	valid    bool
}

func newMGCaptureClaimCommand(id CommandIdentity, r mgCaptureClaimRequest) (*mgCaptureClaimCommand, error) {
	if !mgRecordingIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGCaptureClaim, mgCaptureRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgCaptureClaimCommand{identity: id, request: r, digest: d, valid: true}, nil
}

func newMGCaptureRenewCommand(id CommandIdentity, r mgCaptureRenewRequest) (*mgCaptureRenewCommand, error) {
	if !mgRecordingIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGRenewCapture, mgCaptureRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgCaptureRenewCommand{identity: id, request: r, digest: d, valid: true}, nil
}

func newMGCaptureCheckpointCommand(id CommandIdentity, r mgCaptureCheckpointRequest) (*mgCaptureCheckpointCommand, error) {
	if !mgRecordingIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGCheckpointCapture, mgCaptureRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgCaptureCheckpointCommand{identity: id, request: r, digest: d, valid: true}, nil
}

func (c *mgCaptureClaimCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGCaptureClaimCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}
func (c *mgCaptureRenewCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGCaptureRenewCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}
func (c *mgCaptureCheckpointCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGCaptureCheckpointCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func (c *mgCaptureClaimCommand) command() mgCaptureCommand {
	return mgCaptureCommand{c.request.id, c.identity, mgCaptureKindClaim, c.digest}
}
func (c *mgCaptureRenewCommand) command() mgCaptureCommand {
	return mgCaptureCommand{c.request.id, c.identity, mgCaptureKindRenew, c.digest}
}
func (c *mgCaptureCheckpointCommand) command() mgCaptureCommand {
	return mgCaptureCommand{c.request.id, c.identity, mgCaptureKindCheckpoint, c.digest}
}

// One exact native dvr_recordings capture row. Every field is a same-turn
// comparison fact, never authority, a grant or physical evidence.
type mgCaptureRow struct {
	id, ownerKey, authority, account, profile, source, channel, programme, generation string
	state, reason                                                                     string
	cancelGeneration, claimGeneration                                                 int64
	token, ownerInstance                                                              string
	leaseMS                                                                           int64
	allocationID                                                                      string
	artifactID, artifactDigest                                                        string
	launch                                                                            mgRecordingOptionalID
	allocationGeneration                                                              int64
	allocationToken                                                                   string
	sourceRevision                                                                    int64
	sourceFence                                                                       string
	writerRetiredSequence, physicalExitSequence                                       int64
	exitLaunch                                                                        mgRecordingOptionalID
	exitClaimGeneration, exitCancelGeneration                                         int64
	sharedReadySequence                                                               int64
	capturedStartMS, capturedEndMS, gapCount                                          int64
	producerSucceeded, boundaryReached                                                int64
	shares                                                                            int64
	shareLeader                                                                       mgRecordingNullableText
	shareClaimGeneration                                                              int64
	shareLaunch                                                                       mgRecordingOptionalID
	guidePin                                                                          mgRecordingNullableText
	startMS, endMS, revision, nextAttemptMS, recoveryAttempt                          int64
	updatedMS, finishedMS                                                             int64
	lastSequence, ackSequence                                                         int64
	incarnation, operation                                                            mgRecordingID16
	birth                                                                             mgRecordingDigest32
	member, lifetime                                                                  string
	ruleID                                                                            string
	ruleRevision                                                                      int64
}

func (r mgCaptureRow) valid() bool {
	if !mgRecordingHex(r.id, 64) || !mgRecordingHex(r.ownerKey, 64) || r.authority != "local" ||
		!mgRecordingText(r.account, true, 256) || !mgRecordingText(r.profile, true, 256) ||
		!mgRecordingHex(r.source, 48) || !mgRecordingHex(r.channel, 64) || !mgRecordingHex(r.programme, 64) ||
		!mgRecordingHex(r.generation, 48) || !mgRecordingRevisionValid(r.revision) ||
		!mgRecordingHex(r.member, 32) || !mgRecordingHex(r.lifetime, 32) ||
		r.incarnation == (mgRecordingID16{}) || r.operation == (mgRecordingID16{}) || r.birth == (mgRecordingDigest32{}) ||
		r.startMS < 0 || r.endMS <= r.startMS || r.nextAttemptMS < 0 || r.recoveryAttempt < 0 ||
		r.updatedMS < 0 || r.finishedMS < 0 || r.lastSequence < 0 || r.ackSequence < 0 || r.ackSequence > r.lastSequence ||
		r.cancelGeneration < 0 || r.claimGeneration < 0 || r.leaseMS < 0 ||
		r.allocationGeneration < 0 || r.sourceRevision < 0 || r.writerRetiredSequence < 0 ||
		r.physicalExitSequence < 0 || r.exitClaimGeneration < 0 || r.exitCancelGeneration < 0 ||
		r.sharedReadySequence < 0 || r.capturedStartMS < 0 || r.capturedEndMS < 0 || r.gapCount < 0 ||
		(r.producerSucceeded != 0 && r.producerSucceeded != 1) || (r.boundaryReached != 0 && r.boundaryReached != 1) ||
		(r.shares != 0 && r.shares != 1) || !mgRecordingText(r.ruleID, false, 256) || r.ruleRevision < 0 {
		return false
	}
	switch r.state {
	case "scheduled", "conflicted", "waiting-source", "waiting-guide", "preparing", "recording", "finalizing", "completed", "incomplete-playable", "cancelled", "pending-delete", "deleted", "failed":
	default:
		return false
	}
	if !mgRecordingText(r.token, false, 256) || !mgRecordingText(r.ownerInstance, false, 256) ||
		!mgRecordingText(r.allocationID, false, 256) || !mgRecordingText(r.allocationToken, false, 256) ||
		!mgRecordingText(r.artifactID, false, 256) || !mgRecordingText(r.artifactDigest, false, 256) ||
		!mgCaptureFence(r.sourceFence) && r.sourceFence != "" {
		return false
	}
	if !r.guidePin.valid || r.guidePin.text != r.generation {
		return false
	}
	if r.shares == 1 != r.shareLeader.valid {
		return false
	}
	if r.shareLeader.valid && (!r.shareLaunch.valid || r.shareClaimGeneration <= 0) ||
		!r.shareLeader.valid && (r.shareLaunch.valid || r.shareClaimGeneration != 0) {
		return false
	}
	if r.allocationID != "" && (r.allocationGeneration <= 0 || r.allocationToken == "" || !r.launch.valid) {
		return false
	}
	if r.capturedEndMS != 0 && (r.capturedStartMS == 0 || r.capturedEndMS < r.capturedStartMS) {
		return false
	}
	for _, s := range []int64{r.writerRetiredSequence, r.physicalExitSequence, r.sharedReadySequence} {
		if s > r.lastSequence {
			return false
		}
	}
	if r.physicalExitSequence == 0 {
		if r.exitLaunch.valid || r.exitClaimGeneration != 0 || r.exitCancelGeneration != 0 {
			return false
		}
	} else if !r.launch.valid || r.exitLaunch != r.launch || r.exitClaimGeneration != r.claimGeneration ||
		r.exitCancelGeneration > r.cancelGeneration || r.writerRetiredSequence == 0 ||
		r.writerRetiredSequence > r.physicalExitSequence {
		return false
	}
	if r.sharedReadySequence > 0 && (r.writerRetiredSequence == 0 || r.writerRetiredSequence > r.sharedReadySequence ||
		r.artifactID == "" || r.artifactDigest == "" || r.sourceRevision <= 0 || r.sourceFence == "") {
		return false
	}
	return true
}

// A never-launched logical intent is the only row a claim may take: no prior
// claim/lease/allocation/artifact/share/retirement evidence exists and the
// guide generation is still pinned.
func (r mgCaptureRow) fresh() bool {
	return (r.state == "scheduled" || r.state == "conflicted" || r.state == "waiting-source" || r.state == "waiting-guide") &&
		r.claimGeneration == 0 && r.cancelGeneration == 0 && r.token == "" && r.ownerInstance == "" &&
		r.leaseMS == 0 && r.allocationID == "" && r.artifactID == "" && r.artifactDigest == "" &&
		r.shares == 0 && !r.shareLeader.valid && !r.launch.valid && r.allocationGeneration == 0 &&
		r.allocationToken == "" && r.sourceRevision == 0 && r.sourceFence == "" &&
		r.writerRetiredSequence == 0 && r.physicalExitSequence == 0 && r.sharedReadySequence == 0 &&
		r.capturedStartMS == 0 && r.capturedEndMS == 0 && r.gapCount == 0 &&
		r.producerSucceeded == 0 && r.boundaryReached == 0 && r.recoveryAttempt == 0 && r.finishedMS == 0 &&
		r.guidePin.valid && r.guidePin.text == r.generation
}

func (r mgCaptureRow) capturing() bool {
	return r.state == "preparing" || r.state == "recording" || r.state == "finalizing"
}

func mgCaptureOptionalValue(v mgRecordingOptionalID) any {
	if !v.valid {
		return nil
	}
	return v.id[:]
}

func mgCaptureNullableValue(v mgRecordingNullableText) any {
	if !v.valid {
		return nil
	}
	return v.text
}

func (r mgCaptureRow) unshared() bool { return r.shares == 0 && !r.shareLeader.valid }

// The row must belong to the exact frozen owner tuple. Possession of a valid
// recording id is never authority to claim another owner's occurrence.
func (r mgCaptureRow) ownerIs(o mgRecordingOwner) bool {
	return o.valid() && r.ownerKey == o.key() && r.authority == "local" &&
		r.account == o.account && r.profile == o.profile &&
		r.member == o.memberIncarnation && r.lifetime == o.profileLifetime
}

// The exact claim witness a renewal or checkpoint must present. A stale worker
// that lost its claim generation, token, instance or launch is refused even if
// it knows the current revision.
func (r mgCaptureRow) claimIs(claimGeneration int64, token, instance string, launch mgCaptureLaunch) bool {
	return claimGeneration > 0 && claimGeneration == r.claimGeneration && token != "" && token == r.token &&
		instance != "" && instance == r.ownerInstance && launch.valid() && r.launch.valid && [16]byte(r.launch.id) == [16]byte(launch)
}

func (r mgCaptureRow) allocationIs(a mgCaptureAllocationIdentity) bool {
	return a.valid() && a.id == r.allocationID && a.generation == r.allocationGeneration && a.token == r.allocationToken
}

// The fence is the exact pre-effect capture tuple. Any concurrent claim,
// renewal, cancellation, allocation, source or exit advance refuses the write.
func (r mgCaptureRow) fenceValues() []any {
	return []any{r.id, r.ownerKey, r.revision, r.state, r.token, r.claimGeneration, r.cancelGeneration,
		r.ownerInstance, r.leaseMS, r.incarnation[:], r.lastSequence, r.ackSequence,
		mgCaptureOptionalValue(r.launch), r.allocationID, r.allocationGeneration, r.allocationToken,
		r.sourceRevision, r.sourceFence, r.writerRetiredSequence, r.physicalExitSequence,
		r.sharedReadySequence, r.shareClaimGeneration, mgCaptureOptionalValue(r.shareLaunch), mgCaptureNullableValue(r.guidePin)}
}

// The saved result is the exact post-effect capture custody scalar: identity,
// claim/lease/allocation/launch/source tuple, native captured progress and the
// row revision/sequence. It is not a current DTO, an artifact, a physical exit
// or a byte refund.
type mgCaptureResult struct {
	id, owner, source, channel, programme, generation, state                     string
	token, ownerInstance, allocationID, allocationToken, sourceFence             string
	artifactID                                                                   string
	launch                                                                       mgCaptureLaunch
	claimGeneration, cancelGeneration, leaseMS, allocationGeneration             int64
	sourceRevision, revision, sequence                                           int64
	capturedStartMS, capturedEndMS, gapCount, producerSucceeded, boundaryReached int64
}

func (r mgCaptureResult) valid() bool {
	for _, s := range []string{r.id, r.owner, r.source, r.channel, r.programme, r.generation, r.state} {
		if !mgRecordingText(s, true, 256) {
			return false
		}
	}
	if !mgRecordingHex(r.id, 64) || !mgRecordingHex(r.owner, 64) || !mgRecordingHex(r.source, 48) ||
		!mgRecordingHex(r.channel, 64) || !mgRecordingHex(r.programme, 64) || !mgRecordingHex(r.generation, 48) {
		return false
	}
	switch r.state {
	case "scheduled", "conflicted", "waiting-source", "waiting-guide", "preparing", "recording", "finalizing", "completed", "incomplete-playable", "cancelled", "pending-delete", "deleted", "failed":
	default:
		return false
	}
	for _, s := range []string{r.token, r.ownerInstance, r.allocationID, r.allocationToken, r.artifactID} {
		if !mgCaptureText(s) {
			return false
		}
	}
	if !mgCaptureFence(r.sourceFence) || !r.launch.valid() ||
		r.claimGeneration < 1 || r.cancelGeneration < 0 || r.leaseMS <= 0 ||
		r.allocationGeneration < 1 || r.sourceRevision < 1 || r.revision < 1 || r.revision >= math.MaxInt64 ||
		r.sequence < 1 || r.capturedStartMS < 0 || r.capturedEndMS < 0 || r.gapCount < 0 ||
		r.capturedStartMS == 0 && r.capturedEndMS != 0 ||
		r.capturedEndMS != 0 && r.capturedEndMS < r.capturedStartMS ||
		(r.producerSucceeded != 0 && r.producerSucceeded != 1) ||
		(r.boundaryReached != 0 && r.boundaryReached != 1) {
		return false
	}
	return r.token != "" && r.ownerInstance != "" && r.allocationID != "" && r.allocationToken != "" && r.artifactID != ""
}

type mgCaptureFacts struct {
	row     mgCaptureRow
	command mgCaptureCommand
}

func (r mgCaptureRow) result() mgCaptureResult {
	out := mgCaptureResult{
		id: r.id, owner: r.ownerKey, source: r.source, channel: r.channel, programme: r.programme,
		generation: r.generation, state: r.state, token: r.token, ownerInstance: r.ownerInstance,
		allocationID: r.allocationID, allocationToken: r.allocationToken, sourceFence: r.sourceFence,
		artifactID: r.artifactID, claimGeneration: r.claimGeneration, cancelGeneration: r.cancelGeneration,
		leaseMS: r.leaseMS, allocationGeneration: r.allocationGeneration, sourceRevision: r.sourceRevision,
		revision: r.revision, sequence: r.lastSequence, capturedStartMS: r.capturedStartMS,
		capturedEndMS: r.capturedEndMS, gapCount: r.gapCount, producerSucceeded: r.producerSucceeded,
		boundaryReached: r.boundaryReached,
	}
	if r.launch.valid {
		out.launch = mgCaptureLaunch(r.launch.id)
	}
	return out
}

// Bounded native capture text and native source-fence shapes. The fence is an
// opaque generation/revision token stamped by the parent, never caller proof of
// physical capture.
func mgCaptureText(s string) bool { return mgRecordingText(s, true, 256) }

func mgCaptureFence(s string) bool {
	if len(s) != 32 && len(s) != 48 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Progress may only advance. The first captured instant is immutable once set,
// the last never moves backwards, gaps never shrink and a producer/boundary fact
// that happened is never cleared by a later callback.
func (r mgCaptureRow) progressAdvances(in mgCaptureCheckpointRequest) bool {
	if in.capturedStartMS != 0 && r.capturedStartMS != 0 && in.capturedStartMS != r.capturedStartMS {
		return false
	}
	if r.capturedEndMS != 0 && in.capturedEndMS < r.capturedEndMS {
		return false
	}
	if in.gapCount < r.gapCount {
		return false
	}
	if r.producerSucceeded == 1 && !in.producerSucceeded {
		return false
	}
	if r.boundaryReached == 1 && !in.boundaryReached {
		return false
	}
	return true
}
