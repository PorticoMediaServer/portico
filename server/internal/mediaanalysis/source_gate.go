package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// sourceStageAllowedTx answers whether the saved remote source that contains
// this library folder explicitly permits advanced analysis. This applies before
// every stage, including ranged/Sonic/segment work; a preset or a missing remote
// page cannot grant consent. Local folders retain their library policy.
func sourceStageAllowedTx(ctx context.Context, tx *sql.Tx, librarySource, operation string) (bool, error) {
	consent, err := catalog.SourceAnalysisConsentTx(ctx, tx, librarySource)
	if err != nil || !consent.Allowed {
		return false, err
	}
	switch operation {
	case "trickplay", "chapter_images", "waveform", "loudness", "checksum":
	default:
		return true, nil
	}
	var seekPreviews, chapterImages, waveforms, loudness, checksums bool
	err = tx.QueryRowContext(ctx, `SELECT st.seek_previews,st.chapter_images,st.waveforms,st.loudness,st.checksums FROM library_sources s JOIN remote_source_settings st ON st.source_id=s.remote_source_id WHERE s.id=?`, librarySource).
		Scan(&seekPreviews, &chapterImages, &waveforms, &loudness, &checksums)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	switch operation {
	case "trickplay":
		return seekPreviews, nil
	case "chapter_images":
		return chapterImages, nil
	case "waveform":
		return waveforms, nil
	case "loudness":
		return loudness, nil
	case "checksum":
		return checksums, nil
	}
	return true, nil
}
