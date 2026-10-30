package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The physical directory scope and the root path guard are one predicate on
// every platform. inventory_objects.directory_path and .relative_path hold
// native separators, and catalog_assets.path is filepath.Join(root, relative),
// so these bindings use the platform separator instead of a hard-coded slash.
// ReadingPathFolderEvidence keeps comparing the logical slash key; stored paths
// are never normalized.
const (
	readingDirectoryScopeSQL = `(o.directory_path=?3 OR (?4<>'' AND o.directory_path>=?4 AND o.directory_path<?5))`
	readingDirectoryPathSQL  = `a.path=?6||o.relative_path`
)

// readingDirectoryBindings returns the exact-directory match, the prefix range
// covering descendants, and the exact root prefix for the path guard. logical
// is the slash-normalized ReadingDirectoryKey and directory is the native
// folder the walk reported for it.
func readingDirectoryBindings(logical, directory, resolvedRoot, separator string) (base, lower, upper, rootPrefix string) {
	base = directory
	if logical != "" {
		// The logical key is slash-normalized; the stored folder is native.
		base = strings.ReplaceAll(logical, "/", separator)
		lower = base + separator
		// The next byte after the separator bounds only descendants: a sibling
		// whose name continues the same text sorts below the separator.
		upper = base + string(rune(separator[0]+1))
	}
	rootPrefix = filepath.Clean(resolvedRoot)
	if !strings.HasSuffix(rootPrefix, separator) {
		rootPrefix += separator
	}
	return base, lower, upper, rootPrefix
}

// Explicit point publication uses the same typed latest head as the worker.
func publishReadingPathValuesTx(ctx context.Context, tx *sql.Tx, item, asset int64, object, revision string, v compactcatalog.ReadingValues) (bool, error) {
	changed, err := compactcatalog.ReadingPublishPathTx(ctx, tx, item, asset, object, revision, v)
	if err == nil {
		err = compactcatalog.FlushReadingPathSemanticTx(ctx, tx)
	}
	return changed, err
}
