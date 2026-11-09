package writer

import (
	"unicode/utf8"
)

// ipPolicyResult is the COMPLETE immutable saved policy value. The original
// command identity and request digest live in the common receipt envelope; the
// current mutable policy row never reconstructs it. ResultCount is always one
// because this family accepts exactly one policy owner row per command.
type ipPolicyResult struct {
	PolicyIncarnation                        [16]byte
	AcceptedRevision, AppliedMS, ResultCount int64
	Enabled, CreditEnabled                   bool
	Priority                                 int64
	Region, Algorithm, RefreshMode           string
	CreditAfterItem                          *int64
	CreditAfterProvider                      string
}

// ipRefreshResult is one durable sweep request. ScheduledCount is zero because
// requesting a sweep never schedules a page; a later page command owns that
// count. Generation/revision/epoch are the native POST-CAS values.
type ipRefreshResult struct {
	Provider, Purpose, Phase                       string
	Generation, AcceptedRevision, EpochMS, AfterID int64
	ScheduledCount, AppliedMS, ResultCount         int64
	PageWaiting                                    bool
}

const ipPolicyResultFixedBytes = 4096

func ipPolicyResultDigest(id CommandIdentity, request RequestDigest, v ipPolicyResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func ipRefreshResultDigest(id CommandIdentity, request RequestDigest, v ipRefreshResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func ipPolicyProviderCode(provider string) bool {
	switch provider {
	case "tmdb", "tvdb", "musicbrainz":
		return true
	}
	return false
}

func ipRefreshMode(value string) bool {
	return value == "fill_missing" || value == "replace_unlocked"
}

func ipRefreshPurpose(value string) bool {
	return value == "metadata" || value == "artwork"
}

func ipPolicyRegion(value string) bool {
	return len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z'
}

// Hooks for the OWNER's closed canonicalResultDigest preflight/write switches.
// No independent encoder, callback, pluggable codec or alternate digest path.
func preflightIPPolicyResult(v ipPolicyResult) error {
	remaining := maxCanonicalResultBytes - ipPolicyResultFixedBytes
	for _, s := range [...]string{v.Region, v.Algorithm, v.RefreshMode, v.CreditAfterProvider} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if v.PolicyIncarnation == [16]byte{} || v.AcceptedRevision <= 0 || v.AppliedMS < 0 || v.ResultCount != 1 ||
		v.Priority < 0 || v.Priority > 1000 || !ipPolicyRegion(v.Region) || len(v.Algorithm) == 0 || len(v.Algorithm) > 64 || !ipRefreshMode(v.RefreshMode) {
		return ErrInvalid
	}
	if (v.CreditAfterItem == nil) != (v.CreditAfterProvider == "") {
		return ErrInvalid
	}
	if v.CreditAfterItem != nil && (*v.CreditAfterItem < 0 || *v.CreditAfterItem > 1_000_000) {
		return ErrInvalid
	}
	return nil
}

func preflightIPRefreshResult(v ipRefreshResult) error {
	remaining := maxCanonicalResultBytes - ipPolicyResultFixedBytes
	for _, s := range [...]string{v.Provider, v.Purpose, v.Phase} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if !ipPolicyProviderCode(v.Provider) || !ipRefreshPurpose(v.Purpose) || v.Phase != "entities" ||
		v.Generation <= 0 || v.AcceptedRevision <= 0 || v.EpochMS < 0 || v.AfterID != 0 ||
		v.ScheduledCount != 0 || v.AppliedMS < 0 || v.PageWaiting || v.ResultCount != 0 {
		return ErrInvalid
	}
	if v.Purpose == "artwork" && v.Provider == "musicbrainz" {
		return ErrInvalid
	}
	return nil
}

func writeIPPolicyResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipPolicyResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_policy")
	e.text("set")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.bytes(v.PolicyIncarnation[:])
	e.number(uint64(v.AcceptedRevision))
	e.number(uint64(v.AppliedMS))
	e.flag(v.Enabled)
	e.number(uint64(v.Priority))
	e.text(v.Region)
	e.text(v.Algorithm)
	e.text(v.RefreshMode)
	e.flag(v.CreditEnabled)
	if v.CreditAfterItem == nil {
		e.bytes([]byte{0})
	} else {
		e.bytes([]byte{1})
		e.number(uint64(*v.CreditAfterItem))
	}
	e.text(v.CreditAfterProvider)
	e.number(uint64(v.ResultCount))
	return e.err
}

func writeIPRefreshResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipRefreshResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_refresh")
	e.text("begin")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.Provider)
	e.text(v.Purpose)
	e.number(uint64(v.Generation))
	e.number(uint64(v.AcceptedRevision))
	e.number(uint64(v.EpochMS))
	e.text(v.Phase)
	e.number(uint64(v.AfterID))
	e.number(uint64(v.ScheduledCount))
	e.flag(v.PageWaiting)
	e.number(uint64(v.AppliedMS))
	e.number(uint64(v.ResultCount))
	return e.err
}
