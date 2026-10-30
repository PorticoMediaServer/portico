package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// ReadLimits are ceilings, not a resource reservation. Zero bandwidth means no
// chosen cap; the admission/enforcement owner must still apply every lower cap.
type ReadLimits struct {
	Connections, RequestsPerSecond int
	BytesPerSecond                 int64
}

// SourceReadProfile is the normalized read-authority part of the source's
// existing Reading settings. Episode mode belongs only to the library owner.
type SourceReadProfile struct {
	SourceID       string
	Revision       int64
	Saved          bool
	ScanDepth      string
	WholeFileReads bool
	// Explicit source consent, independent of scan depth and legacy whole-file
	// flags. An absent/legacy profile never supplies this permission.
	AllowAdvancedMediaAnalysis bool
	Limits                     ReadLimits
}

func validSourceReadProfile(p SourceReadProfile) bool {
	switch p.ScanDepth {
	case "file_list_only", "basic", "complete":
	default:
		return false
	}
	return p.Limits.Connections >= 1 && p.Limits.Connections <= 32 && p.Limits.RequestsPerSecond >= 1 && p.Limits.RequestsPerSecond <= 50 && (p.Limits.BytesPerSecond == 0 || p.Limits.BytesPerSecond >= 1_000_000 && p.Limits.BytesPerSecond <= 1_000_000_000 && p.Limits.BytesPerSecond%1_000_000 == 0)
}

// SourceReadPreset maps only the approved read-authority fields. It does not
// select/apply a preset, change scheduling/cache settings or grant decode work.
func SourceReadPreset(name string) (SourceReadProfile, error) {
	p := SourceReadProfile{ScanDepth: "file_list_only", Limits: ReadLimits{Connections: 2, RequestsPerSecond: 2}}
	switch name {
	case "list":
	case "ask":
		p.ScanDepth = "basic"
	case "once":
		p.ScanDepth = "basic"
		p.Limits = ReadLimits{Connections: 4, RequestsPerSecond: 4}
	case "everything":
		p.ScanDepth = "complete"
		p.Limits = ReadLimits{Connections: 8, RequestsPerSecond: 10}
	default:
		return SourceReadProfile{}, storage.ErrRemoteConfig
	}
	return p, nil
}

// SourceReadProfileTx treats an absent saved profile exactly as List under
// ruling section 12. It never silently assigns the wizard's Once selection.
func SourceReadProfileTx(ctx context.Context, tx *sql.Tx, source string) (SourceReadProfile, error) {
	if tx == nil || source == "" {
		return SourceReadProfile{}, storage.ErrRemoteConfig
	}
	if _, err := observationGenerationTx(ctx, tx, source); err != nil {
		return SourceReadProfile{}, err
	}
	return storedSourceReadProfileTx(ctx, tx, source)
}

// Owner configuration projection may retain stored facts when effective mount
// authority is unavailable. Call only after resolving the live source row.
// This does not admit reads: those must use SourceReadProfileTx above.
func storedSourceReadProfileTx(ctx context.Context, tx *sql.Tx, source string) (SourceReadProfile, error) {
	p := SourceReadProfile{SourceID: source, Saved: true}
	err := tx.QueryRowContext(ctx, `SELECT revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second,allow_advanced_media_analysis FROM remote_source_read_profiles WHERE source_id=?`, source).Scan(&p.Revision, &p.ScanDepth, &p.WholeFileReads, &p.Limits.Connections, &p.Limits.RequestsPerSecond, &p.Limits.BytesPerSecond, &p.AllowAdvancedMediaAnalysis)
	if errors.Is(err, sql.ErrNoRows) {
		p, _ = SourceReadPreset("list")
		p.SourceID = source
		return p, nil
	}
	if err != nil {
		return SourceReadProfile{}, err
	}
	if p.Revision < 1 || !validSourceReadProfile(p) {
		return SourceReadProfile{}, storage.ErrRemoteConfig
	}
	return p, nil
}

// currentLocalOwnerTx uses the checked current-token family seam, including
// installed membership policy. A role claim or Hosted token is insufficient.
func currentLocalOwnerTx(ctx context.Context, tx *sql.Tx, service *identity.Service, captured identity.Principal) error {
	if service == nil || tx == nil || captured.Hash == "" {
		return identity.ErrUnauthorized
	}
	current, err := service.PrincipalByDigestTx(ctx, tx, captured.Hash)
	if err != nil {
		return err
	}
	if current.Viewer != captured.Viewer || current.Epoch != captured.Epoch || current.Grant != captured.Grant {
		return identity.ErrUnauthorized
	}
	if current.Authority != "local" || current.Role != "owner" || current.Grant != "" {
		return identity.ErrForbidden
	}
	return nil
}

