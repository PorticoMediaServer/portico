package playback

import (
	"context"
	"testing"
)

func TestOwnerCanReadAndClearRememberedRoutes(t *testing.T) {
	_, s, p, _, item, _ := activityFixture(t)
	ctx := context.Background()
	s.ConfigureHLS(&HLS{})
	first, err := s.Create(p, item, "automatic", "memory-original")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordRouteFailure(ctx, p, first.ID, "decode_error", "decoder stopped"); err != nil {
		t.Fatal(err)
	}
	page, err := s.RouteFailureMemory(ctx)
	if err != nil || len(page.Items) != 1 || page.Items[0].Route != "original" || page.Items[0].ExpiresAtMS <= page.Items[0].ReportedAtMS {
		t.Fatal(page, err)
	}
	if err = s.ClearRouteFailureMemory(ctx, page.Items[0].SourceID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Create(p, item, "automatic", "memory-cleared")
	if err != nil || retry.Mode != "direct" {
		t.Fatal("clear did not restore original route", retry, err)
	}
	page, err = s.RouteFailureMemory(ctx)
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
}
