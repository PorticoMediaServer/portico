//go:build !windows

package preparedmedia

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestPreparedCustodyUsesValidStableNamespacedLocks(t *testing.T) {
	locks, err := livechannels.NewPhysicalLocks(filepath.Join(canonicalFixtureDir(t), "locks"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{locks: locks}
	id := strings.Repeat("a", 64)
	custody, err := s.acquireCustody(id)
	if err != nil {
		t.Fatal("job digest incompatible with physical lock API", err)
	}
	defer custody.Close()
	if next, err := s.acquireCustody(id); !errors.Is(err, livechannels.ErrPhysicalBusy) {
		if next != nil {
			next.Close()
		}
		t.Fatal("same job admitted while prior attempt still owns custody", err)
	}
	object, err := s.objectGate(id)
	if err != nil {
		t.Fatal("object and job lock namespaces collide", err)
	}
	defer object.Close()
	if gate, err := s.objectGate(id); !errors.Is(err, livechannels.ErrPhysicalBusy) {
		if gate != nil {
			gate.Close()
		}
		t.Fatal("live object gate lost", err)
	}
	other, err := s.acquireCustody(strings.Repeat("b", 64))
	if err != nil {
		t.Fatal("independent job blocked", err)
	}
	other.Close()
	custody.Close()
	next, err := s.acquireCustody(id)
	if err != nil {
		t.Fatal("retired job cannot recover", err)
	}
	next.Close()
}
