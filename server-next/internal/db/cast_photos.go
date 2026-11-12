package db

import (
	"context"
	"database/sql"
	"time"
)

// Cast photos (definition area 4, rule 47): a person's photo comes from the provider that
// credits them. A lookup or refresh records its address (recordPhoto) and the picture pass
// fetches it (PhotosToFetch, PictureFetched) while Cast photos are on; turned off, every photo
// fetched from a provider is dropped (DropProviderPhotos) and none is fetched until it is on
// again. A person's photo that is not the provider's (from the files) is left as it is.

// recordPhoto records the provider's photo of a person, by its address, unless the person
// already has one from a provider: one photo each, whichever title credited them first.
func (b *batchTx) recordPhoto(personID int64, address string) error {
	_, err := b.exec(`INSERT INTO images (person_id, role, source, source_url, position, selected)
		SELECT ?, ?, ?, ?, 0, 0 WHERE NOT EXISTS (SELECT 1 FROM images WHERE person_id = ? AND role = ? AND source = ?)`,
		personID, ImagePortrait, ImageFromProvider, address, personID, ImagePortrait, ImageFromProvider)
	return err
}

// PhotosToFetch lists the provider photos not fetched yet of the people credited in a library,
// after an id, a page at a time: the people's part of the library's picture pass. It reads the
// photos_to_fetch index, which holds only those, and each person's credits by their key.
func (d *DB) PhotosToFetch(ctx context.Context, libraryID, after int64, limit int) ([]PictureToFetch, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT i.id, i.role, i.source_url FROM images i
		WHERE i.file_hash IS NULL AND i.source = 3 AND i.person_id IS NOT NULL AND i.id > ? AND i.source_url IS NOT NULL
		AND EXISTS (SELECT 1 FROM credits c CROSS JOIN items t ON t.id = c.item_id WHERE c.person_id = i.person_id AND t.library_id = ?)
		ORDER BY i.id LIMIT ?`, []any{after, libraryID, limit},
		func(p *PictureToFetch) []any { return []any{&p.ID, &p.Role, &p.SourceURL} })
}

// selectPhoto shows the photo a person has with a file: a person's choice first, then the files'
// before the provider's.
func selectPhoto(b *batchTx, personID int64, role ImageRole) error {
	_, err := b.exec(`UPDATE images SET selected = ifnull(id = (SELECT id FROM images WHERE person_id = ? AND role = ? AND file_hash IS NOT NULL
		ORDER BY locked AND selected DESC, source, position, id LIMIT 1), 0) WHERE person_id = ? AND role = ?`, personID, role, personID, role)
	return err
}

// photoFetched records the file of a person's photo the pass fetched and shows it.
func photoFetched(b *batchTx, id, personID int64, role ImageRole, hash string) error {
	if _, err := b.exec(`UPDATE images SET file_hash = ? WHERE id = ? AND file_hash IS NULL`, hash, id); err != nil {
		return err
	}
	if err := selectPhoto(b, personID, role); err != nil {
		return err
	}
	return appendChange(b.tx, Change{At: time.Now().UnixMilli(), Topic: TopicPerson, Op: OpUpdated, RefID: personID})
}

// DropProviderPhotos drops the file of every photo of a person fetched from a provider, for
// Cast photos turned off, a batch at a time at Background priority. The addresses stay
// recorded, so turned on again the picture pass fetches them anew. Each person's shown photo is
// chosen again: one from the files, if they have one. It says how many it dropped.
func (d *DB) DropProviderPhotos(ctx context.Context) (int, error) {
	dropped, after := 0, int64(0)
	for {
		var ids, people []int64
		err := d.write(ctx, Background, func(tx *sql.Tx) error {
			b := newBatchTx(tx)
			defer b.close()
			rows, err := b.tx.Query(`SELECT id, person_id FROM images WHERE id > ? AND person_id IS NOT NULL AND source = 3
				AND file_hash IS NOT NULL ORDER BY id LIMIT 2000`, after)
			if err != nil {
				return err
			}
			ids, people = ids[:0], people[:0]
			for rows.Next() {
				var id, person int64
				if err := rows.Scan(&id, &person); err != nil {
					rows.Close()
					return err
				}
				ids, people = append(ids, id), append(people, person)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			for i, id := range ids {
				if _, err := b.exec(`UPDATE images SET file_hash = NULL, selected = 0 WHERE id = ?`, id); err != nil {
					return err
				}
				if err := selectPhoto(b, people[i], ImagePortrait); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil || len(ids) == 0 {
			return dropped, err
		}
		dropped, after = dropped+len(ids), ids[len(ids)-1]
	}
}
