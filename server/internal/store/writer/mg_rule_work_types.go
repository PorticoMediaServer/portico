package writer

import (
	"math"
	"strconv"
	"strings"
)

// MG-12 composes the bounded rule-work draft (claim/renew/advance with an exact
// cursor and lease generation) into ONE accepting frame, and adds the bounded
// scheduled-intent reconciliation that MG-11 deferred here. Both commands are
// NORMAL, owner-scoped, unwired writer code: no caller string, cached row,
// lease token or descriptor grants authority, and nothing here releases an
// allocation, artifact, generation pin or physical byte.
const (
	MGRuleWorkReconcile  CommandID = 826
	MGScheduledReconcile CommandID = 827
)

const (
	mgReconcileRequestVersion uint16 = 1
	mgReconcileWorkLimit             = 8
	mgReconcileCursorMax             = 4096
	mgReconcileDiagnosticMax         = 1024
	mgReconcileLeaseMaxMS            = 20000
	mgReconcileAttemptsMax           = 4294967295
)

// The exact native dvr_rule_work row. due is split into dueMS/hasDue so the
// nil job_next_attempt_ms (retired from the due index) is not mistaken for 0.
type mgReconcileWorkFacts struct {
	rule            string
	ruleRevision    int64
	generation      string
	phase           string
	cursor          string
	updated         int64
	diagnostic      string
	jobOwner        int64
	leaseGeneration int64
	expires         int64
	attempts        int64
	dueMS           int64
	hasDue          bool
	jobRevision     int64
}

func mgReconcileWorkPhase(p string) bool {
	return p == "existing" || p == "discover" || p == "idle"
}

func (f mgReconcileWorkFacts) valid() bool {
	return mgRecordingHex(f.rule, 64) && f.ruleRevision >= 1 && f.ruleRevision != math.MaxInt64 &&
		mgRecordingHex(f.generation, 48) && mgReconcileWorkPhase(f.phase) &&
		mgRecordingText(f.cursor, false, mgReconcileCursorMax) &&
		f.updated >= 0 && mgRecordingText(f.diagnostic, false, mgReconcileDiagnosticMax) &&
		f.jobOwner >= 0 && f.leaseGeneration >= 0 && f.expires >= 0 &&
		f.attempts >= 0 && f.attempts <= mgReconcileAttemptsMax &&
		f.jobRevision >= 1 && f.jobRevision != math.MaxInt64 && (!f.hasDue || f.dueMS >= 0)
}

// One bounded page of already-composed child results. units is a count of real
// child intents written in THIS frame, never a scanned-EOF assertion.
type mgReconcileWorkPage struct {
	phase  string
	cursor string
	units  int64
}

func (p mgReconcileWorkPage) valid() bool {
	return mgReconcileWorkPhase(p.phase) && mgRecordingText(p.cursor, false, mgReconcileCursorMax) &&
		p.units >= 0 && p.units <= mgReconcileWorkLimit
}

type mgReconcileRuleRequest struct {
	owner          mgRecordingOwner
	rule           string
	ruleRevision   int64
	sourceRevision int64
	generation     string
	mode           string // claim | renew | advance
	worker         int64
	leaseUntil     int64
	before         mgReconcileWorkFacts
	page           mgReconcileWorkPage
}

// Original intent only: the caller's expected work revision/phase/cursor and
// the bounded page are request identity, while the current source head and the
// remaining live-row columns are fresh-only CAS expectations. A legitimate
// retry after a guide refresh therefore still resolves its original receipt.
type mgReconcileRuleIdentity struct {
	account, profile string
	rule             string
	ruleRevision     int64
	mode             string
	worker           int64
	leaseUntil       int64
	workRevision     int64
	phase, cursor    string
	pagePhase        string
	pageCursor       string
	pageUnits        int64
}

type mgReconcileRuleCommand struct {
	identity CommandIdentity
	owner    mgRecordingOwner
	request  mgReconcileRuleIdentity
	digest   RequestDigest
	valid    bool
}

