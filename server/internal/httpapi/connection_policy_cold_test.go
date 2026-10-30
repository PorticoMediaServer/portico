package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestColdConnectionPolicyReadsStoredSettingsWithoutToolValidation(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	current, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := current.Requested
	values.LANNetworks = []string{"192.0.2.0/24"}
	values.TrustedProxies = []string{"203.0.113.9/32"}
	values.AdvertiseURL = "https://media.example"
	values.AccessURLs = []string{"https://other.example"}
	values.APIRequestsPerMinute = 731
	values.RemoteSignInPolicy = "owner-only"
	values.DLNAEnabled = true
	values.SubtitleTrackLimit = 128
	values.SubtitleFileLimitMiB = 96
	principal, err := f.deps.Identity.Authenticate(f.owner)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.deps.Console.ApplySettings(ctx, principal, operations.AllowServerScope, operations.SettingsChange{
		ExpectedRevision: current.Revision, IdempotencyKey: "cold-policy-settings", Values: values,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		medialimits.ConfigureSubtitles(medialimits.DefaultSubtitleTracks, medialimits.DefaultSubtitleFileMiB)
	})
	f.deps.Console.FFmpegValidation = func(context.Context, string) operations.FFmpegStatus {
		panic("request-entry connection policy invoked media-tool validation")
	}
	f.deps.connectionPolicy = &connectionPolicyCache{}
	handler := New(f.deps)
	// Simultaneous first requests share the one cold initializer. There is no
	// warm-up request and no tool-status callback may be reached by any caller.
	const callers = 8
	start := make(chan struct{})
	results := make(chan int, callers)
	var workers sync.WaitGroup
	for i := 0; i < callers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			r := httptest.NewRequest("GET", "http://media.example/v1/system", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			results <- w.Code
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for code := range results {
		if code != 200 {
			t.Fatalf("cold system request status=%d", code)
		}
	}
	// New owns its own cache. Independently exercise the same cold resolver's
	// stored projection rather than assuming the caller retains that cache.
	policy, err := f.deps.connectionSetting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if policy == nil || policy.revision != saved.Revision || policy.apiPerMinute != 731 || policy.remoteSignIn != "owner-only" || !policy.dlna {
		t.Fatalf("stored policy not installed: %+v", policy)
	}
	if !reflect.DeepEqual(policy.accessURLs, []string{"https://media.example", "https://other.example"}) ||
		len(policy.locality.lan) != 1 || policy.locality.lan[0].String() != "192.0.2.0/24" ||
		len(policy.locality.proxies) != 1 || policy.locality.proxies[0].String() != "203.0.113.9/32" ||
		medialimits.SubtitleTracks() != 128 || medialimits.SubtitleFileBytes() != 96<<20 {
		t.Fatalf("stored network/subtitle policy changed: %+v", policy)
	}
}

func TestCanceledColdConnectionPolicyDoesNotWaitForInitializer(t *testing.T) {
	f := administrationFixtureNew(t)
	f.deps.connectionPolicy = &connectionPolicyCache{}
	f.deps.Console.FFmpegValidation = func(context.Context, string) operations.FFmpegStatus {
		panic("canceled request invoked media-tool validation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.deps.connectionPolicy.mu.Lock()
	result := make(chan error, 1)
	go func() {
		_, err := f.deps.connectionSetting(ctx)
		result <- err
	}()
	select {
	case err := <-result:
		f.deps.connectionPolicy.mu.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled cold policy returned %v", err)
		}
	case <-time.After(time.Second):
		f.deps.connectionPolicy.mu.Unlock()
		<-result
		t.Fatal("already-canceled cold request waited for another initializer")
	}
	if f.deps.connectionPolicy.setting.Load() != nil {
		t.Fatal("canceled request published a connection policy")
	}
}
