package sourceops

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestTypedAndLegacyReleaseShareTheSamePhysicalDebt(t *testing.T) {
	s := New()
	lease, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.Begin()
	if err != nil || s.Active() != 2 {
		t.Fatal("typed handle replaced legacy accounting", s.Active(), err)
	}
	s.Close()
	lease.Release()
	lease.Release()
	if s.Active() != 1 {
		t.Fatal("typed release refunded another borrow", s.Active())
	}
	legacy()
	legacy()
	if err := s.Wait(t.Context()); err != nil || s.Active() != 0 {
		t.Fatal("physical scope did not join", err, s.Active())
	}
	if _, err := s.Acquire(); !errors.Is(err, ErrRootLease) {
		t.Fatal("closed owner admitted typed debt", err)
	}
}

func TestTypedDeadlinePreservesOwnerCancellationAndStop(t *testing.T) {
	for _, unbind := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			owner := New()
			deadline := NewDeadline(context.Background(), time.Second)
			defer deadline.Cancel()
			deadline.BindOwner(owner)
			if unbind {
				deadline.UnbindOwner()
			}
			owner.Close()
			synctest.Wait()
			if unbind {
				if err := deadline.Context().Err(); err != nil {
					t.Fatal("stopped owner callback cancelled deadline", err)
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if !errors.Is(deadline.Context().Err(), context.DeadlineExceeded) {
					t.Fatal("original deadline changed", deadline.Context().Err())
				}
			} else if !errors.Is(deadline.Context().Err(), context.Canceled) {
				t.Fatal("owner cancellation was lost", deadline.Context().Err())
			}
		})
	}
}
