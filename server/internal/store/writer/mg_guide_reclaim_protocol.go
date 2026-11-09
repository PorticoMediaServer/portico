package writer

import "math"

// MG guide data retirement protocol. These planners are inert: no SQL, clock,
// receipt, pin proof or COMMIT. W loads native state on one admitted turn,
// proves the current source head and every typed pin, applies at most ONE
// bounded child slice, and saves its own original result afterwards. Logical
// discard is not physical reclamation, and a saved cursor never authorizes a
// deletion on its own.
//
// External generation deletion stays OFF. The final `generation` family reports
// truthful intent and performs no effect until MP-15 physical session pins and
// the real release bridge are installed.
type mgGuideReclaimFamily string

const (
	mgGuideReclaimNone       mgGuideReclaimFamily = "none"
	mgGuideReclaimProgrammes mgGuideReclaimFamily = "programmes"
	mgGuideReclaimChannels   mgGuideReclaimFamily = "channels"
	mgGuideReclaimChoices    mgGuideReclaimFamily = "choices"
	mgGuideReclaimGeneration mgGuideReclaimFamily = "generation"
	mgGuideReclaimComplete   mgGuideReclaimFamily = "complete"
)

// One bounded turn: at most eight child rows, admitted from actual native
// variable bytes before the first DELETE. A full page is exactly this bound.
const (
	mgGuideReclaimRows   int64 = 8
	mgGuideReclaimBytes  int64 = 256 << 10
	mgGuideReclaimCursor int   = 4096
)

// Truthful, effect-free reasons. None of these claims reclamation, completion
// or a physical release.
const (
	mgGuideReclaimBlockedHead      = "current-head"
	mgGuideReclaimBlockedReminder  = "reminder-pinned"
	mgGuideReclaimBlockedRecording = "recording-pinned"
	mgGuideReclaimBlockedOperation = "operation-pinned"
	mgGuideReclaimBlockedExternal  = "external-generation-deletion-disabled"
)

func (f mgGuideReclaimFamily) valid() bool {
	switch f {
	case mgGuideReclaimNone, mgGuideReclaimProgrammes, mgGuideReclaimChannels, mgGuideReclaimChoices, mgGuideReclaimGeneration, mgGuideReclaimComplete:
		return true
	}
	return false
}

// Fixed child-before-parent order. Channels own the programmes that name them,
// and every surviving child family precedes the generation itself. G folds
// programme metadata into live_programmes, so there is no extra metadata slice.
func (f mgGuideReclaimFamily) next() (mgGuideReclaimFamily, bool) {
	switch f {
	case mgGuideReclaimProgrammes:
		return mgGuideReclaimChannels, true
	case mgGuideReclaimChannels:
		return mgGuideReclaimChoices, true
	case mgGuideReclaimChoices:
		return mgGuideReclaimGeneration, true
	}
	return "", false
}

// Native retirement facts for one operation. The caller still owns current
// authority, pins and result disclosure; these values are not authorization.
type mgGuideReclaimState struct {
	request, source, generation, status string
	incarnation                         [16]byte
	family                              mgGuideReclaimFamily
	cursor                              string
	sequence, acknowledged, revision    int64
}

func (s mgGuideReclaimState) valid() bool {
	if !mgGuideText(s.request, true, 256) || !mgGuideText(s.source, true, 256) || !mgGuideText(s.generation, true, 256) ||
		s.incarnation == ([16]byte{}) || !s.family.valid() || !mgGuideText(s.cursor, false, mgGuideReclaimCursor) ||
		s.sequence <= 0 || s.revision <= 0 || s.acknowledged < 0 || s.acknowledged > s.sequence {
		return false
	}
	switch s.status {
	case "staging", "published", "superseded":
	default:
		return false
	}
	if (s.family == mgGuideReclaimNone || s.family == mgGuideReclaimGeneration || s.family == mgGuideReclaimComplete) && s.cursor != "" {
		return false
	}
	return true
}

// A cursor transition is the only retirement effect. It never carries deleted
// counts: those must come from actual native readback on the same turn.
type mgGuideReclaimTransition struct {
	family             mgGuideReclaimFamily
	cursor             string
	sequence, revision int64
}