func (r mgReconcileRuleRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.rule, 64) || r.ruleRevision < 1 || r.ruleRevision == math.MaxInt64 ||
		r.sourceRevision < 1 || !mgRecordingHex(r.generation, 48) || !r.before.valid() ||
		r.before.rule != r.rule || r.worker < 1 || r.worker > math.MaxInt32 || r.leaseUntil < 1 {
		return ErrInvalid
	}
	if r.before.leaseGeneration == math.MaxInt64 {
		return ErrConflict
	}
	switch r.mode {
	case "claim", "renew":
		if r.page != (mgReconcileWorkPage{}) {
			return ErrInvalid
		}
	case "advance":
		if !r.page.valid() {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func newMGReconcileRuleCommand(id CommandIdentity, r mgReconcileRuleRequest) (*mgReconcileRuleCommand, error) {
	if !mgPersonalIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	request := mgReconcileRuleIdentity{
		account: r.owner.account, profile: r.owner.profile, rule: r.rule, ruleRevision: r.ruleRevision,
		mode: r.mode, worker: r.worker, leaseUntil: r.leaseUntil, workRevision: r.before.jobRevision,
		phase: r.before.phase, cursor: r.before.cursor,
		pagePhase: r.page.phase, pageCursor: r.page.cursor, pageUnits: r.page.units,
	}
	digest, err := canonicalRequestDigest(MGRuleWorkReconcile, mgReconcileRequestVersion, id, request)
	if err != nil {
		return nil, err
	}
	return &mgReconcileRuleCommand{identity: id, owner: r.owner, request: request, digest: digest, valid: true}, nil
}

type mgReconcileScheduledRequest struct {
	owner          mgRecordingOwner
	rule           string
	ruleRevision   int64
	sourceRevision int64
	generation     string
	policy         string // keep | cancel
	worker         int64
	before         mgReconcileWorkFacts
}

type mgReconcileScheduledIdentity struct {
	account, profile string
	rule             string
	ruleRevision     int64
	policy           string
	worker           int64
	workRevision     int64
	phase, cursor    string
}

type mgReconcileScheduledCommand struct {
	identity CommandIdentity
	owner    mgRecordingOwner
	request  mgReconcileScheduledIdentity
	digest   RequestDigest
	valid    bool
}

func (r mgReconcileScheduledRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.rule, 64) || r.ruleRevision < 1 || r.ruleRevision == math.MaxInt64 ||
		r.sourceRevision < 1 || !mgRecordingHex(r.generation, 48) || !r.before.valid() ||
		r.before.rule != r.rule || r.worker < 1 || r.worker > math.MaxInt32 {
		return ErrInvalid
	}
	if r.policy != "keep" && r.policy != "cancel" {
		return ErrInvalid
	}
	// Only a worker that already holds the bounded lease may detach or cancel
	// the rule's future intents; this frame never claims a lease of its own.
	if r.before.jobOwner != r.worker || r.before.leaseGeneration < 1 || r.before.expires < 1 ||
		(r.before.phase != "existing" && r.before.phase != "discover") {
		return ErrInvalid
	}
	if r.before.cursor != "" {
		if _, _, ok := mgScheduledCursorParse(r.before.cursor); !ok {
			return ErrInvalid
		}
	}
	return nil
}

func newMGReconcileScheduledCommand(id CommandIdentity, r mgReconcileScheduledRequest) (*mgReconcileScheduledCommand, error) {
	if !mgPersonalIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	request := mgReconcileScheduledIdentity{
		account: r.owner.account, profile: r.owner.profile, rule: r.rule, ruleRevision: r.ruleRevision,
		policy: r.policy, worker: r.worker, workRevision: r.before.jobRevision,
		phase: r.before.phase, cursor: r.before.cursor,
	}
	digest, err := canonicalRequestDigest(MGScheduledReconcile, mgReconcileRequestVersion, id, request)
	if err != nil {
		return nil, err
	}
	return &mgReconcileScheduledCommand{identity: id, owner: r.owner, request: request, digest: digest, valid: true}, nil
}

// The work cursor for the scheduled page is the exact (start_ms,id) keyset
// position. Zero-padded decimal keeps lexical order identical to numeric order
// so the draft's strict cursor monotonicity rule still holds.
func mgScheduledCursor(start int64, id string) string {
	return strings.Repeat("0", 20-len(strconv.FormatInt(start, 10))) + strconv.FormatInt(start, 10) + ":" + id
}

func mgScheduledCursorParse(s string) (start int64, id string, ok bool) {
	if len(s) != 21+64 || s[20] != ':' {
		return 0, "", false
	}
	for _, c := range s[:20] {
		if c < '0' || c > '9' {
			return 0, "", false
		}
	}
	start, err := strconv.ParseInt(s[:20], 10, 64)
	if err != nil || start < 0 {
		return 0, "", false
	}
	id = s[21:]
	if !mgRecordingHex(id, 64) {
		return 0, "", false
	}
	return start, id, true
}
