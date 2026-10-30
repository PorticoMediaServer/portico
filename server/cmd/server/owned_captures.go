package main

import (
	"path/filepath"
	"runtime"

	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
)

// initializeOwnedCaptures composes byte custody with the real source-policy
// adapter and the existing transactional playback/content authorizer. It does
// not enable disc scan/play admission or confer ScopePlayback on a caller.
func initializeOwnedCaptures(dependencies httpapi.Dependencies, cache string) (*ownedcapture.Service, error) {
	// The shared actual-filesystem growth primitive deliberately refuses other
	// hosts. Unsupported capture must not prevent their ordinary server startup.
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, nil
	}
	producer, err := remotesources.NewCaptureProducer(remotesources.CaptureProducerOptions{
		Sources: dependencies.RemoteSources, Identities: dependencies.Identity, AuthorizeTx: dependencies.AuthorizeSubtitles,
	})
	if err != nil {
		return nil, err
	}
	return ownedcapture.New(ownedcapture.Options{DB: dependencies.DB, Directory: filepath.Join(cache, "owned-disc-captures"), Producer: producer})
}
