package writer

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	MGClaimCapture              CommandID = 830
	MGRenewCapture              CommandID = 831
	MGCheckpointCapture         CommandID = 832
	MGCompleteCapture           CommandID = 833
	MGFailCapture               CommandID = 834
	MGAttachCapture             CommandID = 835
	MGDetachCapture             CommandID = 836
	MGReconcileCaptureFollowers CommandID = 837
)

// Cancel composes under the parent recording command; reserved physical IDs
// remain declarations until the trusted observation seam is implemented.
func mgCaptureDescriptors() []Descriptor {
	return []Descriptor{{MGClaimCapture, "mg.capture.claim", Normal}, {MGRenewCapture, "mg.capture.renew", Normal}, {MGAttachCapture, "mg.capture.attach", Normal}}
}

// These IDs identify native command/launch facts. They confer no physical proof.
type mgCaptureID [16]byte

func (v *mgCaptureID) Scan(value any) error {
	b, ok := value.([]byte)
	if !ok || len(b) != 16 {
		return fmt.Errorf("capture ID: %w", ErrInvalid)
	}
	copy(v[:], b)
	return nil
}

type mgCaptureDigest [32]byte

func (v *mgCaptureDigest) Scan(value any) error {
	b, ok := value.([]byte)
	if !ok || len(b) != 32 {
		return fmt.Errorf("capture digest: %w", ErrInvalid)
	}
	copy(v[:], b)
	return nil
}

type mgCaptureOptionalID struct {
	id    mgCaptureID
	valid bool
}

func (v *mgCaptureOptionalID) Scan(value any) error {
	*v = mgCaptureOptionalID{}
	if value == nil {
		return nil
	}
	if err := v.id.Scan(value); err != nil {
		return err
	}
	v.valid = true
	return nil
}
func (v mgCaptureOptionalID) value() any {
	if !v.valid {
		return nil
	}
	return v.id[:]
}

type mgCaptureNullableText struct {
	text  string
	valid bool
}

func (v *mgCaptureNullableText) Scan(value any) error {
	*v = mgCaptureNullableText{}
	if value == nil {
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return ErrInvalid
	}
	v.text, v.valid = s, true
	return nil
}
func (v mgCaptureNullableText) value() any {
	if !v.valid {
		return nil
	}
	return v.text
}

// Closed immutable result projection shared by live native reads and receipts.
// No current row, JSON checkpoint, or caller assertion reconstructs old results.
type mgCaptureSavedResult struct {
	state, reason                                                                       string
	claimGeneration, cancelGeneration                                                   int64
	token, ownerInstance                                                                string
	leaseMS                                                                             int64
	allocationID                                                                        string
	allocationGeneration                                                                int64
	allocationToken                                                                     string
	launch                                                                              mgCaptureOptionalID
	writerRetiredSequence, physicalExitSequence                                         int64
	exitLaunch                                                                          mgCaptureOptionalID
	exitClaimGeneration, exitCancelGeneration, sharedReadySequence                      int64
	sourceRevision                                                                      int64
	sourceFence                                                                         string
	itemID                                                                              int64
	artifactID, artifactDigest                                                          string
	bytes, capturedStartMS, capturedEndMS, gapCount, producerSucceeded, boundaryReached int64
	leaderID                                                                            mgCaptureNullableText
	shareClaimGeneration                                                                int64
	shareLaunch                                                                         mgCaptureOptionalID
	guidePin                                                                            mgCaptureNullableText
}

// Same-turn facts only. Parent owns current principal/source/rating/rule checks,
// original receipt/replay resolution, ACK/floors, resource admission and COMMIT.
type mgCaptureSnapshot struct {
	mgCaptureSavedResult
	id, ownerKey, authority, accountID, profileID                   string
	sourceID, channelID, programmeID, guideGeneration, ruleID       string
	ruleRevision, startMS, endMS, revision                          int64
	nextAttemptMS, recoveryAttempt, updatedMS, finishedMS, hasShare int64
	incarnation, createOperation                                    mgCaptureID
	birthDigest                                                     mgCaptureDigest
	memberIncarnation, profileLifetime                              string
	lastSequence, ackSequence                                       int64
}

type mgCaptureKind string

const (
	mgCaptureKindClaim      mgCaptureKind = "claim"
	mgCaptureKindRenew      mgCaptureKind = "renew"
	mgCaptureKindCancel     mgCaptureKind = "cancel"
	mgCaptureKindCheckpoint mgCaptureKind = "checkpoint"
	mgCaptureKindComplete   mgCaptureKind = "complete"
	mgCaptureKindFail       mgCaptureKind = "fail"
	mgCaptureKindAttach     mgCaptureKind = "attach"
	mgCaptureKindDetach     mgCaptureKind = "detach"
	mgCaptureKindRetire     mgCaptureKind = "retire"
)

