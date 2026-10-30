package identity

import (
	"errors"
	"testing"
)

func TestPasswordHasherAdmissionIsNotBadCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, _ := directFixture(t)
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	if _, e := s.Login("owner", "Testing1!"); !errors.Is(e, ErrBusy) || errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	if len(s.hashSlots) != 2 {
		t.Fatal("rejected request consumed a slot")
	}
}
