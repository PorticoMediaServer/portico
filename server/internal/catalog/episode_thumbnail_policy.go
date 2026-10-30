package catalog

import (
	"context"
	"database/sql"
)

const EpisodeThumbnailOperation = "episode_thumbnail"

// EpisodeThumbnailPolicy is the single runtime automatic-thumbnail policy.
// Its revision is the existing library_scan_policies revision, shared with
// operation/depth updates and the administration aggregate CAS.
type EpisodeThumbnailPolicy struct {
	Mode     string
	Revision int64
}

func DefaultEpisodeThumbnailMode(kind string) string {
	if kind == "tv" || kind == "anime" {
		return "provider_then_made"
	}
	return "off"
}
func ValidEpisodeThumbnailMode(mode string) bool {
	return mode == "provider_then_made" || mode == "provider" || mode == "off"
}
func EpisodeThumbnailPolicyTx(ctx context.Context, tx *sql.Tx, library string) (EpisodeThumbnailPolicy, error) {
	var out EpisodeThumbnailPolicy
	err := tx.QueryRowContext(ctx, `SELECT episode_thumbnail_mode,revision FROM library_scan_policies WHERE library_id=?`, library).Scan(&out.Mode, &out.Revision)
	if err == nil && (!ValidEpisodeThumbnailMode(out.Mode) || out.Revision < 1) {
		err = ErrAdminQuery
	}
	return out, err
}

// ApplyEpisodeThumbnailModeTx is the sole mode writer. The administration owner
// supplies its authorized writer transaction and current aggregate CAS first;
// this additional policy CAS closes competing scan/profile/mode writes. Other
// scan-policy writers update their own columns and preserve this mode.
func ApplyEpisodeThumbnailModeTx(ctx context.Context, tx *sql.Tx, library, mode string, expectedPolicyRevision int64) error {
	if !ValidEpisodeThumbnailMode(mode) || expectedPolicyRevision < 1 {
		return ErrAdminQuery
	}
	old, err := EpisodeThumbnailPolicyTx(ctx, tx, library)
	if err != nil {
		return err
	}
	if old.Revision != expectedPolicyRevision {
		return ErrLibraryConfigurationConflict
	}
	var managed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_private_libraries WHERE library_id=?)`, library).Scan(&managed); err != nil {
		return err
	}
	if managed {
		return ErrAdminQuery
	}
	if old.Mode == mode {
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE library_scan_policies SET episode_thumbnail_mode=?,revision=revision+1 WHERE library_id=? AND revision=?`, mode, library, expectedPolicyRevision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLibraryConfigurationConflict
	}
	return nil
}
