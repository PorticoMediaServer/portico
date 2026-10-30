package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// sourceIntroModeTx reads the saved per-source intro mapping for one object's
// library folder: "off", "inherit", "sound" or "picture". An empty string means
// the object has no remote source association.
func sourceIntroModeTx(ctx context.Context, tx *sql.Tx, object string) (string, error) {
	var mode string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(st.intros_mode,'inherit') FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id LEFT JOIN remote_source_settings st ON st.source_id=src.remote_source_id WHERE o.id=?`, object).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return mode, err
}

// effectiveSegmentModeTx is the mode that actually applies to this file now.
// The library's effective mode (which already folds in the server choice) comes
// first: while it is off, no source choice can grant detection work. Only then
// does the source's own choice pick standard or thorough under it.
func effectiveSegmentModeTx(ctx context.Context, tx *sql.Tx, object, library string) (string, error) {
	libraries, _, err := segmentLibrariesTx(ctx, tx)
	if err != nil {
		return "", err
	}
	libraryMode := SegmentModeOff
	for _, l := range libraries {
		if l.ID == library {
			libraryMode = l.Mode
			break
		}
	}
	if libraryMode == SegmentModeOff {
		return SegmentModeOff, nil
	}
	mode, err := sourceIntroModeTx(ctx, tx, object)
	if err != nil {
		return "", err
	}
	switch mode {
	case "off":
		return SegmentModeOff, nil
	case "sound":
		return SegmentModeStandard, nil
	case "picture":
		return SegmentModeThorough, nil
	}
	return libraryMode, nil
}

// effectiveMode reads the mode that applies to one file right now.
func (s *Service) effectiveMode(ctx context.Context, object, library string) (string, error) {
	tx, closeRead, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer closeRead()
	return effectiveSegmentModeTx(ctx, tx, object, library)
}

// segmentSourceModesTx is the digest input for every source that does not
// simply follow its library, so changing one resets the catch-up pass and
// re-considers queued files under the new choice.
func segmentSourceModesTx(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_id,intros_mode FROM remote_source_settings WHERE intros_mode<>'inherit' ORDER BY source_id LIMIT 500`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var id, mode string
		if err = rows.Scan(&id, &mode); err != nil {
			return "", err
		}
		parts = append(parts, "mode:"+id+"="+mode)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	rows.Close()
	// Consent changes must wake and re-consider files that were excluded while
	// off; an inventory change or the daily pass must not be required first.
	profiles, err := tx.QueryContext(ctx, `SELECT p.source_id,p.revision,p.allow_advanced_media_analysis FROM remote_source_read_profiles p WHERE EXISTS(SELECT 1 FROM library_sources s WHERE s.remote_source_id=p.source_id AND s.enabled=1) ORDER BY p.source_id LIMIT 500`)
	if err != nil {
		return "", err
	}
	defer profiles.Close()
	for profiles.Next() {
		var source string
		var revision int64
		var allowed bool
		if err = profiles.Scan(&source, &revision, &allowed); err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("consent:%s=%d:%t", source, revision, allowed))
	}
	sort.Strings(parts)
	return strings.Join(parts, ","), profiles.Err()
}