type SourceReadProfileChange struct {
	SourceID, Generation string
	ExpectedRevision     int64
	Profile              SourceReadProfile
	// Only a separately submitted explicit action changes consent. Presets and
	// older callers omit it and preserve the original current opt-in.
	AdvancedMediaAnalysis *bool
}

type SourceReadProfileResult struct {
	Profile           SourceReadProfile
	AffectedLibraries []string
}

// ApplySourceReadProfileTx is the sole read-profile writer. The caller owns the
// writer transaction, command receipt, commit and post-commit cancellation. It
// must roll back on any error. Zero expected revision only creates the first
// saved profile; profile revisions never reset on A→B→A or identity edits.
func (s *Service) ApplySourceReadProfileTx(ctx context.Context, tx *sql.Tx, identities *identity.Service, owner identity.Principal, change SourceReadProfileChange) (SourceReadProfileResult, error) {
	if s == nil || tx == nil || change.SourceID == "" || change.Generation == "" || change.ExpectedRevision < 0 || change.Profile.SourceID != "" && change.Profile.SourceID != change.SourceID || !validSourceReadProfile(change.Profile) {
		return SourceReadProfileResult{}, storage.ErrRemoteConfig
	}
	consentPresent := change.AdvancedMediaAnalysis != nil
	consent := false
	if consentPresent {
		consent = *change.AdvancedMediaAnalysis
	}
	if err := currentLocalOwnerTx(ctx, tx, identities, owner); err != nil {
		return SourceReadProfileResult{}, err
	}
	generation, err := observationGenerationTx(ctx, tx, change.SourceID)
	if err != nil {
		return SourceReadProfileResult{}, err
	}
	if generation != change.Generation {
		return SourceReadProfileResult{}, storage.ErrRemoteChanged
	}
	old, err := SourceReadProfileTx(ctx, tx, change.SourceID)
	if err != nil {
		return SourceReadProfileResult{}, err
	}
	if old.Revision != change.ExpectedRevision {
		return SourceReadProfileResult{}, storage.ErrRemoteChanged
	}
	p := change.Profile
	p.SourceID, p.Saved, p.Revision = change.SourceID, true, old.Revision
	p.AllowAdvancedMediaAnalysis = old.AllowAdvancedMediaAnalysis
	if consentPresent {
		p.AllowAdvancedMediaAnalysis = consent
	}
	if old.Saved && old == p {
		return SourceReadProfileResult{Profile: old}, nil
	}
	if old.Revision == 1<<63-1 {
		return SourceReadProfileResult{}, storage.ErrRemoteLimit
	}
	p.Revision++
	if old.Saved {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE remote_source_read_profiles SET revision=?,scan_depth=?,whole_file_reads=?,connections=?,requests_per_second=?,bytes_per_second=?,allow_advanced_media_analysis=? WHERE source_id=? AND revision=?`, p.Revision, p.ScanDepth, p.WholeFileReads, p.Limits.Connections, p.Limits.RequestsPerSecond, p.Limits.BytesPerSecond, p.AllowAdvancedMediaAnalysis, p.SourceID, old.Revision)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				err = storage.ErrRemoteChanged
			}
		}
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second,allow_advanced_media_analysis) VALUES(?,?,?,?,?,?,?,?)`, p.SourceID, p.Revision, p.ScanDepth, p.WholeFileReads, p.Limits.Connections, p.Limits.RequestsPerSecond, p.Limits.BytesPerSecond, p.AllowAdvancedMediaAnalysis)
	}
	if err != nil {
		return SourceReadProfileResult{}, err
	}
	var root string
	if err = tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, p.SourceID).Scan(&root); err != nil {
		return SourceReadProfileResult{}, err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return SourceReadProfileResult{}, storage.ErrRemoteConfig
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT library_id,root FROM library_sources WHERE kind='remote' ORDER BY library_id,root`)
	if err != nil {
		return SourceReadProfileResult{}, err
	}
	var libraries []string
	seen := map[string]bool{}
	for rows.Next() {
		var library, candidate string
		if err = rows.Scan(&library, &candidate); err != nil {
			break
		}
		relative, e := filepath.Rel(root, candidate)
		if e == nil && (relative == "." || filepath.IsLocal(relative)) && !seen[library] {
			seen[library] = true
			libraries = append(libraries, library)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return SourceReadProfileResult{}, err
	}
	for _, library := range libraries {
		result, e := tx.ExecContext(ctx, `UPDATE library_scan_policies SET revision=revision+1 WHERE library_id=? AND revision<9223372036854775807`, library)
		if e != nil {
			return SourceReadProfileResult{}, e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return SourceReadProfileResult{}, e
		}
		if count != 1 {
			return SourceReadProfileResult{}, storage.ErrRemoteChanged
		}
	}
	return SourceReadProfileResult{Profile: p, AffectedLibraries: libraries}, nil
}
