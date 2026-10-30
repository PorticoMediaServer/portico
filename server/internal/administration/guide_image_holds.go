package administration

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

const guideImageTombMarker = ".tomb-"

// The final decision shares writer ordering with backup hold admission. If a
// hold began after the initial mark, the original path is restored. A backup
// starting after the final no-reference decision cannot name this tomb; a new
// publication must install its canonical immutable bytes before its DB row.
func (s *Service) guideImageMustKeep(ctx context.Context, digest string) (bool, error) {
	plan := dbwork.WritePlan{Name: "guide-image-collection-fence", Rows: 0, Pages: 0, VMOps: 100_000, Why: "one singleton hold probe and two indexed rendition-digest probes; no row writes"}
	w, err := dbwork.Begin(dbwork.WithPlan(ctx, plan), s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return true, err
	}
	defer w.Rollback()
	var keep bool
	err = w.Tx().QueryRowContext(w.Context(), `SELECT EXISTS(SELECT 1 FROM backup_blob_holds) OR EXISTS(SELECT 1 FROM live_programme_images WHERE small_digest=?) OR EXISTS(SELECT 1 FROM live_programme_images WHERE large_digest=?)`, digest, digest).Scan(&keep)
	if err != nil {
		return true, err
	}
	if err = w.Commit(); err != nil {
		return true, err
	}
	return keep, nil
}

func (s *Service) retireGuideImage(ctx context.Context, root, name, digest string, afterMark func() error) error {
	keep, err := s.guideImageMustKeep(ctx, digest)
	if err != nil || keep {
		return err
	}
	tomb := name + guideImageTombMarker + identity.Token()
	if err = os.Rename(filepath.Join(root, name), filepath.Join(root, tomb)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if afterMark != nil {
		if err = afterMark(); err != nil {
			restoreErr := restoreGuideImageTomb(root, tomb, name)
			return errors.Join(err, restoreErr)
		}
	}
	return s.settleGuideImageTomb(ctx, root, tomb, name, digest)
}

func (s *Service) settleGuideImageTomb(ctx context.Context, root, tomb, name, digest string) error {
	keep, err := s.guideImageMustKeep(ctx, digest)
	if err != nil || keep {
		return errors.Join(err, restoreGuideImageTomb(root, tomb, name))
	}
	if err = os.Remove(filepath.Join(root, tomb)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func restoreGuideImageTomb(root, tomb, name string) error {
	from, to := filepath.Join(root, tomb), filepath.Join(root, name)
	// Never overwrite a concurrent publisher's canonical inode. Both names are
	// digest-addressed; publication always revalidates/install its own bytes.
	if err := os.Link(from, to); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := os.Remove(from); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
