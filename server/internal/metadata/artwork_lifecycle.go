package metadata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Artwork objects are content-addressed files ("<digest>.img") whose lifetime
// is decided by the database, with no process-wide lock (BE-SRV-14). Two rules
// replace the old artMu critical section:
//
//  1. A writer that references an object it installed checks the file again
//     after its transaction commits and reinstalls it from the bytes it still
//     holds if the file is gone (ensureArtworkFiles).
//  2. A remover never deletes a file directly. It renames the file to a unique
//     tomb, then asks the database whether the object is dead; only then is the
//     tomb removed, otherwise the file is put back (retireArtworkFile).
//
// Every interleaving is safe: either the remover's database check runs after
// the writer's commit (it sees the object and restores the file), or it runs
// before, in which case the writer's post-commit check runs after the rename
// and reinstalls the file. Identical digests mean identical bytes, so a
// restore and a reinstall can race harmlessly. Foreign keys stop a reference
// to an object row that the remover has already deleted.

const artworkTombMarker = ".img.tomb-"

// ensureArtworkFiles is rule 1: after the referencing transaction commits,
// reinstall any object file a concurrent retirement removed.
func (s *Service) ensureArtworkFiles(objects ...artworkInstalled) error {
	for _, o := range objects {
		if o.raw != nil {
			if _, err := os.Stat(filepath.Join(s.cacheRoot, o.digest+".img")); errors.Is(err, os.ErrNotExist) {
				if _, err = s.installArtworkFile(o.raw, o.width, o.height); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// retireArtworkFile is rule 2. dead runs after the file has been moved aside
// and reports whether the object is really gone (it may delete the row). When
// it is not, the file is restored.
func (s *Service) retireArtworkFile(ctx context.Context, digest string, dead func(context.Context) (bool, error)) (bool, error) {
	if !artDigest(digest) {
		return false, nil
	}
	target := filepath.Join(s.cacheRoot, digest+".img")
	tomb := filepath.Join(s.cacheRoot, digest+artworkTombMarker+identity.Token())
	moved := true
	if err := os.Rename(target, tomb); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			// An open file that cannot be renamed on this platform: try later.
			return false, nil
		}
		moved = false
	}
	gone, err := dead(ctx)
	if err != nil || !gone {
		if moved {
			_ = os.Rename(tomb, target)
		}
		return false, err
	}
	if moved {
		_ = os.Remove(tomb)
	}
	return true, nil
}

// settleArtworkTomb handles a tomb left by a crash between rename and decision:
// restore it when the object is still known, otherwise remove it.
func (s *Service) settleArtworkTomb(ctx context.Context, name string) error {
	digest, _, ok := strings.Cut(name, artworkTombMarker)
	if !ok || !artDigest(digest) {
		return nil
	}
	var known bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_blobs WHERE digest=?)`, digest).Scan(&known); err != nil {
		return err
	}
	tomb := filepath.Join(s.cacheRoot, name)
	if known {
		return os.Rename(tomb, filepath.Join(s.cacheRoot, digest+".img"))
	}
	return os.Remove(tomb)
}
