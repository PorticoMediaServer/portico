package metadata

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type LocalAudioPolicyChange struct {
	LibraryID        string `json:"libraryId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	LocalMode        string `json:"localMode"`
}

// Audiobooks have no remote provider job. Their library-local policy still uses
// a current owner transaction and library identity + revision CAS.
func (s *Service) UpdateLocalBookPolicy(item string, m LocalAudioPolicyChange, authorize func(*sql.Tx) error) error {
	if m.LibraryID == "" || m.ExpectedRevision < 1 || (m.LocalMode != "off" && m.LocalMode != "prefer" && m.LocalMode != "supplement") {
		return requestinput.New("invalid local audio policy")
	}
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	var library string
	if e = tx.QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_book_files bf ON bf.entity_id=e.id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id AND l.kind='audiobook' WHERE e.public_id=pid_blob(?)`, item).Scan(&library); e != nil {
		return e
	}
	if library != m.LibraryID {
		return ErrMBConflict
	}
	result, e := tx.Exec(`UPDATE audio_metadata_policies SET local_mode=?,revision=revision+1 WHERE library_id=? AND revision=?`, m.LocalMode, library, m.ExpectedRevision)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrMBConflict
	}
	if _, e = tx.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`, library); e != nil {
		return e
	}
	return gated.Commit()
}
