package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"time"
)

func (d Dependencies) playbackDiagnosticsRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/playback/diagnostics", d.playbackDiagnosticsHandler(d.Playback.DeliveryDiagnostics))
}

// diagnosticsCapacity composes the transcoding half of the overview. It holds
// its own composer, and therefore its own reporter cache, so this diagnostic
// never waits behind a Server overview poll or the other way round.
func (d Dependencies) diagnosticsCapacity() func(context.Context) (telemetry.Report, error) {
	if d.Console == nil {
		return nil
	}
	composer := d.capacityComposer()
	return func(ctx context.Context) (telemetry.Report, error) {
		document, e := d.Console.Settings(ctx, operations.AllowServerScope)
		if e != nil {
			return telemetry.Report{}, e
		}
		return composer.Report(ctx, transcodeSettings(document.Effective), telemetry.SessionCounts{}), nil
	}
}

// playbackOverview is the delivery diagnostic an owner reads. It embeds the
// playback service's own document, so the delivery workstream keeps sole
// authority over those fields, and adds what the transcoding administration
// surface publishes beside them.
type playbackOverview struct {
	playback.DeliveryDiagnostics
	Hardware   telemetry.HardwareStatus `json:"hardware"`
	Sessions   telemetry.SessionCounts  `json:"sessions"`
	Throughput playbackThroughput       `json:"throughput"`
}

// playbackThroughput reports how much media the server converts per wall-clock
// second. A null value means the delivery service publishes no progress figure
// yet; it is never reported as zero, which would read as "converting nothing".
type playbackThroughput struct {
	ConvertedSecondsPerSecond *float64 `json:"convertedSecondsPerSecond"`
	Detail                    string   `json:"detail,omitempty"`
}

// overview composes the extended document. A capacity report that cannot be
// built leaves the added sections at their empty shapes rather than failing the
// diagnostic, which must stay readable when subsystems are struggling.
func (d Dependencies) overview(ctx context.Context, diagnostics playback.DeliveryDiagnostics, capacity func(context.Context) (telemetry.Report, error)) playbackOverview {
	out := playbackOverview{DeliveryDiagnostics: diagnostics, Hardware: telemetry.HardwareStatus{
		Probes: []telemetry.HardwareProbe{}, Devices: []decoder.HardwareFactDevice{}, Formats: []decoder.HardwareFactFormat{},
	}}
	out.Sessions.Active = diagnostics.ActiveConversionSessions
	out.Throughput.Detail = "The delivery service does not yet publish converted media progress, so no rate can be reported."
	if capacity == nil {
		return out
	}
	report, e := capacity(ctx)
	if e != nil {
		return out
	}
	out.Hardware, out.Sessions = report.Hardware, report.Sessions
	out.Sessions.Active = diagnostics.ActiveConversionSessions
	return out
}
func (d Dependencies) playbackDiagnosticsHandler(read func(context.Context) (playback.DeliveryDiagnostics, error)) http.HandlerFunc {
	slots := make(chan struct{}, 4)
	capacity := d.diagnosticsCapacity()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		before, e := d.supportOwner(ctx, r)
		if e != nil {
			operationsAuthFailure(w, e)
			return
		}
		if r.URL.RawQuery != "" {
			write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_playback_diagnostics", "message": "Refresh playback observations without additional options.", "retryable": false}})
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			write(w, 429, map[string]any{"error": map[string]any{"code": "playback_diagnostics_busy", "message": "Playback observations are busy. Try again shortly.", "retryable": true}})
			return
		}
		diagnostics, e := read(ctx)
		observed := time.Now().UTC()
		after, authErr := d.supportOwner(ctx, r)
		if authErr != nil {
			operationsAuthFailure(w, authErr)
			return
		}
		if before.Hash != after.Hash || before.Epoch != after.Epoch || before.ProfileID != after.ProfileID {
			write(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized", "message": "Authentication is required.", "retryable": false}})
			return
		}
		if e != nil || ctx.Err() != nil {
			write(w, 503, map[string]any{"error": map[string]any{"code": "playback_diagnostics_unavailable", "message": "Playback observations could not be read. Try again.", "retryable": true}})
			return
		}
		write(w, 200, map[string]any{"scope": map[string]string{"serverId": d.Identity.ID(), "viewerFence": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", before.Hash, before.Epoch))))}, "observedAt": observed.Format(time.RFC3339Nano), "freshUntil": observed.Add(30 * time.Second).Format(time.RFC3339Nano), "diagnostics": d.overview(ctx, diagnostics, capacity)})
	}
}
