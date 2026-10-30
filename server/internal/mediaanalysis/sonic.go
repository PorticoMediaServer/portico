package mediaanalysis

import (
	"context"
	"database/sql"
	"io"
	"math"
	"os"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

type sonicTrackBinding struct {
	EntityID       int64
	StartUS, EndUS int64
}
type sonicItemAnalysis struct {
	Binding  sonicTrackBinding
	Analysis sonic.Analysis
}

func sonicBindingsTx(ctx context.Context, tx *sql.Tx, b binding) ([]sonicTrackBinding, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id,v.start_seconds,v.end_seconds FROM catalog_assets a JOIN catalog_asset_links link ON link.asset_id=a.id JOIN catalog_entities e ON e.id=link.entity_id JOIN catalog_libraries l ON l.id=e.library_id LEFT JOIN music_virtual_tracks v ON v.asset_id=a.token AND v.entity_id=e.id WHERE a.token=? AND e.kind=7 AND e.retired=0 AND l.library_id=? AND l.retired=0 ORDER BY e.id LIMIT 4097`, b.AssetID, b.Source.LibraryID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	tracks := []sonicTrackBinding{}
	parts := []string{"sonic-track-ranges-1"}
	for rows.Next() {
		var track sonicTrackBinding
		var start, end sql.NullFloat64
		if err = rows.Scan(&track.EntityID, &start, &end); err != nil {
			return nil, "", err
		}
		track.EndUS = b.DurationUS
		if start.Valid != end.Valid {
			return nil, "", ErrConflict
		}
		if start.Valid {
			track.StartUS, err = sonicRangeUS(start.Float64)
			if err != nil {
				return nil, "", err
			}
			track.EndUS, err = sonicRangeUS(end.Float64)
			if err != nil {
				return nil, "", err
			}
		}
		if track.StartUS < 0 || track.EndUS <= track.StartUS || track.EndUS > b.DurationUS {
			return nil, "", ErrConflict
		}
		tracks = append(tracks, track)
		parts = append(parts, strconv.FormatInt(track.EntityID, 10), strconv.FormatInt(track.StartUS, 10), strconv.FormatInt(track.EndUS, 10))
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if len(tracks) > 4096 {
		return nil, "", ErrBudget
	}
	return tracks, token(parts...), nil
}

// CUE positions are stored seconds on their original 75 Hz clock, and are not
// necessarily integer microseconds. These are the actual analysis recipe's
// nearest-microsecond bounds, never replacement playback trim/frame facts.
func sonicRangeUS(seconds float64) (int64, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64((1<<53)-1)/1e6 {
		return 0, ErrInput
	}
	return int64(math.Round(seconds * 1e6)), nil
}

func (s *Service) stageAlgorithm(b binding, stage string) string {
	if stage != catalog.SonicOperation {
		return Algorithm
	}
	model := "unavailable"
	if runtime := s.sonicRuntime(); runtime != nil {
		id := runtime.Identity()
		model = token(id.ID, id.Version, id.SHA256, runtime.HelperDigest())
	}
	return Algorithm + ":sonic:" + token(model, b.SonicBinding)
}
func (s *Service) stageWorkAlgorithm(b binding, stage string) string {
	if stage != catalog.SonicOperation {
		return b.WorkAlgorithm
	}
	return b.WorkAlgorithm + ":" + token(s.stageAlgorithm(b, stage))
}

func (s *Service) SonicCanAttempt() bool { return s != nil && s.sonicRuntime().CanAttempt() }

// SonicRuntimeToken names the installed sonic model and helper ("" when none
// is installed), so a caller can tell whether SonicCurrentTx answers taken
// earlier still apply.
func (s *Service) SonicRuntimeToken() string {
	if s == nil {
		return ""
	}
	runtime := s.sonicRuntime()
	if runtime == nil {
		return ""
	}
	id := runtime.Identity()
	return token(id.ID, id.Version, id.SHA256, runtime.HelperDigest())
}

// SonicCurrentTx is used by the bounded inventory-enqueue page. It detects a
// model/helper/range change without invalidating unrelated analysis. It only
// reads, so it can run in a read-only snapshot outside the writer.
func (s *Service) SonicCurrentTx(ctx context.Context, tx *sql.Tx, request Request) (bool, error) {
	if !s.SonicCanAttempt() {
		return true, nil
	}
	b, err := s.readBindingTx(ctx, tx, request, false)
	if err != nil {
		return false, err
	}
	var complete bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM analysis_heads h JOIN analysis_results r ON r.id=h.result_id WHERE h.object_id=? AND h.source_revision=? AND h.stage='sonic' AND r.algorithm=? AND r.root_incarnation=? AND r.configuration_generation=? AND r.retired_ms=0 AND (?='' OR r.evidence=?))`, b.ObjectID, b.SourceRevision, s.stageAlgorithm(b, catalog.SonicOperation), b.Source.Incarnation, b.Source.Generation, b.ProbeEvidence, b.ProbeEvidence).Scan(&complete)
	return complete, err
}

