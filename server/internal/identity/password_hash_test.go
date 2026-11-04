package identity

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestInvitationPasswordHashingSharesTheTwoIdentitySlots(t *testing.T) {
	s := &Service{hashSlots: make(chan struct{}, 2)}
	started, waiting := make(chan struct{}, 8), make(chan struct{}, 8)
	s.hashSlotWaiting = func() { waiting <- struct{}{} }
	release := make(chan struct{})
	var active, peak atomic.Int32
	var workers sync.WaitGroup
	defer workers.Wait()
	defer close(release)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := s.hashPassword(context.Background(), "Correct-Horse-9", func(_ []byte, cost int) ([]byte, error) {
				if cost != PasswordCost {
					t.Errorf("password cost=%d", cost)
				}
				n := active.Add(1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				started <- struct{}{}
				<-release
				active.Add(-1)
				return []byte("fixture-hash"), nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	hashing, queued := 0, 0
	for range 8 {
		select {
		case <-started:
			hashing++
		case <-waiting:
			queued++
		}
	}
	if hashing != 2 || queued != 6 || peak.Load() != 2 {
		t.Fatalf("unbounded bcrypt execution: hashing=%d queued=%d peak=%d", hashing, queued, peak.Load())
	}
}

func TestInvitationPasswordHashUsesOccupiedSharedSlotsAndReleasesThem(t *testing.T) {
	s := &Service{hashSlots: make(chan struct{}, 2)}
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.HashPassword(ctx, "Correct-Horse-9"); !errors.Is(err, context.Canceled) {
		t.Fatalf("hash bypassed occupied shared slots: %v", err)
	}
	if len(s.hashSlots) != 2 {
		t.Fatal("refused hash changed existing slot ownership")
	}
	<-s.hashSlots
	<-s.hashSlots
	hash, err := s.HashPassword(context.Background(), "Correct-Horse-9")
	if err != nil || len(s.hashSlots) != 0 || bcrypt.CompareHashAndPassword(hash, []byte("Correct-Horse-9")) != nil {
		t.Fatalf("valid hash/release: %v slots=%d", err, len(s.hashSlots))
	}
}

func TestInvitationHasherFailureReleasesItsSlot(t *testing.T) {
	s := &Service{hashSlots: make(chan struct{}, 2)}
	failure := errors.New("hash failed")
	_, err := s.hashPassword(context.Background(), "Correct-Horse-9", func([]byte, int) ([]byte, error) { return nil, failure })
	if !errors.Is(err, failure) || len(s.hashSlots) != 0 {
		t.Fatalf("hasher failure leaked admission: %v slots=%d", err, len(s.hashSlots))
	}
}
