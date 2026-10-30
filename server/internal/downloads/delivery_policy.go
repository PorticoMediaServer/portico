package downloads

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
)

const ReasonDownloadCeiling = "exceeds_download_policy"

func (s *Service) deliveryConfig() playback.DeliveryConfiguration {
	if s.deliveryConfiguration != nil {
		return s.deliveryConfiguration().Normalized()
	}
	return playback.DefaultDeliveryConfiguration()
}

func (s *Service) resolveQuality(ctx context.Context, tx *sql.Tx, device playback.CapabilityDocument, requested string, source source) string {
	if !audioKind(source.Kind) {
		requested = s.deliveryConfig().ClampDownloadQuality(requested)
	}
	return resolvedQuality(ctx, tx, device, requested, source)
}

func (s *Service) cappedQualities(choices []QualityChoice) []QualityChoice {
	out := make([]QualityChoice, 0, len(choices))
	for _, choice := range choices {
		// Original also serves audio and sources below a cap. Per-file admission
		// resolves it against actual source facts; a generic list cannot do that.
		if choice.Quality == QualityOriginal || choice.Quality == "automatic" || s.deliveryConfig().DownloadQualityAllowed(choice.Quality) {
			out = append(out, choice)
		}
	}
	return out
}

func (s *Service) withinCeiling(quality string, source source) bool {
	if audioKind(source.Kind) || readingKind(source.Kind) {
		return true
	}
	cfg := s.deliveryConfig()
	if cfg.DownloadQualityAllowed(quality) {
		return true
	}
	cap, capped := playback.LadderPreset(cfg.DownloadMaxQuality)
	return !capped || cap.SameAsSource(source.Height, source.VideoBitrateBPS)
}

func (s *Service) preparationReason(ctx context.Context, tx *sql.Tx, quality, recipe string, found source) string {
	if reason := s.conversionPolicyReason(ctx, tx, quality, recipe, found); reason != "" {
		return reason
	}
	return qualityReason(ctx, tx, quality, recipe, found)
}

func (s *Service) conversionPolicyReason(ctx context.Context, tx *sql.Tx, quality, recipe string, found source) string {
	if !s.withinCeiling(quality, found) {
		return ReasonDownloadCeiling
	}
	if quality == QualityOriginal && recipe == "" || sameAsSource(quality, found) {
		return ""
	}
	// An existing finished file needs no server conversion permission.
	if versions, err := selectionDownloadVersions(ctx, tx, found, recipe); err == nil && selectableVersionForSource(versions, recipe, found.Asset) != nil {
		return ""
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&enabled); err != nil {
		return playback.RungReasonTranscodeOff
	}
	cfg := s.deliveryConfig()
	if audioKind(found.Kind) {
		enabled = cfg.AudioConversionAllowed(enabled)
	} else {
		enabled = cfg.VideoConversionAllowed(enabled)
	}
	if !enabled {
		return playback.RungReasonTranscodeOff
	}
	if !audioKind(found.Kind) && cfg.OutputFormat == "h264" && preparedmedia.Rich(recipe) {
		return ReasonNotOptimized
	}
	return ""
}