// Logical withdrawal of a staging operation's own generation. It requires the
// original unacknowledged-free lineage (`last==ACK`), a fresh operation identity
// under the SAME incarnation, and a generation that is not the serving head. A
// published generation is withdrawn by publishing a replacement, never by
// rewriting its accepted operation.
func planMGGuideDiscard(s mgGuideReclaimState, identity CommandIdentity, head string) (out mgGuideReclaimTransition, err error) {
	if !s.valid() || s.status != "staging" || s.family != mgGuideReclaimNone || s.cursor != "" ||
		!mgGuideText(head, false, 256) || head == s.generation ||
		identity.Incarnation != s.incarnation || identity.OperationID == ([16]byte{}) ||
		s.acknowledged != s.sequence || s.sequence == math.MaxInt64 || s.revision == math.MaxInt64 ||
		identity.Sequence != s.sequence+1 {
		return out, ErrConflict
	}
	return mgGuideReclaimTransition{mgGuideReclaimProgrammes, "", s.sequence + 1, s.revision + 1}, nil
}

// ONE bounded native page of the current family, ordered by its exact PK key.
// rows==0 means the family range is exhausted. `more` is the bounded lookahead,
// so a caller never COUNTs a whole generation to learn whether it is empty.
type mgGuideReclaimPage struct {
	family mgGuideReclaimFamily
	after  string
	rows   int64
	bytes  int64
	more   bool
	last   string
}

func (p mgGuideReclaimPage) valid(s mgGuideReclaimState) bool {
	if p.family != s.family || p.after != s.cursor || p.rows < 0 || p.rows > mgGuideReclaimRows ||
		p.bytes < 0 || p.bytes > mgGuideReclaimBytes {
		return false
	}
	if p.rows == 0 {
		return p.bytes == 0 && !p.more && p.last == ""
	}
	// A page must make exact forward progress and account for at least one byte
	// per row; a partial page can never claim more work behind it.
	return p.bytes > 0 && p.last > p.after && (!p.more || p.rows == mgGuideReclaimRows)
}

// planMGGuideReclaimStep advances exactly one bounded slice. A partial page
// deletes its rows and keeps the family; an exhausted page advances to the next
// child family as its own command. The `generation` family is never planned
// here because its deletion gate is closed.
func planMGGuideReclaimStep(s mgGuideReclaimState, identity CommandIdentity, page mgGuideReclaimPage) (out mgGuideReclaimTransition, err error) {
	if !s.valid() || s.status != "superseded" || s.family == mgGuideReclaimNone ||
		s.family == mgGuideReclaimGeneration || s.family == mgGuideReclaimComplete ||
		s.acknowledged != s.sequence || identity.Incarnation != s.incarnation ||
		identity.OperationID == ([16]byte{}) || identity.Sequence != s.sequence+1 ||
		s.sequence == math.MaxInt64 || s.revision == math.MaxInt64 || !page.valid(s) {
		return out, ErrConflict
	}
	if page.rows > 0 {
		return mgGuideReclaimTransition{s.family, page.last, s.sequence + 1, s.revision + 1}, nil
	}
	next, ok := s.family.next()
	if !ok || next == mgGuideReclaimGeneration {
		return out, ErrConflict
	}
	return mgGuideReclaimTransition{next, "", s.sequence + 1, s.revision + 1}, nil
}

// planMGGuideReclaimGeneration is the closed external gate. Children must be
// proven empty first; the effect then reports its truthful reason and performs
// no write at all. Enabling a real release is MP-15's bridge, not this task.
func planMGGuideReclaimGeneration(s mgGuideReclaimState, identity CommandIdentity, children int64) (out string, err error) {
	if !s.valid() || s.status != "superseded" || s.family != mgGuideReclaimGeneration || s.cursor != "" ||
		s.acknowledged != s.sequence || identity.Incarnation != s.incarnation ||
		identity.OperationID == ([16]byte{}) || identity.Sequence != s.sequence+1 {
		return out, ErrConflict
	}
	if children < 0 {
		return out, ErrInvalid
	}
	if children > 0 {
		return out, ErrResolveRequired
	}
	return mgGuideReclaimBlockedExternal, nil
}
