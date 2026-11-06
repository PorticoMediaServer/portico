package writer

import (
	"errors"
	"math"
	"testing"
)

func TestSearchCoalesceAccountingTransitions(t *testing.T) {
	for _, tt := range []struct {
		name       string
		banks      []searchCoalesceBankCounters
		wantDeltas [2]int64
		wantCounts [2]int64
	}{
		{"already pending", []searchCoalesceBankCounters{{1, 17, 41, 7, 3}}, [2]int64{0}, [2]int64{17}},
		{"caught up", []searchCoalesceBankCounters{{1, 0, 41, 7, 7}}, [2]int64{1}, [2]int64{1}},
		{"mixed banks", []searchCoalesceBankCounters{{1, 17, 41, 7, 3}, {2, 9, 81, 11, 11}}, [2]int64{0, 1}, [2]int64{17, 10}},
		{"both pending", []searchCoalesceBankCounters{{1, 1, 41, 7, 0}, {2, 1, 81, 11, 0}}, [2]int64{0, 0}, [2]int64{1, 1}},
		{"both caught up", []searchCoalesceBankCounters{{1, 17, 41, 7, 7}, {2, 9, 81, 11, 11}}, [2]int64{1, 1}, [2]int64{18, 10}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]searchCoalesceBankCounters(nil), tt.banks...)
			plan, err := prepareSearchCoalesceAccounting(53, tt.banks)
			if err != nil || plan.InputSequence != 54 || plan.BankCount != len(tt.banks) {
				t.Fatalf("changed cohort must advance ONE clock: %+v %v", plan, err)
			}
			for i, bank := range tt.banks {
				if bank != before[i] {
					t.Fatal("reduction mutated captured counters")
				}
				got := plan.Banks[i]
				if got.GenerationID != bank.GenerationID || got.JobRevision != bank.JobRevision+1 ||
					got.InputGeneration != bank.InputGeneration+1 || got.PendingDelta != tt.wantDeltas[i] ||
					got.PendingSubjects != tt.wantCounts[i] {
					t.Fatalf("wrong subject-local counter transition: %+v", got)
				}
			}
			if plan.BankCount == 1 && plan.Banks[1] != (searchCoalesceBankAdvance{}) {
				t.Fatal("unused bank has an effect")
			}
			frozen := plan
			tt.banks[0].PendingSubjects++
			if plan != frozen {
				t.Fatal("plan retained mutable input custody")
			}
		})
	}
}

func TestSearchCoalesceAccountingConservesPendingPopulation(t *testing.T) {
	// Enumerate a subject's contribution independently of the implementation's
	// transition branches. Other subjects' pending population stays fixed.
	p := func(input, published int64) int64 {
		if input > published {
			return 1
		}
		return 0
	}
	for input := int64(1); input <= 8; input++ {
		for published := int64(0); published <= input; published++ {
			for others := int64(0); others <= 5; others++ {
				oldP := p(input, published)
				before := searchCoalesceBankCounters{GenerationID: 13, PendingSubjects: others + oldP,
					JobRevision: 71, InputGeneration: input, PublishedGeneration: published}
				plan, err := prepareSearchCoalesceAccounting(53, []searchCoalesceBankCounters{before})
				if err != nil {
					t.Fatal(err)
				}
				after := plan.Banks[0]
				newP := p(after.InputGeneration, published)
				if newP != 1 || after.PendingDelta != newP-oldP || after.PendingSubjects-newP != others {
					t.Fatalf("lost population: input=%d published=%d others=%d => %+v", input, published, others, after)
				}
			}
		}
	}
}

func TestSearchCoalesceAccountingRepeatedChangeAndCatchUp(t *testing.T) {
	bank := searchCoalesceBankCounters{GenerationID: 1, PendingSubjects: 1, JobRevision: 7, InputGeneration: 3}
	sequence := int64(19)
	for n := 0; n < 20; n++ {
		plan, err := prepareSearchCoalesceAccounting(sequence, []searchCoalesceBankCounters{bank})
		if err != nil || plan.Banks[0].PendingDelta != 0 || plan.Banks[0].PendingSubjects != 1 || plan.InputSequence != sequence+1 {
			t.Fatalf("repeated pending source overcounted: %+v %v", plan, err)
		}
		sequence = plan.InputSequence
		bank.JobRevision, bank.InputGeneration = plan.Banks[0].JobRevision, plan.Banks[0].InputGeneration
	}
	// Model an independently successful captured-current publication only to
	// establish arithmetic input. This is not a publication command control.
	bank.PublishedGeneration, bank.PendingSubjects = bank.InputGeneration, 0
	plan, err := prepareSearchCoalesceAccounting(sequence, []searchCoalesceBankCounters{bank})
	if err != nil || plan.Banks[0].PendingDelta != 1 || plan.Banks[0].PendingSubjects != 1 {
		t.Fatalf("caught-up source failed to become pending: %+v %v", plan, err)
	}
	bank.InputGeneration, bank.JobRevision, bank.PendingSubjects = plan.Banks[0].InputGeneration, plan.Banks[0].JobRevision, plan.Banks[0].PendingSubjects
	plan, err = prepareSearchCoalesceAccounting(plan.InputSequence, []searchCoalesceBankCounters{bank})
	if err != nil || plan.Banks[0].PendingDelta != 0 || plan.Banks[0].PendingSubjects != 1 {
		t.Fatal("caught-up transition was counted twice")
	}
}

