package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"strings"
)

// ResolveExtraOwnerTx attaches an extra only when its owning folder or filename
// identifies one title. Loose films sharing a folder do not inherit each
// other's trailers. Provider matching never supplies an extra's ownership.
func ResolveExtraOwnerTx(ctx context.Context, tx *sql.Tx, library, path, parent string) (int64, error) {
	prefix := filepath.Clean(parent)
	separator := string(filepath.Separator)
	if !strings.HasSuffix(prefix, separator) {
		prefix += separator
	}
	// A path prefix ends with an ASCII separator on every supported platform.
	// The next byte supplies an indexed half-open range, including literal %/_ names.
	end := prefix[:len(prefix)-1] + string(prefix[len(prefix)-1]+1)
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN e.kind=? THEN ep.show_id ELSE e.id END,COALESCE(sh.title,e.title),a.path,e.kind FROM catalog_assets a CROSS JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id LEFT JOIN catalog_episodes ep ON ep.entity_id=e.id LEFT JOIN catalog_entities sh ON sh.id=ep.show_id WHERE cl.library_id=? AND e.kind IN(?,?) AND a.path>=? AND a.path<? LIMIT 513`, compactcatalog.Episode, library, compactcatalog.Movie, compactcatalog.Episode, prefix, end)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	owners := map[int64]bool{}
	named := map[int64]bool{}
	title, _ := naming.Read(filepath.Base(path), "movie").PreferredTitle()
	count := 0
	for rows.Next() {
		count++
		if count > 512 {
			// A bounded prefix cannot prove either a unique name or a sole owner.
			return 0, nil
		}
		var owner int64
		var name, asset string
		var kind int
		if err = rows.Scan(&owner, &name, &asset, &kind); err != nil {
			return 0, err
		}
		if kind == int(compactcatalog.Movie) && filepath.Dir(asset) != parent {
			continue
		}
		owners[owner] = true
		if compactcatalog.IdentityText(name) == compactcatalog.IdentityText(title) {
			named[owner] = true
		}
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if len(named) == 1 {
		for id := range named {
			return id, nil
		}
	}
	if len(owners) == 1 {
		for id := range owners {
			return id, nil
		}
	}
	return 0, nil
}

// RefreshExtraOwnersTx runs after inventory so extras listed before their
// owning show's first episode still attach during the same scan.
func RefreshExtraOwnersTx(ctx context.Context, tx *sql.Tx, source LibrarySource, after int64) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.item_id,a.path,e.parent_path FROM item_extras e JOIN inventory_objects o ON o.asset_id=e.asset_id JOIN catalog_assets a ON a.token=e.asset_id WHERE o.source_id=? AND o.root_incarnation=? AND o.retired=0 AND o.state='available' AND e.library_id=? AND e.item_id>? ORDER BY e.item_id LIMIT 64`, source.ID, source.Incarnation, source.LibraryID, after)
	if err != nil {
		return after, err
	}
	type extra struct {
		id           int64
		path, parent string
	}
	batch := []extra{}
	for rows.Next() {
		var v extra
		if err = rows.Scan(&v.id, &v.path, &v.parent); err != nil {
			rows.Close()
			return after, err
		}
		batch = append(batch, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return after, err
	}
	for _, v := range batch {
		owner, err := ResolveExtraOwnerTx(ctx, tx, source.LibraryID, v.path, v.parent)
		if err != nil {
			return after, err
		}
		var nullable any
		if owner != 0 {
			nullable = owner
		}
		if _, err = tx.ExecContext(ctx, `UPDATE item_extras SET owner_id=? WHERE item_id=? AND owner_id IS NOT ?`, nullable, v.id, nullable); err != nil {
			return after, err
		}
		after = v.id
	}
	if len(batch) == 0 {
		return 0, nil
	}
	return after, nil
}
