package operations

import (
	"context"
	"testing"
)

func TestIntegrationCompatibilitySettingRoundTripsWithoutReplacingOtherSettings(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx := context.Background()
	before, err := s.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	if before.Effective.IntegrationCompatibility {
		t.Fatal("compatibility must default off")
	}
	next := before.Requested
	next.IntegrationCompatibility = true
	changed, err := s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "compatibility-on", Values: next})
	if err != nil {
		t.Fatal(err)
	}
	reread, err := s.Settings(ctx, auth)
	if err != nil || !changed.Effective.IntegrationCompatibility || !reread.Effective.IntegrationCompatibility {
		t.Fatalf("compatibility not durable: %v", err)
	}
	if reread.Effective.MemoryCap != before.Effective.MemoryCap || reread.Effective.TranscodeLocation != before.Effective.TranscodeLocation || reread.Effective.APIRequestsPerMinute != before.Effective.APIRequestsPerMinute {
		t.Fatal("compatibility changed unrelated settings")
	}
}
