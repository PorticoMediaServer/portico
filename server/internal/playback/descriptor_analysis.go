package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strconv"
	"time"
)

var ErrAnalysisDisabled = storage.ErrRemoteAnalysisDisabled

// AnalyzeDescriptor is invoked only by the existing scanner. Playback-time
// resolution does not depend on this high-I/O opt-in. A failed analysis is not a
// missing descriptor; ingestion retains its inventory and records unknown facts.
func (r *Remote) AnalyzeDescriptor(parent context.Context, library, path string) (facts assets.Facts, err error) {
	if r == nil || r.io == nil {
		return facts, ErrAnalysisDisabled
	}
	var enabled bool
	var analysisRevision int64
	err = r.db.QueryRowContext(parent, `SELECT enabled,revision FROM source_strm_analysis WHERE library_id=?`, library).Scan(&enabled, &analysisRevision)
	if errors.Is(err, sql.ErrNoRows) || !enabled {
		return facts, ErrAnalysisDisabled
	}
	if err != nil {
		return facts, err
	}
	var scanRevision int64
	scanAllowed := func(ctx context.Context) (int64, error) {
		var revision int64
		var tier, raw string
		if e := r.db.QueryRowContext(ctx, `SELECT revision,tier,operations_json FROM library_scan_policies WHERE library_id=?`, library).Scan(&revision, &tier, &raw); e != nil {
			return 0, e
		}
		var ops []string
		if json.Unmarshal([]byte(raw), &ops) != nil || tier == "file_list_only" {
			return 0, ErrAnalysisDisabled
		}
		for _, op := range ops {
			if op == "probe" {
				return revision, nil
			}
		}
		return 0, ErrAnalysisDisabled
	}
	if scanRevision, err = scanAllowed(parent); err != nil {
		return facts, err
	}
	select {
	case r.probes <- struct{}{}:
		defer func() { <-r.probes }()
	default:
		return facts, remotemedia.ErrBusy
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	descriptor := &descriptorCapture{root: library}
	defer descriptor.Close()
	if r.io.IsRemote(path) {
		descriptor.object, err = r.io.Remote.Acquire(ctx, path, "analysis")
	} else {
		if r.AnalysisRoot == nil {
			return facts, storage.ErrRootLease
		}
		var lease *storage.RootLease
		lease, err = r.AnalysisRoot(ctx, library, path)
		if err == nil {
			descriptor.local, err = r.io.OpenObservedPlayback(ctx, lease)
		}
	}
	if err != nil {
		return facts, err
	}
	locator, err := descriptor.capture(ctx)
	if err != nil {
		return facts, err
	}
	policy, networkRevision, err := r.policy(library)
	if err != nil {
		return facts, err
	}
	var assetID string
	if err = r.db.QueryRowContext(ctx, `SELECT a.token FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id=? AND a.path=? ORDER BY a.token LIMIT 1`, library, path).Scan(&assetID); err != nil {
		return facts, err
	}
	object := assetID + ":uri:" + identity.Digest(locator)
	binding, err := remotemedia.ExactLocatorBinding(locator, library, object)
	if err != nil {
		return facts, err
	}
	target, err := remotemedia.DiscoverVersion(ctx, locator, policy, library, object, binding)
	if err != nil {
		return facts, err
	}
	current := func(ctx context.Context) error {
		revision, e := scanAllowed(ctx)
		if e != nil || revision != scanRevision {
			return ErrAnalysisDisabled
		}
		var active bool
		var rev int64
		e = r.db.QueryRowContext(ctx, `SELECT enabled,revision FROM source_strm_analysis WHERE library_id=?`, library).Scan(&active, &rev)
		if e != nil || !active || rev != analysisRevision {
			return ErrAnalysisDisabled
		}
		_, rev, e = r.policy(library)
		if e != nil {
			return e
		}
		if rev != networkRevision {
			return ErrSourcePolicyChanged
		}
		return nil
	}
	input := newSTRMObject(ctx, target, descriptor, current)
	defer input.Close()
	u, _ := url.Parse(locator)
	extension := filepath.Ext(u.Path)
	if extension == "" {
		extension = ".mp4"
	}
	facts, err = r.probe.InspectRemote(ctx, input, extension)
	if err != nil {
		return facts, remotemedia.ErrUnsupported
	}
	if _, err = descriptor.dependencies(ctx); err != nil {
		return facts, err
	}
	if err = current(ctx); err != nil {
		return facts, err
	}
	facts.Container = "strm"
	facts.AnalysisSourceEvidence = "remote:" + identity.Digest(target.Version().ID()+":"+descriptor.digest+":"+strconv.FormatInt(networkRevision, 10))
	return facts, nil
}