func (k mgCaptureKind) valid() bool {
	switch k {
	case mgCaptureKindClaim, mgCaptureKindRenew, mgCaptureKindCancel, mgCaptureKindCheckpoint, mgCaptureKindComplete, mgCaptureKindFail, mgCaptureKindAttach, mgCaptureKindDetach, mgCaptureKindRetire:
		return true
	}
	return false
}

type mgCaptureCommand struct {
	recordingID   string
	incarnation   mgCaptureID
	sequence      int64
	operation     mgCaptureID
	requestDigest mgCaptureDigest
	kind          mgCaptureKind
}

func (c mgCaptureCommand) valid() bool {
	return mgCaptureText(c.recordingID, true) && c.incarnation != (mgCaptureID{}) && c.sequence > 0 && c.operation != (mgCaptureID{}) && c.requestDigest != (mgCaptureDigest{}) && c.kind.valid()
}
func (c mgCaptureCommand) next(s mgCaptureSnapshot, kind mgCaptureKind) bool {
	return c.valid() && c.recordingID == s.id && c.incarnation == s.incarnation && c.kind == kind && s.lastSequence == s.ackSequence && s.lastSequence < math.MaxInt64 && c.sequence == s.lastSequence+1
}

type mgCaptureClaimInput struct {
	token, ownerInstance, artifactID string
	allocation                       mgCaptureAllocationFence
	leaseMS                          int64
	launchID                         mgCaptureID
	sourceRevision                   int64
	sourceFence                      string
}
type mgCaptureAllocationFence struct {
	id, token           string
	generation, leaseMS int64
}
type mgCaptureRetirement uint8

const (
	mgCaptureNoPhysicalClaim mgCaptureRetirement = iota + 1
	mgCaptureRetirementPending
	mgCaptureSharedHold
)

type mgCaptureFacts struct {
	recording  mgCaptureSnapshot
	retirement mgCaptureRetirement
	command    mgCaptureCommand
}

func mgCaptureFact(s mgCaptureSnapshot, c mgCaptureCommand) mgCaptureFacts {
	retirement := mgCaptureNoPhysicalClaim
	if s.hasShare == 1 || s.leaderID.valid {
		retirement = mgCaptureSharedHold
	}
	if s.launch.valid || s.claimGeneration != 0 || s.token != "" || s.ownerInstance != "" || s.allocationID != "" || s.leaseMS != 0 {
		retirement = mgCaptureRetirementPending
	}
	return mgCaptureFacts{s, retirement, c}
}

