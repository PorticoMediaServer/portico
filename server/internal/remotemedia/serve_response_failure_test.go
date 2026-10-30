package remotemedia

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestServeFailuresDoNotInventProviderReports(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503, 507} {
		for _, operation := range []string{"range", "sequential", "stat", "list", "visit", "metadata", "directory"} {
			t.Run(http.StatusText(status)+"/"+operation, func(t *testing.T) {
				budget := NewOperationBudget(context.Background(), 1)
				endpoint, err := NewServeEndpoint(12345, "private", "private", budget)
				if err != nil {
					t.Fatal(err)
				}
				defer endpoint.Close()
				endpoint.client.Transport = serveRoundTrip(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"50400"}}, Body: http.NoBody}, nil
				})
				ctx := context.Background()
				check := func(context.Context) error { return nil } // Transport fixture, no production authority.
				metadata := &RcloneMetadata{endpoint: endpoint, fs: "fixture:/"}
				switch operation {
				case "range":
					_, err = endpoint.ReadRange(ctx, "video", `"old"`, 10, 0, 1, check)
				case "sequential":
					_, err = endpoint.OpenFromStart(ctx, "book", `"old"`, 10, check)
				case "stat":
					_, err = endpoint.Stat(ctx, "video", check)
				case "list":
					_, err = endpoint.ListSnapshot(ctx, "", check)
				case "visit":
					err = endpoint.VisitSnapshot(ctx, "", check, func(DAVEntry) error { return nil })
				case "metadata":
					_, err = metadata.Stat(ctx, "video", check)
				case "directory":
					err = metadata.VisitDirectory(ctx, "", check, func(RcloneFileMetadata) error { return nil })
				}
				want := ErrUnavailable
				if status == 401 || status == 403 {
					want = ErrDenied
				}
				var provider *ResponseFailure
				if !errors.Is(err, want) || errors.As(err, &provider) {
					t.Fatalf("local status %d became provider state: %v", status, err)
				}
				if counts := budget.Counts(); counts.Active != 0 || counts.Peak != 1 {
					t.Fatal("failed operation retained or bypassed source capacity", counts)
				}
			})
		}
	}
}
