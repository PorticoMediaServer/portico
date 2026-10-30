package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Closing an ordinary descriptor on a live SQLite inode releases this
// process's POSIX locks on it (dbwork/open.go). Source inspection and restore
// must identify those inodes with stat, before any header read or hash opens
// them. Keep the main identity from construction; refresh sidecars because a
// replacement writer can recreate them. No source descriptor is opened here.
type sourceFileGuard struct {
	db   *sql.DB
	path string
	main os.FileInfo
	err  error
}

func newSourceFileGuard(db *sql.DB) sourceFileGuard {
	if db == nil {
		return sourceFileGuard{}
	}
	path := dbwork.DatabaseFile(context.Background(), db)
	info, err := os.Stat(path)
	if path == "" || err != nil {
		return sourceFileGuard{db: db, err: ErrSourceInvalid}
	}
	return sourceFileGuard{db: db, path: path, main: info}
}

func (g sourceFileGuard) files() ([]os.FileInfo, error) {
	// Recovery can reuse the source-selection service after closing its live
	// handle and applying a staged database. Only a fully drained closed handle
	// holds no locks: Close itself can leave borrowed connections alive.
	if g.db != nil && closedDatabase(g.db) && dbwork.ConnectionsDrained(g.db) {
		return nil, nil
	}
	if g.err != nil || g.path == "" {
		return nil, g.err
	}
	main, err := os.Stat(g.path)
	if err != nil || !os.SameFile(main, g.main) {
		return nil, ErrSourceInvalid
	}
	files := []os.FileInfo{main}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		info, err := os.Stat(g.path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, ErrSourceInvalid
		}
		files = append(files, info)
	}
	return files, nil
}

// liveSourceKind returns 1 for the live main file, 2 for its recovery files,
// or 0 for an independent file. SameFile also catches hard-link aliases.
func liveSourceKind(info os.FileInfo, files []os.FileInfo) int {
	for i, live := range files {
		if os.SameFile(info, live) {
			if i == 0 {
				return 1
			}
			return 2
		}
	}
	return 0
}

func (s *Service) sourceFilesSafe(paths ...string) error {
	files, err := s.sourceGuard.files()
	if err != nil {
		return err
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue // the ordinary source validator reports missing inputs
		}
		if err != nil || liveSourceKind(info, files) != 0 {
			return ErrSourceInvalid
		}
	}
	return nil
}

func (s *Service) sourceManifestSafe(dir string, manifest Manifest) error {
	files, err := s.sourceGuard.files()
	if err != nil {
		return err
	}
	return visitManifest(context.Background(), dir, manifest, func(entry FileEntry) error {
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(entry.Name)))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || liveSourceKind(info, files) != 0 {
			return ErrSourceInvalid
		}
		return nil
	})
}

// Selecting this server's own database is supported without reopening it:
// take the same writer-gated online copy as a manual backup, then let ordinary
// staging inspect the private copy. Inspection alone only classifies the
// already-open live main as plaintext and does not start this work.
func (s *Service) snapshotLiveSource(ctx context.Context, source Source) (Source, func(), error) {
	nothing := func() {}
	if source.Path == "" {
		return source, nothing, nil
	}
	info, err := os.Lstat(source.Path)
	if err != nil || !info.Mode().IsRegular() {
		return source, nothing, nil
	}
	files, err := s.sourceGuard.files()
	if err != nil {
		return source, nothing, err
	}
	switch liveSourceKind(info, files) {
	case 0:
		return source, nothing, nil
	case 2:
		return source, nothing, ErrSourceInvalid
	}
	if canonicalDirectory(filepath.Dir(source.Path), false) != nil {
		return source, nothing, ErrSourceInvalid
	}
	if err = ctx.Err(); err != nil {
		return source, nothing, err
	}
	s.copyMu.Lock()
	defer s.copyMu.Unlock()
	if err = ctx.Err(); err != nil {
		return source, nothing, err
	}
	dir, err := s.privateScratch("backup-snapshot-")
	if err != nil {
		return source, nothing, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	dest := filepath.Join(dir, DatabaseName)
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err == nil {
		err = copyDatabase(ctx, s.db, dest, nil, s.limits(KindPreRestore))
	}
	if err != nil {
		cleanup()
		return source, nothing, err
	}
	return Source{Path: dest}, cleanup, nil
}
