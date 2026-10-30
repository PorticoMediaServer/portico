package mediaexec

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
)

func TestConcurrentNetworkReportsKeepUnconfinedPosture(t *testing.T) {
	resetForTest(t)
	const workers = 32
	const rounds = 256
	const reason = "optional loopback command admitted host networking"
	// Exercise competing publications without production hooks. Reaching the
	// old unlock-before-publication window is still scheduler-dependent.
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for round := 0; round < rounds; round++ {
		networkPosture.Lock()
		networkPosture.unconfined, networkPosture.reason = false, ""
		networkPosture.Unlock()
		reportNetworkIsolation(true, "")
		start := make(chan struct{})
		done := make(chan struct{}, workers)
		for worker := 0; worker < workers; worker++ {
			isolated := worker != round%workers
			go func() {
				<-start
				reportNetworkIsolation(isolated, reason)
				done <- struct{}{}
			}()
		}
		close(start)
		for worker := 0; worker < workers; worker++ {
			select {
			case <-done:
			case <-deadline.C:
				t.Fatalf("concurrent network reports did not finish in round %d", round)
			}
		}
		found := false
		for _, status := range capabilityreport.All() {
			if status.Capability != capabilityreport.DecoderNetworkIsolation {
				continue
			}
			found = true
			if status.Available || status.Code != "decoder_network_unconfined" || status.Detail != reason {
				t.Fatalf("round %d lost the admitted host-network posture: %+v", round, status)
			}
		}
		if !found {
			t.Fatalf("network posture missing in round %d", round)
		}
	}
}
