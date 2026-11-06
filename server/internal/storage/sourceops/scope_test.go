package sourceops

import (
	"context"
	"errors"
	"testing"
)

func TestScopeCloseRetainsPhysicalDebt(t *testing.T) {
	s := New()
	done, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(s.Context().Err(), context.Canceled) || !errors.Is(s.Wait(ctx), context.Canceled) || s.Active() != 1 {
		t.Fatal("cancellation retired actual work")
	}
	if release, err := s.Begin(); release != nil || !errors.Is(err, ErrRootLease) {
		t.Fatal("closed scope accepted new debt", err)
	}
	done()
	done() // the same actual completion cannot debit twice
	if err := s.Wait(t.Context()); err != nil || s.Active() != 0 {
		t.Fatal("actual completion did not retire debt", err)
	}
	s.Close()
}
