package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
)

// Page rows are retained only for the exact scan/configuration. Discovery refuses
// partial directories; these snapshots never prove absence on their own.
func CommitManagedPage(ctx context.Context, tx *sql.Tx, source, incarnation, job, directory string, entries []storage.Snapshot) error {
	for _, entry := range entries {
		if filepath.Dir(entry.Path) != directory {
			return ErrConflict
		}
		raw, e := json.Marshal(entry)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_inventory_entries(source_id,incarnation,job_id,directory,directory_key,path,name,snapshot) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source_id,job_id,path) DO UPDATE SET directory_key=excluded.directory_key,name=excluded.name,snapshot=excluded.snapshot`, source, incarnation, job, directory, strings.ToLower(directory), entry.Path, strings.ToLower(entry.Name), string(raw)); e != nil {
			return e
		}
	}
	return nil
}
func DiscoverManaged(ctx context.Context, db *sql.DB, c *storage.Client, source, incarnation, job, relative, path string, embedded []Track, origin int64, known bool) (*Inventory, error) {
	// A source's own folder is listed as "."; a caller may also name it by the
	// empty path. Without this a title sitting directly in a library's folder
	// never found its folder's listing, and its scan ended with a warning.
	if relative == "" {
		relative = "."
	}
	var done int
	e := db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_directories d JOIN inventory_runs r ON r.job_id=d.job_id JOIN library_sources s ON s.id=r.source_id WHERE d.job_id=? AND d.relative_path=? AND d.state='done' AND s.id=? AND s.incarnation=? AND s.generation=r.source_generation AND s.enabled=1`, job, relative, source, incarnation).Scan(&done)
	if e != nil {
		return nil, e
	}
	if done != 1 {
		return &Inventory{Status: "unavailable"}, nil
	}
	stem := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(stem)
	directory := strings.ToLower(filepath.Dir(path))
	rows, e := db.QueryContext(ctx, `SELECT snapshot FROM subtitle_inventory_entries WHERE source_id=? AND incarnation=? AND job_id=? AND directory_key IN(?,?,?) AND name LIKE ? ESCAPE '\' ORDER BY path LIMIT 4097`, source, incarnation, job, directory, strings.ToLower(filepath.Join(filepath.Dir(path), "Subs")), strings.ToLower(filepath.Join(filepath.Dir(path), "Subtitles")), escaped+".%")
	if e != nil {
		return nil, e
	}
	var entries []storage.Snapshot
	for rows.Next() {
		var raw string
		var v storage.Snapshot
		if e = rows.Scan(&raw); e != nil {
			break
		}
		if e = json.Unmarshal([]byte(raw), &v); e != nil {
			break
		}
		entries = append(entries, v)
	}
	rowError := rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if rowError != nil {
		return nil, rowError
	}
	return discover(ctx, c, source, path, embedded, origin, known, func(visit func(storage.Snapshot) error) error {
		for _, v := range entries {
			if e := visit(v); e != nil {
				return e
			}
		}
		return nil
	})
}
