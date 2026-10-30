package telemetry

import (
	"strings"
	"testing"
)

func healthyFacts() AttentionFacts {
	return AttentionFacts{
		TranscodingEnabled: true,
		TemporaryDirectory: TemporaryDirectory{Path: "/var/portico/hls", Ready: true},
		FFmpegOK:           true,
	}
}

func ids(items []Item) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func TestAHealthyServerNeedsNoAttention(t *testing.T) {
	if items := Attention(healthyFacts()); len(items) != 0 {
		t.Fatal("nothing should be raised on a healthy server", ids(items))
	}
}

func TestOverloadAlertHasPlainOwnerMessage(t *testing.T) {
	facts := healthyFacts()
	facts.Alerts = []OpenAlert{{ID: "overload", Code: "server_overloaded", Severity: "warning"}}
	items := Attention(facts)
	if len(items) != 1 || items[0].Title != "Server is overloaded" || !strings.Contains(items[0].Detail, "20 requests timed out in five minutes") {
		t.Fatalf("overload message: %+v", items)
	}
}

func TestStatePermissionsAlertExplainsAndOffersFix(t *testing.T) {
	facts := healthyFacts()
	facts.Alerts = []OpenAlert{{ID: "perms", Code: "state-permissions", Severity: "warning"}}
	items := Attention(facts)
	if len(items) != 1 || items[0].Severity != SeverityWarning || !strings.Contains(items[0].Detail, "Other accounts on this computer can read your Portico data") {
		t.Fatalf("permissions message: %+v", items)
	}
}

func TestEveryConditionIsRaisedAndOrderedByUrgency(t *testing.T) {
	ratio := 0.02
	facts := healthyFacts()
	facts.Alerts = []OpenAlert{{ID: "alert-one", Code: "storage-degraded", Severity: "warning"}}
	facts.UnavailableSources = []NamedRecord{{ID: "source-one", Name: "Films", Detail: "the server reported offline"}}
	facts.FailedScans = []NamedRecord{{ID: "scan-one", Name: "Music", Detail: "source_unreadable"}}
	facts.PausedScans = []NamedRecord{{ID: "scan-two", Name: "Shows"}}
	facts.CertificateExpiry = &CertificateExpiry{DaysRemaining: 9, DNSName: "*.abc.direct.getportico.tv"}
	facts.StorageFreeRatio = &ratio
	facts.TranscodingEnabled = false
	facts.ConversionsDeclined = 12
	facts.TemporaryDirectory = TemporaryDirectory{Path: "/var/portico/hls"}
	facts.FFmpegOK = false

	items := Attention(facts)
	for _, expected := range []string{"alert:alert-one", "source:source-one", "scan-failed:scan-one", "scan-paused:scan-two", "certificate", "storage", "transcoding-disabled", "temporary-directory", "ffmpeg"} {
		found := false
		for _, id := range ids(items) {
			found = found || id == expected
		}
		if !found {
			t.Fatal("missing item", expected, ids(items))
		}
	}
	lastRank := -1
	for _, item := range items {
		rank, ok := severityRank[item.Severity]
		if !ok {
			t.Fatal("unknown severity", item)
		}
		if rank < lastRank {
			t.Fatal("items must be ordered most urgent first", ids(items))
		}
		lastRank = rank
		if item.Title == "" || item.Detail == "" || item.Action.Kind == "" {
			t.Fatal("every item must say what it is and where to go", item)
		}
	}
	for _, item := range items {
		if item.ID == "certificate" && !strings.Contains(item.Detail, "9 days") {
			t.Fatal("the certificate item must say how long is left", item)
		}
		if item.ID == "transcoding-disabled" && !strings.Contains(item.Detail, "12 playback attempts") {
			t.Fatal("the transcoding item must say how often it mattered", item)
		}
	}
}

func TestQuietConditionsAreNotRaised(t *testing.T) {
	healthy := 0.5
	facts := healthyFacts()
	facts.CertificateExpiry = &CertificateExpiry{DaysRemaining: CertificateWarningDays + 1}
	facts.StorageFreeRatio = &healthy
	facts.TranscodingEnabled = false
	facts.ConversionsDeclined = 0
	if items := Attention(facts); len(items) != 0 {
		t.Fatal("a certificate in date, healthy storage, and transcoding nobody needed raise nothing", ids(items))
	}
	// Transcoding off only matters once a viewer actually needed it.
	facts.ConversionsDeclined = 1
	if items := Attention(facts); len(items) != 1 || items[0].ID != "transcoding-disabled" {
		t.Fatal("transcoding off with demand must be raised", ids(items))
	}
}

func TestAnExpiredCertificateIsCriticalAndTheListIsBounded(t *testing.T) {
	facts := healthyFacts()
	facts.CertificateExpiry = &CertificateExpiry{DaysRemaining: 0}
	items := Attention(facts)
	if len(items) != 1 || items[0].Severity != SeverityCritical {
		t.Fatal("an expired certificate is critical", items)
	}
	facts = healthyFacts()
	for i := 0; i < 200; i++ {
		facts.Alerts = append(facts.Alerts, OpenAlert{ID: "alert", Code: "code", Severity: "warning"})
	}
	if len(Attention(facts)) > 50 {
		t.Fatal("the list must stay one screen")
	}
}

func TestActivityQualifiedMajorIssuesHaveNoDiagnosticInference(t *testing.T) {
	if got := MajorIssues(nil, false, false, false); len(got) != 0 {
		t.Fatal(got)
	}
	got := MajorIssues([]NamedRecord{{ID: "source", Name: "Archive", Detail: "private credential"}}, true, true, true)
	if len(got) != 4 || got[0].Severity != "playback_stop" || got[1].Severity != "data_risk" || got[0].Destination.Target != "source" {
		t.Fatal(got)
	}
	for _, v := range got {
		if strings.Contains(v.Subject, "credential") {
			t.Fatal("raw source detail escaped")
		}
	}
}