func TestSearchCoalesceAccountingOverflowAndWholePlanRefusal(t *testing.T) {
	valid := searchCoalesceBankCounters{GenerationID: 1, PendingSubjects: 1, JobRevision: 7, InputGeneration: 3}
	for _, tt := range []struct {
		name string
		edit func(*searchCoalesceBankCounters)
	}{
		{"missing bank", func(b *searchCoalesceBankCounters) { b.GenerationID = 0 }},
		{"negative pending", func(b *searchCoalesceBankCounters) { b.PendingSubjects = -1 }},
		{"missing own revision", func(b *searchCoalesceBankCounters) { b.JobRevision = 0 }},
		{"own revision overflow", func(b *searchCoalesceBankCounters) { b.JobRevision = math.MaxInt64 }},
		{"missing input", func(b *searchCoalesceBankCounters) { b.InputGeneration = 0 }},
		{"input overflow", func(b *searchCoalesceBankCounters) { b.InputGeneration = math.MaxInt64 }},
		{"negative published", func(b *searchCoalesceBankCounters) { b.PublishedGeneration = -1 }},
		{"future published", func(b *searchCoalesceBankCounters) { b.PublishedGeneration = b.InputGeneration + 1 }},
		{"missing pending contribution", func(b *searchCoalesceBankCounters) { b.PendingSubjects = 0 }},
		{"caught up pending overflow", func(b *searchCoalesceBankCounters) {
			b.PendingSubjects, b.PublishedGeneration = math.MaxInt64, b.InputGeneration
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bad := valid
			bad.GenerationID = 2
			tt.edit(&bad)
			banks := []searchCoalesceBankCounters{valid, bad}
			plan, err := prepareSearchCoalesceAccounting(0, banks)
			if !errors.Is(err, ErrInvalid) || plan != (searchCoalesceAccounting{}) || banks[0] != valid || banks[1] != bad {
				t.Fatalf("invalid second bank escaped as partial/mutated plan: %+v %v", plan, err)
			}
		})
	}
	for _, sequence := range []int64{-1, math.MaxInt64} {
		if plan, err := prepareSearchCoalesceAccounting(sequence, []searchCoalesceBankCounters{valid}); !errors.Is(err, ErrInvalid) || plan != (searchCoalesceAccounting{}) {
			t.Fatal("invalid/overflowing global clock produced effects")
		}
	}
	valid.PendingSubjects, valid.JobRevision, valid.InputGeneration = math.MaxInt64, math.MaxInt64-1, math.MaxInt64-1
	plan, err := prepareSearchCoalesceAccounting(math.MaxInt64-1, []searchCoalesceBankCounters{valid})
	if err != nil || plan.InputSequence != math.MaxInt64 || plan.Banks[0].JobRevision != math.MaxInt64 ||
		plan.Banks[0].InputGeneration != math.MaxInt64 || plan.Banks[0].PendingSubjects != math.MaxInt64 || plan.Banks[0].PendingDelta != 0 {
		t.Fatalf("pending delta0 must not overflow bank count: %+v %v", plan, err)
	}
}

func TestSearchCoalesceAccountingBoundsTargetsBeforeInspection(t *testing.T) {
	if plan, err := prepareSearchCoalesceAccounting(0, nil); !errors.Is(err, ErrInvalid) || plan != (searchCoalesceAccounting{}) {
		t.Fatal("empty source accounting must not advance clock")
	}
	// Even invalid first values/clock cannot hide an unqualified target count.
	if plan, err := prepareSearchCoalesceAccounting(-1, make([]searchCoalesceBankCounters, 3)); !errors.Is(err, ErrCap) || plan != (searchCoalesceAccounting{}) {
		t.Fatal("target-count admission did not precede inspection")
	}
	bank := searchCoalesceBankCounters{GenerationID: 1, PendingSubjects: 1, JobRevision: 7, InputGeneration: 3}
	if plan, err := prepareSearchCoalesceAccounting(0, []searchCoalesceBankCounters{bank, bank}); !errors.Is(err, ErrInvalid) || plan != (searchCoalesceAccounting{}) {
		t.Fatal("duplicate bank was counted twice")
	}
}
