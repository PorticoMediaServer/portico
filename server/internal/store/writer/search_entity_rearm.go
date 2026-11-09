package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This is a source-command composition, not another command/receipt issuer.
// The owner resolves ORIGINAL history before its metadata write and this call.
// An error cancels the SAME transaction; even a caller that ignores it cannot
// commit its metadata or a prefix of the two-bank obligation.
type searchRearmReason string

const (
	searchRearmInvalid             searchRearmReason = "invalid_input"
	searchRearmNeverIndexed        searchRearmReason = "never_indexed"
	searchRearmMissingBankJob      searchRearmReason = "missing_bank_job"
	searchRearmSourceOrBankChanged searchRearmReason = "source_or_bank_changed"
	searchRearmCustodyMismatch     searchRearmReason = "custody_mismatch"
	searchRearmCounterExhausted    searchRearmReason = "counter_exhausted"
	searchRearmUnregistered        searchRearmReason = "unregistered_recipe"
)

type searchRearmRefusal struct{ Reason searchRearmReason }

func (e *searchRearmRefusal) Error() string { return "search rearm: " + string(e.Reason) }
func (e *searchRearmRefusal) Unwrap() error {
	if e.Reason == searchRearmInvalid || e.Reason == searchRearmUnregistered {
		return ErrInvalid
	}
	if e.Reason == searchRearmNeverIndexed || e.Reason == searchRearmMissingBankJob || e.Reason == searchRearmCustodyMismatch {
		return ErrResolveRequired
	}
	return ErrConflict
}

// Bank IDs identify immutable bank profiles, not mutable generation-driver
// leases/cursors. Recipe is an expectation, never an instruction to edit a bank.
type searchEntityRearmBank struct {
	ID     int64
	Recipe string
}

type searchEntityRearmInput struct {
	entity, sourceRevision int64
	incarnation            [16]byte
	banks                  [2]searchEntityRearmBank
	count                  int
	valid                  bool
}

func newSearchEntityRearmInput(entity int64, incarnation [16]byte, sourceRevision int64, active searchEntityRearmBank, building *searchEntityRearmBank) (*searchEntityRearmInput, error) {
	if entity <= 0 || incarnation == ([16]byte{}) || sourceRevision <= 0 {
		return nil, &searchRearmRefusal{searchRearmInvalid}
	}
	in := &searchEntityRearmInput{entity: entity, incarnation: incarnation, sourceRevision: sourceRevision, banks: [2]searchEntityRearmBank{active}, count: 1}
	if building != nil {
		in.banks[1], in.count = *building, 2
	}
	for i := 0; i < in.count; i++ {
		b := &in.banks[i]
		if b.ID <= 0 || i == 1 && b.ID == active.ID || len(b.Recipe) == 0 || len(b.Recipe) > 128 || !utf8.ValidString(b.Recipe) || strings.IndexByte(b.Recipe, 0) >= 0 {
			return nil, &searchRearmRefusal{searchRearmInvalid}
		}
		b.Recipe = strings.Clone(b.Recipe)
	}
	in.valid = true
	return in, nil
}

// A stable obligation description, NOT Applied, a receipt or a readiness claim.
// It excludes mutable worker revisions/lease/clock and a Changed bit: an exact
// second call returns the same value without creating any work or SQL effect.
type searchEntityRearmObligation struct{ BankID, JobID, InputGeneration int64 }
type searchEntityRearmResult struct {
	EntityID, SourceRevision int64
	BankCount                int
	Banks                    [2]searchEntityRearmObligation
}

type searchRearmJob struct {
	id, revision, input, published, sourceRevision int64
	sourceIncarnation                              [16]byte
	commandIncarnation                             [16]byte
	head, stage                                    sql.NullInt64
	phase                                          string
	afterField, afterOrdinal, afterFragment        int64
	expected, staged, stagedBytes                  int64
	leaseOwner, leaseGeneration, leaseExpires      int64
	attempts, last, ack                            int64
	due                                            sql.NullInt64
}

