package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SourceAnalysisConsent is a same-snapshot source fact, not permission supplied
// by an engine/caller. Actual work retains its original source and this witness
// alongside the operation, actor, schedule, range and resource admission.
type SourceAnalysisConsent struct {
	LibrarySourceID, SourceID, ConfigurationGeneration string
	ProfileRevision, LibraryGeneration                 int64
	Incarnation, RootIdentity                          string
	Remote, Allowed                                    bool
}

// SourceAnalysisConsentTx uses the exact normalized folder-to-source binding.
// A missing remote binding/profile is never treated as a local allowed folder.
// Reading this fact does not open the provider or authorize any byte work.
func SourceAnalysisConsentTx(ctx context.Context, tx *sql.Tx, librarySource string) (SourceAnalysisConsent, error) {
	out := SourceAnalysisConsent{LibrarySourceID: librarySource}
	if ctx == nil || tx == nil || librarySource == "" {
		return out, storage.ErrRemoteConfig
	}
	var kind string
	err := tx.QueryRowContext(ctx, `SELECT kind,COALESCE(remote_source_id,''),generation,incarnation,root_identity FROM library_sources WHERE id=? AND enabled=1`, librarySource).Scan(&kind, &out.SourceID, &out.LibraryGeneration, &out.Incarnation, &out.RootIdentity)
	if err != nil {
		return out, err
	}
	if kind == "local" {
		out.Allowed = true
		return out, nil
	}
	if kind != "remote" || out.SourceID == "" {
		return out, storage.ErrRemoteConfig
	}
	out.Remote = true
	var allowed int
	err = tx.QueryRowContext(ctx, `SELECT CASE WHEN r.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END,COALESCE(p.revision,0),COALESCE(p.allow_advanced_media_analysis,0)
 FROM remote_sources r LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id
 LEFT JOIN remote_source_read_profiles p ON p.source_id=r.id
 WHERE r.id=? AND r.removed=0`, out.SourceID).Scan(&out.ConfigurationGeneration, &out.ProfileRevision, &allowed)
	if err != nil || out.ConfigurationGeneration == "" || out.ProfileRevision < 0 || (allowed != 0 && allowed != 1) {
		return out, storage.ErrRemoteOffline
	}
	out.Allowed = out.ProfileRevision > 0 && allowed == 1
	return out, nil
}

// CheckSourceAnalysisConsentTx refuses a changed or disabled original source.
// It never adopts the current witness in place of the captured one.
func CheckSourceAnalysisConsentTx(ctx context.Context, tx *sql.Tx, original SourceAnalysisConsent) error {
	current, err := SourceAnalysisConsentTx(ctx, tx, original.LibrarySourceID)
	if err != nil {
		return err
	}
	if !original.Allowed || !current.Allowed {
		return storage.ErrRemoteAnalysisDisabled
	}
	if current != original {
		return storage.ErrRemoteChanged
	}
	return nil
}
