package writer

// These are captured counter values for ONE existing subject, in the source
// command's active and optional building bank. They carry no source, profile,
// stage, lease or receipt authority. The actual same-turn proof remains with
// the caller; this reduction neither reads nor writes SQL.
type searchCoalesceBankCounters struct {
	GenerationID        int64
	PendingSubjects     int64
	JobRevision         int64 // the SUBJECT publisher, never generation.job_revision
	InputGeneration     int64
	PublishedGeneration int64
}

type searchCoalesceBankAdvance struct {
	GenerationID    int64
	JobRevision     int64
	InputGeneration int64
	PendingSubjects int64
	PendingDelta    int64
}

type searchCoalesceAccounting struct {
	InputSequence int64
	BankCount     int
	Banks         [2]searchCoalesceBankAdvance
}

// prepareSearchCoalesceAccounting prepares counters only for an actual source
// change. A no-op or historical replay must bypass it, just as it bypasses all
// fresh source effects. The caller resolves its ORIGINAL receipt first and
// proves exact source/head/profile/job/stage facts in the SAME commandTurn.
// Neither this value nor a new RAW job witness attests an old staged image.
//
// Scope: one existing subject and at most two DISTINCT existing banks. No
// missing-job insertion, generic source mutation or accepting command is
// enabled here. Output is a detached value; refusal returns no partial plan.
func prepareSearchCoalesceAccounting(sequence int64, banks []searchCoalesceBankCounters) (searchCoalesceAccounting, error) {
	if len(banks) == 0 {
		return searchCoalesceAccounting{}, invalid("search_coalesce_banks")
	}
	// Refuse an unqualified cohort before walking even its first value.
	if err := count("search_coalesce_banks", len(banks), 2); err != nil {
		return searchCoalesceAccounting{}, err
	}
	nextSequence, err := nextSearchInputCounter(sequence, 1)
	if err != nil {
		return searchCoalesceAccounting{}, err
	}
	plan := searchCoalesceAccounting{InputSequence: nextSequence, BankCount: len(banks)}
	for i, bank := range banks {
		if bank.GenerationID <= 0 || i > 0 && bank.GenerationID == banks[0].GenerationID ||
			bank.JobRevision <= 0 || bank.InputGeneration <= 0 ||
			bank.PublishedGeneration < 0 || bank.PublishedGeneration > bank.InputGeneration || bank.PendingSubjects < 0 {
			return searchCoalesceAccounting{}, invalid("search_coalesce_counters")
		}
		nextRevision, err := nextSearchInputCounter(bank.JobRevision, 1)
		if err != nil {
			return searchCoalesceAccounting{}, err
		}
		nextInput, err := nextSearchInputCounter(bank.InputGeneration, 1)
		if err != nil {
			return searchCoalesceAccounting{}, err
		}
		// Every fresh coalesce leaves the subject pending. A caught-up job
		// contributes +1; one already pending contributes 0, including when
		// the bank total is at MaxInt64. That total must already include it.
		delta, nextPending := int64(0), bank.PendingSubjects
		if bank.InputGeneration == bank.PublishedGeneration {
			delta = 1
			nextPending, err = nextSearchInputCounter(bank.PendingSubjects, delta)
			if err != nil {
				return searchCoalesceAccounting{}, err
			}
		} else if bank.PendingSubjects == 0 {
			return searchCoalesceAccounting{}, invalid("search_coalesce_pending")
		}
		plan.Banks[i] = searchCoalesceBankAdvance{
			GenerationID: bank.GenerationID, JobRevision: nextRevision,
			InputGeneration: nextInput, PendingSubjects: nextPending, PendingDelta: delta,
		}
	}
	return plan, nil
}
