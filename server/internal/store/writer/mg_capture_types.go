package writer

import (
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

// Only the existing-column effects implemented here are offered for aggregation.
// Cancel is composed by the parent's recording-cancel/security command. Reserved
// checkpoint/complete/fail/detach/reconcile IDs have no executable recipe yet.
func mgCaptureDescriptors() []Descriptor {
	return []Descriptor{
		{MGClaimCapture, "mg.capture.claim", Normal},
		{MGRenewCapture, "mg.capture.renew", Normal},
		{MGAttachCapture, "mg.capture.attach", Normal},
	}
}

// Native point facts, never an authorization grant or an Applied response. The
// parent must reauthorize the exact owner/source/channel/programme/rule in its
// core frame, including current rating restrictions for BOTH share participants.
// The helper then rechecks these facts on that SAME commandTurn. It never opens
// a writer, performs IO, saves a receipt or decides COMMIT/replay truth.
type mgCaptureSnapshot struct {
	id, ownerKey, authority, accountID, profileID     string
	sourceID, channelID, programmeID, guideGeneration string
	ruleID                                            string
	ruleRevision, startMS, endMS, revision            int64
	state, reason                                     string
	claimGeneration, cancelGeneration                 int64
	token, ownerInstance                              string
	leaseMS                                           int64
	allocationID, artifactID                          string
	itemID, nextAttemptMS, recoveryAttempt            int64
	updatedMS, finishedMS                             int64
	hasShare                                          int64
	leaderID                                          mgCaptureNullableText
}

type mgCaptureNullableText struct {
	text  string
	valid bool
}

type mgCaptureClaimInput struct {
	token, ownerInstance, artifactID string
	allocation                       mgCaptureAllocationFence
	leaseMS                          int64
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
}

func mgCaptureFact(s mgCaptureSnapshot) mgCaptureFacts {
	retirement := mgCaptureNoPhysicalClaim
	if s.hasShare == 1 || s.leaderID.valid {
		retirement = mgCaptureSharedHold
	}
	if s.claimGeneration != 0 || s.token != "" || s.ownerInstance != "" || s.allocationID != "" || s.leaseMS != 0 {
		retirement = mgCaptureRetirementPending
	}
	return mgCaptureFacts{s, retirement}
}

// No retirement/reclaim/complete/detach effect exists against today's columns.
// Required typed scalar evidence on the recording, owned by the parent's DDL:
// physical launch ID + recording claim token/generation + owner instance;
// allocation ID/token/generation; retirement sequence + exact cancel generation;
// writer-retired sequence and physical-exclusion witness ID bound to that launch;
// artifact ID/digest/size and shared-ready sequence. Acceptance must consume a
// trusted native physical-exclusion observation under exact fences, never lease
// expiry, checkpoint_json, a PID or a caller-provided WriterRetired boolean.
// Cancellation preserves every custody field and every allocation/resource debt.

func mgCaptureText(s string, required bool) bool {
	return (!required || s != "") && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func (s mgCaptureSnapshot) valid() bool {
	for _, v := range []string{s.id, s.ownerKey, s.authority, s.sourceID, s.channelID, s.programmeID, s.guideGeneration, s.state} {
		if !mgCaptureText(v, true) {
			return false
		}
	}
	for _, v := range []string{s.accountID, s.profileID, s.ruleID, s.reason, s.token, s.ownerInstance, s.allocationID, s.artifactID, s.leaderID.text} {
		if !mgCaptureText(v, false) {
			return false
		}
	}
	return s.revision > 0 && s.revision < math.MaxInt64 && s.claimGeneration >= 0 && s.claimGeneration < math.MaxInt64 &&
		s.cancelGeneration >= 0 && s.cancelGeneration < math.MaxInt64 && s.ruleRevision >= 0 && s.leaseMS >= 0 &&
		s.startMS >= 0 && s.endMS > s.startMS && s.nextAttemptMS >= 0 && s.recoveryAttempt >= 0 && s.itemID >= 0 && s.updatedMS >= 0 && s.finishedMS >= 0 &&
		(s.hasShare == 0 || s.hasShare == 1) && (s.leaderID.valid || s.leaderID.text == "")
}
func (s mgCaptureSnapshot) unshared() bool { return s.hasShare == 0 && !s.leaderID.valid }
func (s mgCaptureSnapshot) fresh() bool {
	return s.state == "scheduled" && s.claimGeneration == 0 && s.cancelGeneration == 0 && s.token == "" &&
		s.ownerInstance == "" && s.leaseMS == 0 && s.allocationID == "" && s.artifactID == "" && s.itemID == 0 && s.recoveryAttempt == 0 && s.finishedMS == 0 && s.unshared()
}
func (s mgCaptureSnapshot) capturing() bool {
	return s.state == "preparing" || s.state == "recording" || s.state == "finalizing"
}
func (s mgCaptureSnapshot) mutable() bool {
	return s.capturing() || s.state == "scheduled" || s.state == "conflicted" || s.state == "waiting-source" || s.state == "waiting-guide"
}
