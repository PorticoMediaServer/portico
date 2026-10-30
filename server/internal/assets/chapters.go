package assets

import "database/sql"

func PersistChapters(tx *sql.Tx, asset string, size, modified int64, f Facts) error {
	status, chapters, fingerprint, e := chapterInput(f)
	if e != nil || status == "" {
		return e
	}
	_, e = tx.Exec(`INSERT INTO asset_chapter_facts VALUES(?,1,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET revision=asset_chapter_facts.revision+CASE WHEN asset_chapter_facts.fingerprint!=excluded.fingerprint OR asset_chapter_facts.size!=excluded.size OR asset_chapter_facts.modified_ns!=excluded.modified_ns THEN 1 ELSE 0 END,size=excluded.size,modified_ns=excluded.modified_ns,status=excluded.status,fingerprint=excluded.fingerprint`, asset, size, modified, status, fingerprint)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`DELETE FROM asset_chapters WHERE asset_id=?`, asset); e != nil {
		return e
	}
	for i, c := range chapters {
		if _, e = tx.Exec(`INSERT INTO asset_chapters VALUES(?,?,?,?,?)`, asset, i+1, streamText(c.Title, 500), c.Start, c.End); e != nil {
			return e
		}
	}
	return nil
}
