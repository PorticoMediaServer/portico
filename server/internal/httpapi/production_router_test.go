package httpapi

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	library "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackruntime"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
)

func TestProductionRouterConstructsWithEveryMediaRoute(t *testing.T) {
	d, _ := logoutHTTPFixture(t)
	// Enable the same conditional route families as cmd/server. These services
	// are only used for registration; no workers, decoders or network calls run.
	d.Playback = playback.New(d.DB)
	d.PlaybackRuntime = &playbackruntime.Runtime{Linear: &playbackruntime.LinearRuntime{}}
	d.LiveChannels = &livechannels.Store{}
	d.LibraryChannels = &library.Store{}
	d.Prepared = &preparedmedia.Service{}
	d.Downloads = &downloads.Service{}
	d.Mounts = &mounts.Service{}
	d.RemoteSources = &remotesources.Service{}
	d.Networking = &networking.ClaimHandler{}
	d.RouteIdentity = http.NotFoundHandler()
	if New(d) == nil {
		t.Fatal("production router was not constructed")
	}
	// Check every media pattern, including grant values that used to expose
	// the overlap. Go must be able to mount all of them, not just the registry.
	for pattern := range routeLanes {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok || !strings.HasPrefix(path, "/v1/media/") {
			continue
		}
		path = pathParameter.ReplaceAllString(path, "linear")
		if got := RoutePattern(httptest.NewRequest(method, path, nil)); got != muxPattern(method, strings.TrimPrefix(pattern, method+" ")) {
			t.Errorf("%s matched %s", pattern, got)
		}
	}
}

func TestAllProductionRoutePatternsAreCompatible(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	patterns := map[string]bool{}
	// Include every declared hand-registered family, even optional networking
	// routes whose managers are absent in a minimal startup fixture.
	for pattern := range routeLanes {
		method, path, ok := strings.Cut(pattern, " ")
		if ok {
			if path == "" {
				continue
			}
			pattern = muxPattern(method, path)
		}
		patterns[pattern] = true
	}
	for _, route := range registry.Definitions() {
		patterns[muxPattern(route.Method, route.Path)] = true
	}
	ordered := make([]string, 0, len(patterns))
	for pattern := range patterns {
		ordered = append(ordered, pattern)
	}
	sort.Strings(ordered)
	mux := http.NewServeMux()
	for _, pattern := range ordered {
		// Any ambiguity panics here, just as it does when New mounts the real
		// handlers. A no-op handler isolates registration from media execution.
		mux.Handle(pattern, http.NotFoundHandler())
	}
}