type searchRearmCustody struct {
	id, revision, last, ack, leaseOwner, leaseGeneration, leaseExpires, attempts, after int64
	incarnation                                                                         [16]byte
	due                                                                                 sql.NullInt64
}

type searchRearmBankPlan struct {
	bank       searchEntityRearmBank
	role       string
	pending    int64
	job        searchRearmJob
	retirement searchRearmCustody
	changed    bool
	advance    searchCoalesceBankAdvance
	nextLease  int64
	nextRetire int64
}

// rearmSearchEntity has one all-or-nothing source-command state machine:
//
//   - unchanged source + current bank/recipe: stable obligation, ZERO effects;
//   - caught up -> capture: input/revision/lease generation +1, pending +1;
//   - already pending -> capture: input/revision/lease generation +1, pending +0;
//   - detached OLD stage: existing held reclaimer -> ready (never new custody);
//   - published head/native own rows: unchanged until PB atomically replaces them;
//   - error: zero result, cancel caller transaction; its dispatcher rolls back.
//
// Active and optional building are an EXACT expected pair. Plan both before
// effects; advance input_sequence once, never serving-search revision. Immutable
// bank ID/recipe is the durable FK fence. A new bank/recipe needs its own existing
// job; missing jobs refuse, never initialize or skip a bank. Worker command/ACK
// identity is retained; only its fresh work lease is invalidated. The owner's
// original receipt replay MUST bypass this function entirely.
//
// No DDL, CommandID, FTS maintenance, publication receipt, grant or dispatcher.
// OPEN GATES: caller216/217 authority/receipt/Work composition and qualification;
// C-embed actual-native Capture before golden adoption; SearchSeal body/dispatch
// is absent on the inspected base: rerun its stale-lease matrix row when it lands.
// The retained sealed-image ownership/head-CAS test is not seal certification.
func rearmSearchEntity(ctx context.Context, turn *commandTurn, in *searchEntityRearmInput) (out searchEntityRearmResult, err error) {
	defer func() {
		if err != nil && turn != nil && turn.transaction != nil && turn.transaction.cancel != nil {
			turn.transaction.cancel()
		}
	}()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in == nil || !in.valid || in.count < 1 || in.count > 2 {
		return out, &searchRearmRefusal{searchRearmInvalid}
	}
	defer turn.transaction.guardPanic()
	// Definitions and Work are composed/frozen by the caller's dispatcher.
	// This component cannot install SQL, acquire a grant or manufacture a turn.
	for _, id := range searchEntityRearmRequiredIDs() {
		if _, ok := turn.steps[id]; !ok {
			return out, &searchRearmRefusal{searchRearmUnregistered}
		}
	}
	query := func(id stmt.ID, args ...any) (commandRow, error) {
		return turn.queryRow(ctx, turn.steps[id].statementStep, args...)
	}
	cas := func(id stmt.ID, args ...any) error {
		r, e := turn.exec(ctx, turn.steps[id].statementStep, args...)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return &searchRearmRefusal{searchRearmSourceOrBankChanged}
		}
		return nil
	}
	var serving, sequence int64
	var active, building sql.NullInt64
	r, e := query(searchInputHead)
	if e != nil {
		return out, e
	}
	if e = r.Scan(&serving, &active, &building, &sequence); e != nil {
		return out, searchRearmReadError(e, searchRearmSourceOrBankChanged)
	}
	if serving <= 0 || sequence < 0 || !active.Valid || active.Int64 != in.banks[0].ID ||
		building.Valid != (in.count == 2) || building.Valid && building.Int64 != in.banks[1].ID {
		return out, &searchRearmRefusal{searchRearmSourceOrBankChanged}
	}
	var library, kind, revision int64
	var incarnation []byte
	r, e = query(searchRearmEntity, in.entity)
	if e != nil {
		return out, e
	}
	if e = r.Scan(&library, &kind, &revision, &incarnation); e != nil {
		return out, searchRearmReadError(e, searchRearmSourceOrBankChanged)
	}
	if library <= 0 || kind <= 0 || revision != in.sourceRevision || !bytes.Equal(incarnation, in.incarnation[:]) {
		return out, &searchRearmRefusal{searchRearmSourceOrBankChanged}
	}
	r, e = query(searchInputSubject, in.entity, library, kind)
	if e != nil {
		return out, e
	}
	var subject int64
	if e = r.Scan(&subject); e != nil || subject != in.entity {
		return out, searchRearmReadError(e, searchRearmSourceOrBankChanged)
	}
	var plans [2]searchRearmBankPlan
	var counters [2]searchCoalesceBankCounters
	var indices [2]int
	changed := 0
	out = searchEntityRearmResult{EntityID: in.entity, SourceRevision: revision, BankCount: in.count}
	// Finish ALL validation, custody and overflow planning before any write.
	for i := 0; i < in.count; i++ {
		p := &plans[i]
		p.bank, p.role = in.banks[i], "active"
		if i == 1 {
			p.role = "building"
		}
		r, e = query(searchRearmBank, p.bank.ID, p.role, p.bank.Recipe)
		if e != nil {
			return out, e
		}
		if e = r.Scan(&p.pending); e != nil || p.pending < 0 {
			return out, searchRearmReadError(e, searchRearmSourceOrBankChanged)
		}
		r, e = query(searchRearmJobRead, in.entity, p.bank.ID, p.bank.Recipe)
		if e != nil {
			return out, e
		}
		j := &p.job
		var source, command []byte
		e = r.Scan(&j.id, &j.revision, &j.input, &j.published, &j.head, &j.stage, &source, &j.sourceRevision, &command, &j.phase,
			&j.afterField, &j.afterOrdinal, &j.afterFragment, &j.expected, &j.staged, &j.stagedBytes,
			&j.leaseOwner, &j.leaseGeneration, &j.leaseExpires, &j.attempts, &j.due, &j.last, &j.ack)
		if errors.Is(e, sql.ErrNoRows) {
			// Distinguish a never-indexed subject from a missing required bank
			// job without counting or scanning its complete bank history.
			r, e = query(searchInputExisting, in.entity)
			if e != nil {
				return out, e
			}
			var anyJob int64
			e = r.Scan(&anyJob)
			if errors.Is(e, sql.ErrNoRows) {
				return out, &searchRearmRefusal{searchRearmNeverIndexed}
			}
			if e != nil {
				return out, e
			}
			return out, &searchRearmRefusal{searchRearmMissingBankJob}
		}
		if e != nil {
			return out, e
		}
		if j.id <= 0 || j.revision <= 0 || j.input <= 0 || j.published < 0 || j.published > j.input ||
			j.head.Valid != (j.published > 0) || j.head.Valid && j.head.Int64 <= 0 || j.stage.Valid && (j.stage.Int64 <= 0 || j.stage == j.head) ||
			len(source) != 16 || len(command) != 16 || j.sourceRevision <= 0 || j.last < j.ack || j.ack < 0 ||
			j.input > j.published && p.pending == 0 {
			return out, &searchRearmRefusal{searchRearmCustodyMismatch}
		}
		copy(j.sourceIncarnation[:], source)
		copy(j.commandIncarnation[:], command)
		if j.sourceRevision > revision {
			return out, &searchRearmRefusal{searchRearmSourceOrBankChanged}
		}
		// Head strings/native rows/custody remain untouched. Only fixed
		// publication metadata is read; no title or token bytes are copied.
		if j.head.Valid {
			r, e = query(searchRearmHead, j.head.Int64, in.entity, p.bank.ID, j.published, p.bank.Recipe)
			if e != nil {
				return out, e
			}
			var id int64
			if e = r.Scan(&id); e != nil || id != j.head.Int64 {
				return out, searchRearmReadError(e, searchRearmCustodyMismatch)
			}
		}
		if j.stage.Valid {
			r, e = query(searchRearmStage, j.stage.Int64, in.entity, p.bank.ID, j.input, j.sourceIncarnation[:], j.sourceRevision, p.bank.Recipe)
			if e != nil {
				return out, e
			}
			var id int64
			if e = r.Scan(&id); e != nil || id != j.stage.Int64 {
				return out, searchRearmReadError(e, searchRearmCustodyMismatch)
			}
			r, e = query(searchRearmCustodyRead, j.stage.Int64, j.stage.Int64, "held")
			if e != nil {
				return out, e
			}
			c := &p.retirement
			var inc []byte
			if e = r.Scan(&c.id, &c.revision, &inc, &c.after, &c.last, &c.ack, &c.leaseOwner, &c.leaseGeneration, &c.leaseExpires, &c.attempts, &c.due); e != nil {
				return out, searchRearmReadError(e, searchRearmCustodyMismatch)
			}
			if c.id <= 0 || c.revision <= 0 || len(inc) != 16 || c.after != 0 || c.last != c.ack || c.ack != 0 || c.leaseOwner != 0 || c.leaseExpires != 0 {
				return out, &searchRearmRefusal{searchRearmCustodyMismatch}
			}
			copy(c.incarnation[:], inc)
		}
		p.changed = j.sourceRevision != revision || j.sourceIncarnation != in.incarnation
		out.Banks[i] = searchEntityRearmObligation{p.bank.ID, j.id, j.input}
		if !p.changed {
			continue
		}
		if j.leaseGeneration == math.MaxInt64 || p.retirement.revision == math.MaxInt64 {
			return out, &searchRearmRefusal{searchRearmCounterExhausted}
		}
		p.nextLease, p.nextRetire = j.leaseGeneration+1, p.retirement.revision+1
		indices[changed] = i
		counters[changed] = searchCoalesceBankCounters{p.bank.ID, p.pending, j.revision, j.input, j.published}
		changed++
	}
	if changed == 0 {
		return out, nil
	}
	accounting, e := prepareSearchCoalesceAccounting(sequence, counters[:changed])
	if e != nil {
		return out, &searchRearmRefusal{searchRearmCounterExhausted}
	}
	for n := 0; n < changed; n++ {
		p := &plans[indices[n]]
		p.advance = accounting.Banks[n]
		out.Banks[indices[n]].InputGeneration = p.advance.InputGeneration
	}
	for n := 0; n < changed; n++ {
		p := &plans[indices[n]]
		j, a := p.job, p.advance
		id, args := searchRearmJobValues(in, p)
		if e = cas(id, args...); e != nil {
			return out, e
		}
		if j.stage.Valid {
			c := p.retirement
			id := searchRearmWakeNoDue
			args := []any{p.nextRetire, c.id, j.stage.Int64, j.stage.Int64, c.incarnation[:], c.revision, c.after, c.last, c.ack, c.leaseOwner, c.leaseGeneration, c.leaseExpires, c.attempts}
			if c.due.Valid {
				id, args = searchRearmWakeDue, append(args, c.due.Int64)
			}
			if e = cas(id, args...); e != nil {
				return out, e
			}
		}
		if e = cas(searchRearmPending, a.PendingSubjects, p.bank.ID, p.role, p.bank.Recipe, p.pending); e != nil {
			return out, e
		}
	}
	if building.Valid {
		e = cas(searchInputBuilding, accounting.InputSequence, serving, active.Int64, building.Int64, sequence)
	} else {
		e = cas(searchInputSequence, accounting.InputSequence, serving, active.Int64, sequence)
	}
	return out, e
}

func searchRearmReadError(err error, reason searchRearmReason) error {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return &searchRearmRefusal{reason}
	}
	return err
}
