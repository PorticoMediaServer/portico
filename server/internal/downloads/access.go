package downloads

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// LibraryAccess checks current membership in the caller's authority snapshot.
// The router installs the same policy used by playback and catalogue reads.
type LibraryAccess func(context.Context, *sql.Tx, identity.Principal, string) error

func (s *Service) SetLibraryAccess(check LibraryAccess) {
	s.accessMu.Lock()
	s.libraryAccess = check
	s.accessMu.Unlock()
}

func (s *Service) checkLibrary(ctx context.Context, tx *sql.Tx, p identity.Principal, library string) error {
	s.accessMu.RLock()
	check := s.libraryAccess
	s.accessMu.RUnlock()
	if check == nil {
		return nil
	} // Isolated compositions can supply their own admission guard.
	err := check(ctx, tx, p, library)
	if errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrNotVisible) {
		return identity.ErrContentRestricted
	}
	return err
}

func (s *Service) visibleItem(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) error {
	var library string
	err := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&library)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrContentRestricted
	}
	if err != nil {
		return err
	}
	if err = s.checkLibrary(ctx, tx, p, library); err != nil {
		return err
	}
	return contentaccess.VisibleKnownItemTx(ctx, tx, p, item)
}

// A removed title may still describe a copy the viewer holds. Its old library
// remains an access boundary until the library itself is removed.
func (s *Service) visiblePreparation(ctx context.Context, tx *sql.Tx, p identity.Principal, r row) error {
	if r.Title.OnServer {
		return s.visibleItem(ctx, tx, p, r.ItemID)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`, r.LibraryID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return s.checkLibrary(ctx, tx, p, r.LibraryID)
	}
	return nil
}
