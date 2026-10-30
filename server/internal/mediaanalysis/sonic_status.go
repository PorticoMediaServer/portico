package mediaanalysis

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

// SonicStatus contains owner facts, not display copy or a work grant. Counts
// are committed, current-source song results, independently of runtime health.
// Disabling future analysis retains those results. No ETA is invented from a
// queue length, and a verified installation alone never claims inference.
type SonicStatus struct {
	Settings                  catalog.SonicSettings
	PolicyAllows              bool
	CanAnalyze                bool
	Total, Analyzed           int64
	Runtime                   sonic.Availability
	Work                      workpolicy.HeavyWorkFacts
	EstimatedRemainingSeconds *int64
}

func (s *Service) SonicStatus(ctx context.Context, library string, at time.Time, authorize func(*sql.Tx) error) (SonicStatus, error) {
	var out SonicStatus
	if s == nil || s.db == nil || at.IsZero() || authorize == nil {
		return out, ErrInput
	}
	tx, release, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer release()
	if err = authorize(tx); err != nil {
		return out, err
	}
	if out.Settings, err = catalog.SonicSettingsTx(ctx, tx, library); err != nil {
		return out, err
	}
	policy, err := catalog.ScanPolicyTx(ctx, tx, library)
	if err != nil {
		return out, err
	}
	out.PolicyAllows = policy.Allows(catalog.SonicOperation)
	runtime := s.sonicRuntime()
	out.Runtime = runtime.Availability()
	out.CanAnalyze = runtime.CanAttempt()
	if out.Work, err = workpolicy.ReadHeavyWorkTx(ctx, tx, "analysis", at); err != nil {
		return out, err
	}
	// Count songs rather than files: two CUE tracks share bytes but have separate
	// normalized vectors and exact analysis ranges. EXISTS prevents duplicate
	// inventory/source/version rows from inflating either number. Missing,
	// replaced, re-rooted or reconfigured source results are never current.
	err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(EXISTS(
	 SELECT 1 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id
	 JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=a.token
	 JOIN library_sources src ON src.id=o.source_id
	 JOIN analysis_heads h ON h.object_id=o.id AND h.source_revision=o.revision AND h.stage='sonic'
	 JOIN analysis_results r ON r.id=h.result_id
	 JOIN music_sonic_embeddings embedding ON embedding.result_id=r.id AND embedding.entity_id=item.id
	 LEFT JOIN music_virtual_tracks cue ON cue.asset_id=a.token AND cue.entity_id=item.id
	 WHERE link.entity_id=item.id AND link.available=1 AND a.available=1
	 AND o.retired=0 AND o.state='available' AND o.unsupported_reason=''
	 AND o.size=a.size AND o.modified_ns=a.modified_ns
	 AND src.enabled=1 AND src.library_id=library.library_id AND o.root_incarnation=src.incarnation
	 AND r.retired_ms=0 AND r.asset_id=a.token AND r.object_id=o.id AND r.source_revision=o.revision
	 AND r.stage='sonic' AND r.algorithm GLOB ? AND r.root_incarnation=src.incarnation AND r.configuration_generation=src.generation
	 AND (a.container<>'strm' OR EXISTS(SELECT 1 FROM analysis_probe_bindings probe WHERE probe.object_id=o.id AND probe.source_revision=o.revision AND probe.evidence=r.evidence))
	 AND embedding.model_id=? AND embedding.model_version=? AND embedding.model_sha256=?
	 AND embedding.source_start_us=CAST(round(COALESCE(cue.start_seconds,0)*1000000) AS INTEGER)
	 AND embedding.source_end_us=CAST(round(COALESCE(cue.end_seconds,a.duration)*1000000) AS INTEGER)
	)),0) FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id
	 WHERE item.kind=7 AND item.retired=0 AND library.library_id=? AND library.retired=0
	 AND EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id
	 JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=a.token JOIN library_sources src ON src.id=o.source_id
	 WHERE link.entity_id=item.id AND link.available=1 AND a.available=1 AND a.audio_codec<>'' AND a.duration>0
	 AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns
	 AND src.enabled=1 AND src.library_id=library.library_id AND o.root_incarnation=src.incarnation)`, Algorithm+":sonic:*", sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256, library).Scan(&out.Total, &out.Analyzed)
	if err == nil && out.PolicyAllows && out.Runtime.State == "ready" && out.Work.WindowOpen && out.Work.PausedUntil <= at.UnixMilli() {
		out.EstimatedRemainingSeconds = s.sonicRemainingSeconds(library, out.Total-out.Analyzed)
	}
	return out, err
}