// Physical observation issuance/consumption stays entirely in the parent's core
// frame. No executable checkpoint/writer-retirement/shared-ready/physical-exit,
// reclaim, complete or detach body is exposed here. Exact typed draft columns
// are read/preserved/receipted, but neither lease expiry nor a matching scalar
// launch/token/PID/caller boolean is physical exclusion. No effect clears guide
// pins, allocation custody, exit evidence or shared-ready/gap/producer evidence.
func mgCaptureText(s string, required bool) bool {
	return (!required || s != "") && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func (r mgCaptureSavedResult) valid(sequence int64) bool {
	switch r.state {
	case "scheduled", "conflicted", "waiting-source", "waiting-guide", "preparing", "recording", "finalizing", "completed", "incomplete-playable", "cancelled", "pending-delete", "deleted", "failed":
	default:
		return false
	}
	if (!r.launch.valid && r.launch.id != (mgCaptureID{})) || (!r.exitLaunch.valid && r.exitLaunch.id != (mgCaptureID{})) || (!r.shareLaunch.valid && r.shareLaunch.id != (mgCaptureID{})) || (!r.guidePin.valid && r.guidePin.text != "") {
		return false
	}
	for _, s := range []string{r.state, r.reason, r.token, r.ownerInstance, r.allocationID, r.allocationToken, r.sourceFence, r.artifactID, r.artifactDigest, r.leaderID.text, r.guidePin.text} {
		if !mgCaptureText(s, false) {
			return false
		}
	}
	if r.claimGeneration < 0 || r.claimGeneration >= math.MaxInt64 || r.cancelGeneration < 0 || r.cancelGeneration >= math.MaxInt64 || r.leaseMS < 0 || r.allocationGeneration < 0 || r.sourceRevision < 0 || r.itemID < 0 || r.bytes < 0 || r.gapCount < 0 {
		return false
	}
	for _, s := range []int64{r.writerRetiredSequence, r.physicalExitSequence, r.sharedReadySequence} {
		if s < 0 || s > sequence {
			return false
		}
	}
	if r.exitClaimGeneration < 0 || r.exitCancelGeneration < 0 || r.shareClaimGeneration < 0 || (r.producerSucceeded != 0 && r.producerSucceeded != 1) || (r.boundaryReached != 0 && r.boundaryReached != 1) {
		return false
	}
	if r.allocationGeneration > 0 && (r.allocationID == "" || r.allocationToken == "" || !r.launch.valid) {
		return false
	}
	if !r.guidePin.valid && r.launch.valid && r.physicalExitSequence == 0 {
		return false
	}
	if r.physicalExitSequence == 0 {
		if r.exitLaunch.valid || r.exitClaimGeneration != 0 || r.exitCancelGeneration != 0 {
			return false
		}
	} else if !r.launch.valid || r.exitLaunch != r.launch || r.exitClaimGeneration != r.claimGeneration || r.exitCancelGeneration > r.cancelGeneration || r.writerRetiredSequence == 0 || r.writerRetiredSequence > r.physicalExitSequence {
		return false
	}
	if r.sharedReadySequence > 0 && (r.writerRetiredSequence == 0 || r.writerRetiredSequence > r.sharedReadySequence || r.artifactID == "" || r.artifactDigest == "" || r.bytes <= 0 || r.capturedEndMS <= r.capturedStartMS || r.sourceRevision <= 0 || r.sourceFence == "") {
		return false
	}
	return (r.leaderID.valid && r.leaderID.text != "" && r.shareClaimGeneration > 0 && r.shareLaunch.valid) || (!r.leaderID.valid && r.leaderID.text == "" && r.shareClaimGeneration == 0 && !r.shareLaunch.valid)
}
func (s mgCaptureSnapshot) valid() bool {
	for _, v := range []string{s.id, s.ownerKey, s.authority, s.sourceID, s.channelID, s.programmeID, s.guideGeneration, s.state} {
		if !mgCaptureText(v, true) {
			return false
		}
	}
	for _, v := range []string{s.accountID, s.profileID, s.ruleID} {
		if !mgCaptureText(v, false) {
			return false
		}
	}
	return s.mgCaptureSavedResult.valid(s.lastSequence) && s.incarnation != (mgCaptureID{}) && s.createOperation != (mgCaptureID{}) && s.birthDigest != (mgCaptureDigest{}) && len(s.memberIncarnation) == 32 && mgCaptureText(s.memberIncarnation, true) && len(s.profileLifetime) == 32 && mgCaptureText(s.profileLifetime, true) && s.lastSequence >= 0 && s.ackSequence >= 0 && s.ackSequence <= s.lastSequence && s.lastSequence < math.MaxInt64 && s.revision > 0 && s.revision < math.MaxInt64 && s.ruleRevision >= 0 && s.startMS >= 0 && s.endMS > s.startMS && s.nextAttemptMS >= 0 && s.recoveryAttempt >= 0 && s.updatedMS >= 0 && s.finishedMS >= 0 && ((s.hasShare == 0 && !s.leaderID.valid) || (s.hasShare == 1 && s.leaderID.valid)) && (!s.guidePin.valid || s.guidePin.text == s.guideGeneration)
}
func (s mgCaptureSnapshot) unshared() bool { return s.hasShare == 0 && !s.leaderID.valid }
func (s mgCaptureSnapshot) fresh() bool {
	return s.state == "scheduled" && s.claimGeneration == 0 && s.cancelGeneration == 0 && s.token == "" && s.ownerInstance == "" && s.leaseMS == 0 && s.allocationID == "" && s.artifactID == "" && s.itemID == 0 && s.recoveryAttempt == 0 && s.finishedMS == 0 && s.unshared() && !s.launch.valid && s.allocationGeneration == 0 && s.allocationToken == "" && s.sourceRevision == 0 && s.sourceFence == "" && s.writerRetiredSequence == 0 && s.physicalExitSequence == 0 && s.sharedReadySequence == 0 && s.guidePin.valid && s.guidePin.text == s.guideGeneration
}
func (s mgCaptureSnapshot) capturing() bool {
	return s.state == "preparing" || s.state == "recording" || s.state == "finalizing"
}
func (s mgCaptureSnapshot) mutable() bool {
	return s.capturing() || s.state == "scheduled" || s.state == "conflicted" || s.state == "waiting-source" || s.state == "waiting-guide"
}

// SQL NULL comparison lengths only; no physical-proof flags.
func (v mgCaptureOptionalID) length() int64 {
	if !v.valid {
		return -1
	}
	return 16
}
func (v mgCaptureNullableText) length() int64 {
	if !v.valid {
		return -1
	}
	return int64(utf8.RuneCountInString(v.text))
}
