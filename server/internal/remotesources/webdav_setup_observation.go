package remotesources

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// These are private setup facts, not plaintext wrapper observations, immutable
// byte versions or runtime content authority. Publication binds every row to
// the saved config digest and source generation in the owner-save transaction.
type webDAVSetupObservation struct {
	Remote, Object string
	Size           int64
	Support        remotemedia.SetupRangeSupport
	Observed       int64
}

func aggregateWebDAVSetup(rows []webDAVSetupObservation) remotemedia.SetupRangeSupport {
	if len(rows) == 0 {
		return remotemedia.SetupRangeUnknown
	}
	result := remotemedia.SetupRangeSupported
	for _, row := range rows {
		if row.Support == remotemedia.SetupRangeUnsupported {
			return remotemedia.SetupRangeUnsupported
		}
		if row.Support != remotemedia.SetupRangeSupported {
			result = remotemedia.SetupRangeUnknown
		}
	}
	return result
}

func candidateWebDAVObserver(raw []byte, selected string, profile SourceReadProfile, policy remotemedia.Policy, check func(context.Context) error, output *[]webDAVSetupObservation) (candidateObserver, error) {
	if output == nil {
		return nil, storage.ErrRemoteConfig
	}
	leaves, err := configuredBackends(raw, selected)
	if err != nil {
		return nil, err
	}
	// A suffix on a wrapper needs an actual rclone mapping to its raw leaf.
	// This graph parser does not prove that mapping, so it must stay unknown.
	exact := true
	*output = nil
	for _, leaf := range leaves {
		if leaf.Kind == "webdav" {
			if !leaf.RawRootKnown {
				exact = false
			}
			*output = append(*output, webDAVSetupObservation{Remote: leaf.Remote, Support: remotemedia.SetupRangeUnknown})
		}
	}
	if len(*output) == 0 {
		return nil, nil
	}
	return func(ctx context.Context, _ *rcloneprogram.ServeProcess, hold *remotemedia.ExclusiveTest, clients map[string]candidateMetadata) (func(context.Context) error, func(), error) {
		type pending struct {
			row        int
			diagnostic *remotemedia.RcloneWebDAVSetup
			file       remotemedia.DAVEntry
		}
		var pendingRows []pending
		cleanup := func() {
			for _, p := range pendingRows {
				p.diagnostic.Close()
			}
		}
		if !exact {
			return nil, cleanup, nil
		}
		for i := range *output {
			row := &(*output)[i]
			metadata, exists := clients[row.Remote]
			if !exists || metadata.client == nil {
				cleanup()
				return nil, cleanup, storage.ErrRemoteChanged
			}
			diagnostic, err := metadata.client.SetupWebDAV(ctx, hold, policy, check)
			if err != nil {
				cleanup()
				return nil, cleanup, err
			}
			if diagnostic == nil {
				continue
			}
			file := metadata.file
			if file == nil {
				diagnostic.Close()
				continue
			}
			row.Object, row.Size = file.Relative, file.Size
			pendingRows = append(pendingRows, pending{row: i, diagnostic: diagnostic, file: *file})
		}
		return func(ctx context.Context) error {
			defer cleanup()
			for _, p := range pendingRows {
				support, err := p.diagnostic.Diagnose(ctx, p.file, hold, profile.Limits.RequestsPerSecond, profile.Limits.BytesPerSecond, check)
				(*output)[p.row].Support = support
				(*output)[p.row].Observed = time.Now().UnixMilli()
				if err != nil {
					return err
				}
			}
			return check(ctx)
		}, cleanup, nil
	}, nil
}
