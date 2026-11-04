package downloads

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestDownloadQualityAskAndSavedChoices(t *testing.T) {
	for _, tc := range []struct {
		saved, requested, want string
		required               bool
	}{
		{"ask", "", "", true},
		{"ask", "720p-high", "720p-high", false},
		{"1080p-high", "", "1080p-high", false},
	} {
		t.Run(tc.saved+"/"+tc.requested, func(t *testing.T) {
			h := newHarness(t)
			ctx := playback.WithDeliveryContext(context.Background(), playback.DeliveryContext{DeviceClass: "web"})
			_, err := operations.New(h.db).ApplyPreferences(ctx, h.viewer, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{
				Scope: operations.ScopeDeviceClass, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "download-quality",
				Values: operations.PreferencePatch{"quality.downloads": tc.saved},
			})
			if err != nil {
				t.Fatal("save download quality", err)
			}
			g, err := dbwork.BeginSnapshot(ctx, h.db)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Rollback()
			got, err := requestedDownloadQuality(ctx, g.Tx(), h.viewer, tc.requested, source{Kind: "movie"})
			g.Rollback()
			if (err != nil) != tc.required || got != tc.want {
				t.Fatalf("saved=%q requested=%q: %q, %v", tc.saved, tc.requested, got, err)
			}
			if tc.required && !errors.Is(err, ErrQualityRequired) {
				t.Fatalf("missing choice = %v, want ErrQualityRequired", err)
			}
			view, err := h.service.Options(ctx, h.viewer, h.item, true)
			if err != nil || view.DefaultQuality != tc.saved || len(view.Options) == 0 {
				t.Fatalf("quality menu must remain readable: %+v %v", view, err)
			}
			if tc.saved == "1080p-high" {
				request := Request{OperationID: "saved-video-replay", MediaID: h.item}
				first, err := h.service.Submit(ctx, h.viewer, request, nil)
				if err != nil || len(first.Items) != 1 {
					t.Fatal(first, err)
				}
				_, err = operations.New(h.db).ApplyPreferences(ctx, h.viewer, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{
					Scope: operations.ScopeDeviceClass, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "video-now-ask",
					Values: operations.PreferencePatch{"quality.downloads": "ask"},
				})
				if err != nil {
					t.Fatal(err)
				}
				replay, err := h.service.Submit(ctx, h.viewer, request, nil)
				if err != nil || !replay.Duplicate || replay.BatchID != first.BatchID || len(replay.Items) != 1 || replay.Items[0].Quality != first.Items[0].Quality {
					t.Fatal("preference change retargeted video replay", replay, err)
				}
			}
		})
	}
}

func TestContainerDownloadQualityAskAndSavedChoices(t *testing.T) {
	for _, tc := range []struct {
		saved, requested, want string
		required               bool
	}{
		{"ask", "", "", true},
		{"ask", "720p-high", "720p-high", false},
		{"1080p-high", "", "1080p-high", false},
	} {
		t.Run(tc.saved+"/"+tc.requested, func(t *testing.T) {
			h := requestHarness(t, 1)
			ctx := playback.WithDeliveryContext(context.Background(), playback.DeliveryContext{DeviceClass: "web"})
			_, err := operations.New(h.db).ApplyPreferences(ctx, h.viewer, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{
				Scope: operations.ScopeDeviceClass, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "container-download-quality",
				Values: operations.PreferencePatch{"quality.downloads": tc.saved},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := newContainerRequest("quality-choice", h.show)
			request.Quality = RequestQuality(tc.requested)
			view, err := h.service.SubmitContainer(ctx, h.viewer, request)
			if (err != nil) != tc.required {
				t.Fatalf("container saved=%q requested=%q: %+v %v", tc.saved, tc.requested, view, err)
			}
			if tc.required {
				if !errors.Is(err, ErrQualityRequired) {
					t.Fatalf("missing container choice = %v, want ErrQualityRequired", err)
				}
				var count int
				if err := h.db.QueryRow(`SELECT count(*) FROM download_requests`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("missing quality admitted container work: %d %v", count, err)
				}
				return
			}
			var stored string
			if err := h.db.QueryRow(`SELECT quality FROM download_requests WHERE id=?`, view.RequestID).Scan(&stored); err != nil || stored != tc.want {
				t.Fatalf("pinned container quality = %q, %v", stored, err)
			}
			if tc.saved == "1080p-high" {
				_, err = operations.New(h.db).ApplyPreferences(ctx, h.viewer, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{
					Scope: operations.ScopeDeviceClass, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "container-now-ask",
					Values: operations.PreferencePatch{"quality.downloads": "ask"},
				})
				if err != nil {
					t.Fatal(err)
				}
				replay, err := h.service.SubmitContainer(ctx, h.viewer, request)
				if err != nil || replay.RequestID != view.RequestID {
					t.Fatal("preference change retargeted container replay", replay, err)
				}
			}
		})
	}
}

func TestDownloadAskKeepsOtherMediaAndDeviceDefaults(t *testing.T) {
	h := newHarness(t)
	ctx := playback.WithDeliveryContext(context.Background(), playback.DeliveryContext{DeviceClass: "web"})
	_, err := operations.New(h.db).ApplyPreferences(ctx, h.viewer, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{
		Scope: operations.ScopeDeviceClass, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "web-ask-only",
		Values: operations.PreferencePatch{"quality.downloads": "ask"},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := dbwork.BeginSnapshot(ctx, h.db)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Rollback()
	for _, tc := range []struct{ kind, want string }{{"song", "320"}, {"audiobook_file", "original"}, {"ebook", "original"}, {"comic", "original"}} {
		got, err := requestedDownloadQuality(ctx, g.Tx(), h.viewer, "", source{Kind: tc.kind})
		if err != nil || got != tc.want {
			t.Fatalf("Ask changed %s default: %q %v", tc.kind, got, err)
		}
	}
	mobile := playback.WithDeliveryContext(context.Background(), playback.DeliveryContext{DeviceClass: "mobile"})
	if got, err := requestedDownloadQuality(mobile, g.Tx(), h.viewer, "", source{Kind: "movie"}); err != nil || got != "original" {
		t.Fatalf("web Ask changed mobile default: %q %v", got, err)
	}
	for _, key := range []string{"quality.home", "quality.remote", "quality.mobile"} {
		field, ok := operations.PreferenceFieldFor(key)
		if !ok {
			t.Fatal(key)
		}
		for _, value := range field.AllowedValues {
			if value == "ask" {
				t.Fatalf("Ask leaked into playback preference %s", key)
			}
		}
	}
}
