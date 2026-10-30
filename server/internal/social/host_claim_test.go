package social

import "testing"

func TestHostClaimReadinessRemainsRequiredAfterGovernanceWindow(t *testing.T) {
	const now int64 = 500000
	g := record{state: "host-reconnecting-paused", hostAuthority: "host-only", hostSeenMS: now - HostPauseSeconds*1000, pauseAfter: HostPauseSeconds, itemID: "item", playbackRevision: 4}
	ready := hostClaimFacts{role: "member", state: "joined", readiness: "ready", seenMS: now, readyMS: now, readyRevision: 4}
	if !hostClaimAllowed(g, ready, now) {
		t.Fatal("capable member refused at pause boundary")
	}
	for _, check := range []struct {
		name   string
		change func(*hostClaimFacts)
	}{
		{"unready", func(f *hostClaimFacts) { f.readiness = "buffering" }},
		{"expired presence", func(f *hostClaimFacts) { f.seenMS = now - HostGraceSeconds*1000 - 1 }},
		{"expired readiness", func(f *hostClaimFacts) { f.readyMS = now - ReadinessStaleSeconds*1000 - 1 }},
		{"stale playback revision", func(f *hostClaimFacts) { f.readyRevision-- }},
		{"left", func(f *hostClaimFacts) { f.state = "left" }},
		{"sitting host", func(f *hostClaimFacts) { f.role = "host" }},
	} {
		t.Run(check.name, func(t *testing.T) {
			f := ready
			check.change(&f)
			if hostClaimAllowed(g, f, now) {
				t.Fatal("ineligible member accepted")
			}
		})
	}
	if hostClaimAllowed(g, ready, now-1) {
		t.Fatal("member accepted before pause boundary")
	}
	g.state = "ended"
	if hostClaimAllowed(g, ready, now) {
		t.Fatal("ended group accepted claim")
	}
}
