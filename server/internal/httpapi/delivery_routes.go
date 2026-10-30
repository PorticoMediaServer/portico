package httpapi

import (
	"context"
	"golang.org/x/text/language"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
)

// deliveryContext is the edge's own observation of one request: whether it
// comes from inside the owner's home networks, and what the client says it is
// on. Network class is resolved here and nowhere else.
func (d Dependencies) deliveryContext(r *http.Request) playback.DeliveryContext {
	if r == nil {
		return playback.DeliveryContext{NetworkClass: playback.NetworkUnknown, ServerLocality: playback.LocalityUnknown, TransportClass: "unknown"}
	}
	// One rule says whether a request is inside the owner's home networks, for
	// every purpose: the same one that decides a member's remote limits.
	class, locality := playback.NetworkClassOf(d.requestIsRemote(r))
	return playback.DeliveryContext{NetworkClass: class, ServerLocality: locality, TransportClass: playback.NormalizeTransportClass(r.Header.Get(playback.TransportClassHeader)), DeviceClass: deviceClassHeader(r), AppLanguage: requestAppLanguage(r)}
}

// App language is a device presentation fact, never an authorization input.
func requestAppLanguage(r *http.Request) string {
	if raw := r.Header.Get("X-Portico-App-Language"); len(raw) <= 64 && raw != "" {
		if tag, err := language.Parse(raw); err == nil && tag != language.Und {
			return tag.String()
		}
	}

	return ""
}

// DeviceClassHeader lets a client name the surface it is rendering on, which is
// the scope of its device-class preferences.
const DeviceClassHeader = "X-Portico-Device-Class"

func deviceClassHeader(r *http.Request) string {
	if r == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get(DeviceClassHeader))) {
	case "web":
		return "web"
	case "tablet":
		return "tablet"
	case "mobile":
		return "mobile"
	case "television", "tv":
		return "television"
	}
	return ""
}

// withDelivery attaches the edge observation to the request context so session
// creation, offers and quality changes all resolve against the same lane.
func (d Dependencies) withDelivery(r *http.Request) *http.Request {
	return r.WithContext(playback.WithDeliveryContext(r.Context(), d.deliveryContext(r)))
}

// deliveryRoutes publishes what delivery resolved to and what this host can
// actually encode with. Both are reads; nothing here changes a session.
func (d Dependencies) deliveryRoutes(mux *http.ServeMux) {
	// GET /v1/playback/delivery-policy — one indexed row read plus the viewer's
	// preference documents. Constant cost, no paging.
	d.httpHandle(mux, "GET /v1/playback/delivery-policy", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		if r.URL.RawQuery != "" {
			failure(w, errInvalidDeliveryQuery)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		edge := d.deliveryContext(r)
		cfg := playback.DefaultDeliveryConfiguration()
		if d.Playback != nil {
			cfg = d.Playback.DeliveryConfiguration()
		}
		var values playback.DeliveryPreferenceReader
		if d.DB != nil {
			gated, txErr := dbwork.BeginSnapshot(ctx, d.DB)
			if txErr != nil {
				failure(w, txErr)
				return
			}
			tx := gated.Tx()
			read, sources, _, prefErr := operations.EffectivePreferences(tx, p.Viewer, edge.DeviceClass)
			_ = gated.Rollback()
			if prefErr == nil {
				values = playback.WithPreferenceSources(read, sources)
			}
		}
		policy := playback.ResolveDeliveryPolicy(values, edge.NetworkClass, edge.ServerLocality, edge.TransportClass, cfg)
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, map[string]any{"serverId": d.Identity.ID(), "policy": policy, "ladder": qualityladder.Menu(), "planningPolicies": playback.PlanningPolicies, "transportClasses": playback.TransportClasses, "savedQualities": playback.SavedQualities()})
	})

	// GET /v1/admin/transcode/capacity lives in telemetry_routes.go; its report
	// takes this service's hardware probes through playbackProbes.
}

var errInvalidDeliveryQuery = requestinput.New("Read delivery policy without additional options.")
