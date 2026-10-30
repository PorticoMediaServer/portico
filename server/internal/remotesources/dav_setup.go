package remotesources

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func davServeConfiguration(config remotemedia.DAVConfig) []byte {
	raw := "[portico]\ntype=webdav\nurl=" + config.Root + "\nvendor=other\n"
	if config.Username != "" || config.Password != "" {
		raw += "headers=Authorization,Basic " + base64.StdEncoding.EncodeToString([]byte(config.Username+":"+config.Password)) + "\n"
	}
	return []byte(raw)
}

// testDAVSetup retains the current source/profile/owner facts through a real
// temporary serve, then its setup-only direct diagnostic. The returned DB-only
// guard is reused in the existing config/receipt publication transaction.
func (s *Service) testDAVSetup(ctx context.Context, currentID string, source Source, config remotemedia.DAVConfig, authorize func(*sql.Tx) error) (remotemedia.SetupRangeSupport, func(*sql.Tx) error, error) {
	return s.testDAVSetupAt(ctx, currentID, source.Generation-1, source, config, authorize)
}

// testDAVSetupAt takes the exact expected generation instead of impersonating
// the pre-save generation. Existing save callers keep the narrow wrapper above.
func (s *Service) testDAVSetupAt(ctx context.Context, currentID string, expectedGeneration int64, source Source, config remotemedia.DAVConfig, authorize func(*sql.Tx) error) (remotemedia.SetupRangeSupport, func(*sql.Tx) error, error) {
	unknown := remotemedia.SetupRangeUnknown
	if authorize == nil {
		return unknown, nil, storage.ErrRemoteConfig
	}
	profile, _ := SourceReadPreset("list")
	profile.SourceID = source.ID
	if currentID != "" {
		snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return unknown, nil, err
		}
		profile, err = SourceReadProfileTx(ctx, snapshot.Tx(), currentID)
		snapshot.Rollback()
		if err != nil {
			return unknown, nil, err
		}
	}
	guard := func(tx *sql.Tx) error {
		if err := authorize(tx); err != nil {
			return err
		}
		if currentID != "" {
			var generation int64
			var root, kind string
			if err := tx.QueryRowContext(ctx, `SELECT generation,root,kind FROM remote_sources WHERE id=? AND removed=0`, currentID).Scan(&generation, &root, &kind); err != nil {
				return err
			}
			actual, err := SourceReadProfileTx(ctx, tx, currentID)
			if err != nil {
				return err
			}
			if generation != expectedGeneration || root != source.RootPath || kind != "webdav" || actual != profile {
				return storage.ErrRemoteChanged
			}
		}
		return nil
	}
	check := func(ctx context.Context) error {
		snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return err
		}
		err = guard(snapshot.Tx())
		snapshot.Rollback()
		if err != nil {
			return err
		}
		_, err = remotemedia.NewSourceDAV(config, s.resolver)
		if err != nil {
			return err
		}
		return (remotemedia.Policy{Resolver: s.resolver, SourceEndpoints: true, SourcePrivateOnly: strings.HasPrefix(config.Root, "http://")}).CheckEndpointApproval(ctx, config.Root, config.Approval)
	}
	if err := check(ctx); err != nil {
		return unknown, nil, err
	}
	program, err := rcloneprogram.Resolve()
	if err != nil {
		return unknown, nil, err
	}
	policy := remotemedia.Policy{Resolver: s.resolver, SourceEndpoints: true, SourcePrivateOnly: strings.HasPrefix(config.Root, "http://"), Approvals: []remotemedia.Approval{config.Approval}}
	support := unknown
	observe := func(ctx context.Context, _ *rcloneprogram.ServeProcess, hold *remotemedia.ExclusiveTest, metadata map[string]candidateMetadata) (func(context.Context) error, func(), error) {
		file := metadata["portico:"].file
		if file == nil {
			return nil, nil, nil
		}
		return func(ctx context.Context) error {
			// runCandidateServe has already stopped and physically waited for its
			// temporary process. The same hold remains live throughout this observer.
			dav, err := remotemedia.NewSourceDAV(config, s.resolver)
			if err != nil {
				return err
			}
			support, err = dav.DiagnoseSetupRanges(ctx, *file, hold, profile.Limits.RequestsPerSecond, profile.Limits.BytesPerSecond, check)
			return err
		}, nil, nil
	}
	raw := davServeConfiguration(config)
	defer clear(raw)
	generation := strconv.FormatInt(max(int64(1), expectedGeneration), 10)
	if err = s.runCandidateServe(ctx, currentID, generation, profile, raw, "portico:", program, "", policy, check, observe); err != nil {
		return unknown, nil, err
	}
	if err = check(ctx); err != nil {
		return unknown, nil, err
	}
	return support, guard, nil
}
