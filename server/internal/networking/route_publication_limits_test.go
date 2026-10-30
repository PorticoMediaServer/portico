package networking

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestHostedCandidatesStayTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, _, ctx := accessURLsFixture(t, func(_ context.Context) []string { return []string{"https://media.example"} })
	capture := captureAccessURLs(t, m, ctx)
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	if len(capture.observations) != 1 {
		t.Fatalf("publication count=%d", len(capture.observations))
	}
	for _, obs := range capture.observations {
		for _, c := range obs.Candidates {
			if !strings.HasPrefix(c.BaseURL, "https://") {
				t.Fatalf("Hosted publication contains plaintext route: %+v", c)
			}
		}
	}
}
func TestHostedCandidatesPreserveEveryLANWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, topo, ctx := accessURLsFixture(t, func(_ context.Context) []string {
		return []string{"https://one.example", "https://two.example", "https://three.example", "https://four.example", "https://five.example", "https://six.example", "https://seven.example", "https://eight.example"}
	})
	installSyntheticCertificate(t, m.h.certificates, ctx)
	m.h.certificates.SetListening(32500, true)
	topo.LAN = []string{"192.168.1.20", "192.168.1.21", "192.168.1.22", "192.168.1.23", "192.168.1.24"}
	capture := captureAccessURLs(t, m, ctx)
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	if len(capture.observations) != 1 {
		t.Fatalf("publication count=%d", len(capture.observations))
	}
	for _, obs := range capture.observations {
		if got := manualCandidates(obs); len(got) != 8 || got[0] != "https://one.example" {
			t.Fatalf("owner addresses lost priority: %v", got)
		}
		if len(obs.Candidates) != 13 {
			t.Fatalf("Hosted publication lost a real LAN address: %d candidates", len(obs.Candidates))
		}
	}
}

func TestPublishedCandidatesDeduplicateAndKeepManualPriority(t *testing.T) {
	in := []RemoteCandidate{{BaseURL: "https://preferred.example", Class: "manual"}, {BaseURL: "http://127.0.0.1:32500", Class: "lan"}, {BaseURL: "https://preferred.example", Class: "public"}, {BaseURL: "https://second.example", Class: "manual"}}
	out := boundedRouteCandidates(in)
	if len(out) != 2 || out[0].BaseURL != "https://preferred.example" || out[0].Class != "manual" || out[1].BaseURL != "https://second.example" {
		t.Fatalf("selected candidates: %+v", out)
	}
}

func TestPublishedCandidatesShareEightyCandidateBudget(t *testing.T) {
	in := []RemoteCandidate{}
	for i := 0; i < 100; i++ {
		in = append(in, RemoteCandidate{BaseURL: fmt.Sprintf("https://candidate-%d.example", i), Class: "manual"})
	}
	out := boundedRouteCandidates(in)
	if len(out) != 80 || out[0].BaseURL != in[0].BaseURL || out[79].BaseURL != in[79].BaseURL {
		t.Fatalf("candidate budget len=%d", len(out))
	}
}
