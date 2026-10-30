package mediaanalysis

import (
	"testing"
	"time"
)

func TestSonicEstimateNeedsActualLibraryPublications(t *testing.T) {
	s := &Service{}
	if s.sonicRemainingSeconds("music", 10) != nil {
		t.Fatal("queue length invented a throughput")
	}
	s.recordSonicProgress("music", 2, time.Second)
	if s.sonicRemainingSeconds("music", 10) != nil {
		t.Fatal("single publication presented as a stable rate")
	}
	s.recordSonicProgress("music", 2, 3*time.Second)
	got := s.sonicRemainingSeconds("music", 10)
	if got == nil || *got != 10 {
		t.Fatal("estimate not based on actual active elapsed time", got)
	}
	if s.sonicRemainingSeconds("other", 10) != nil || s.sonicRemainingSeconds("music", 0) != nil {
		t.Fatal("estimate crossed library or survived completion")
	}
	s.recordSonicProgress("music", 0, time.Hour)
	s.recordSonicProgress("music", 2, 0)
	if got = s.sonicRemainingSeconds("music", 10); got == nil || *got != 10 {
		t.Fatal("nonpublication changed rate", got)
	}
	for range 40 {
		s.recordSonicProgress("music", 1, time.Second)
	}
	if len(s.sonicProgress["music"]) != 32 {
		t.Fatal("unbounded observations")
	}
}