func (s *Service) produceSonic(ctx context.Context, b binding, decode Decode, key string, physical *os.File) (produced, error) {
	out := produced{Artifacts: []artifactOutput{}, Markers: []Marker{}}
	if len(b.SonicTracks) == 0 || b.AudioCodec == "" {
		out.Summary = map[string]any{"status": "not_applicable", "reason": "no_music_track"}
		return out, nil
	}
	runtime := s.sonicRuntime()
	if runtime == nil {
		return out, sonic.ErrUnavailable
	}
	if int64(len(b.SonicTracks))*(sonic.Dimensions+sonic.TagCount)*4 > s.stageLimit(ctx) {
		return out, ErrBudget
	}
	measured := map[[2]int64]sonic.Analysis{}
	for _, track := range b.SonicTracks {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		rangeKey := [2]int64{track.StartUS, track.EndUS}
		analysis, found := measured[rangeKey]
		if !found {
			spec := decoder.AnalysisSpec{Kind: "sonic_pcm", MaxDurationUS: b.DurationUS, StartUS: track.StartUS, RegionUS: track.EndUS - track.StartUS}
			// The allowance is a bound, never a published duration/rate fact. It
			// covers the source's microsecond rounding and the resampler's flush.
			maxSamples := int64(math.Ceil(float64(spec.RegionUS)*sonic.SampleRate/1e6)) + sonic.SampleRate/10
			var err error
			analysis, err = runtime.Run(ctx, key+":"+strconv.FormatInt(track.EntityID, 10), physical, func(yield func([]float32) error) error {
				return decode(ctx, key+":pcm:"+strconv.FormatInt(track.EntityID, 10), spec, func(input io.Reader) error {
					_, err := sonic.LogMel(input, maxSamples, yield)
					return err
				})
			})
			if err != nil {
				return out, err
			}
			measured[rangeKey] = analysis
		}
		out.Sonic = append(out.Sonic, sonicItemAnalysis{Binding: track, Analysis: analysis})
	}
	model := runtime.Identity()
	out.Summary = map[string]any{"status": "complete", "tracks": len(out.Sonic), "modelId": model.ID, "modelVersion": model.Version, "modelSHA256": model.SHA256, "serverLocal": true}
	return out, nil
}

func persistSonic(ctx context.Context, tx *sql.Tx, result string, items []sonicItemAnalysis) error {
	for _, item := range items {
		analysis, track := item.Analysis, item.Binding
		if analysis.Model.ID != sonic.ModelID || analysis.Model.Version != sonic.ModelVersion || analysis.Model.SHA256 != sonic.ModelSHA256 || analysis.Patches < 1 || track.EntityID < 1 || track.StartUS < 0 || track.EndUS <= track.StartUS {
			return ErrUnsupported
		}
		vector, err := analysis.Embedding.Bytes()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO music_sonic_embeddings(result_id,entity_id,source_start_us,source_end_us,model_id,model_version,model_sha256,dimensions,vector) VALUES(?,?,?,?,?,?,?,?,?)`, result, track.EntityID, track.StartUS, track.EndUS, analysis.Model.ID, analysis.Model.Version, analysis.Model.SHA256, sonic.Dimensions, vector); err != nil {
			return err
		}
		for index, score := range analysis.Tags {
			if math.IsNaN(float64(score)) || math.IsInf(float64(score), 0) || score < 0 || score > 1 {
				return sonic.ErrEmbedding
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO music_sonic_tags(result_id,entity_id,tag_index,score) VALUES(?,?,?,?)`, result, track.EntityID, index, score); err != nil {
				return err
			}
		}
	}
	return nil
}

// QueueSonicRefresh only marks existing music libraries in the existing durable
// inventory queue on startup/model change. Work remains admitted by the same
// analysis work window; this function opens/decodes no recordings.
func (s *Service) QueueSonicRefresh(ctx context.Context) error {
	if !s.SonicCanAttempt() {
		return nil
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return catalog.QueueSonicRefreshTx(ctx, tx)
	})
}
