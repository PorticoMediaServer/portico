package db

import (
	"database/sql"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// reread stores a title's local details again after its NFO file changed, was added or went.
// What the file gives replaces what the title has, a provider's value included: local details
// win. A field or list a person locked is left alone. A field the file no longer gives keeps
// its value and stops being the file's, so the next refresh may replace it.
func (im *importer) reread(id int64, it *ImportItem) error {
	var locked, was, language sql.NullString
	var root sql.NullInt64
	var match MatchState
	if err := im.queryRow(`SELECT i.locked_fields, i.local_fields, i.root_id, i.match_state, l.language FROM items i
		JOIN libraries l ON l.id = i.library_id WHERE i.id = ?`, id).Scan(&locked, &was, &root, &match, &language); err != nil {
		return err
	}
	keep := fieldSet(locked)
	// What the file gives now is the file's, except where a person locked the field; what a
	// person typed and the file does not give stays the person's.
	sources := fieldSources(was)
	for field, source := range sources {
		if source == SourceFile {
			delete(sources, field)
		}
	}
	local := map[string]bool{}
	for _, field := range it.LocalFields {
		if local[field] = !keep[field]; local[field] {
			sources[field] = SourceFile
		}
	}
	sets, args := "local_fields = ?, updated_at = ?, revision = revision + 1", []any{sourceJSON(sources), im.now}
	set := func(field, column string, value any) {
		if local[field] {
			sets += ", " + column + " = ?"
			args = append(args, value)
		}
	}
	set(FieldTitle, "title", it.Title)
	if local[FieldSortTitle] || local[FieldTitle] && !keep[FieldSortTitle] {
		sets += ", sort_title = ?"
		args = append(args, sorttext.Key(it.Title, it.SortTitle, language.String))
	}
	set(FieldOriginalTitle, "original_title", it.OriginalTitle)
	set(FieldSummary, "summary", it.Summary)
	set(FieldTagline, "tagline", it.Tagline)
	set(FieldYear, "year", it.Year)
	set(FieldReleaseDate, "release_date", it.ReleaseDate)
	set(FieldContentRating, "content_rating", it.ContentRating)
	set(FieldContentRating, "rating_age", it.RatingAge)
	set(FieldRating, "community_rating", it.CommunityRating)
	if _, err := im.exec(`UPDATE items SET `+sets+` WHERE id = ?`, append(args, id)...); err != nil {
		return err
	}
	if local[FieldContentRating] && !root.Valid {
		if _, err := im.exec(`UPDATE items SET rating_age = ? WHERE root_id = ? AND content_rating IS NULL`, it.RatingAge, id); err != nil {
			return err
		}
	}
	// A match a person chose, or locked, is theirs: an id the file now carries does not replace
	// it, or the next refresh would bring another title's details.
	ids := it.ExternalIDs
	if match == MatchChosen || keep[FieldMatch] {
		ids = nil
	}
	for _, ext := range ids {
		if _, err := im.exec(`INSERT INTO item_external_ids (item_id, provider, external_id) VALUES (?, ?, ?)
			ON CONFLICT (item_id, provider) DO UPDATE SET external_id = excluded.external_id`, id, ext.Provider, ext.ID); err != nil {
			return err
		}
	}
	cleared := map[string]bool{}
	for _, tag := range it.Tags {
		field, known := tagFields[tag.Kind]
		if !known || !local[field] {
			continue
		}
		if !cleared[tag.Kind] {
			cleared[tag.Kind] = true
			if _, err := im.exec(`DELETE FROM item_tags WHERE item_id = ? AND tag_id IN (SELECT id FROM tags WHERE kind = ?)`, id, tag.Kind); err != nil {
				return err
			}
		}
		tagID, err := im.tagID(tag)
		if err != nil {
			return err
		}
		if _, err := im.exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, tagID); err != nil {
			return err
		}
	}
	if local[FieldCredits] && len(it.Credits) > 0 {
		if _, err := im.exec(`DELETE FROM credits WHERE item_id = ?`, id); err != nil {
			return err
		}
		for position, credit := range it.Credits {
			personID, err := im.personID(credit)
			if err != nil {
				return err
			}
			if err := im.insertCredit(id, position, personID, credit.Role, credit.Character); err != nil {
				return err
			}
		}
	}
	if err := im.localImages(id, it.Images); err != nil {
		return err
	}
	return appendChange(im.tx, Change{At: im.now, Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: im.batch.LibraryID})
}

// localImages stores a title's pictures. One that was copied from a library file is the row
// of that file: a file that changed replaces its own picture.
func (im *importer) localImages(itemID int64, images []ImportImage) error {
	roles := map[ImageRole]bool{}
	for position, image := range images {
		if image.File == nil {
			if err := im.insertImage(itemID, position, image); err != nil {
				return err
			}
			continue
		}
		if err := checkCode("images.role", imageRoleNames, image.Role); err != nil {
			return err
		}
		f, err := im.file(*image.File)
		if err != nil {
			return err
		}
		// A file that changed keeps its row, so a picture a person chose stays chosen.
		res, err := im.exec(`UPDATE images SET file_hash = ?, width = ?, height = ? WHERE file_id = ? AND item_id = ? AND role = ?`,
			image.FileHash, nz(image.Width), nz(image.Height), f.id, itemID, image.Role)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := im.exec(`DELETE FROM images WHERE file_id = ?`, f.id); err != nil {
				return err
			}
			if _, err := im.exec(`INSERT INTO images (item_id, file_id, role, source, file_hash, width, height, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				itemID, f.id, image.Role, ImageFromFile, image.FileHash, nz(image.Width), nz(image.Height), position); err != nil {
				return err
			}
		}
		roles[image.Role] = true
	}
	for role := range roles {
		if err := selectPicture(im.batchTx, itemID, role); err != nil {
			return err
		}
	}
	return nil
}

type shownPicture struct {
	item int64
	role ImageRole
}

// shownPictures are the titles and roles for which a library file's picture is the one shown.
func shownPictures(b *batchTx, fileID int64) ([]shownPicture, error) {
	rows, err := b.tx.Query(`SELECT item_id, role FROM images WHERE file_id = ? AND item_id IS NOT NULL`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []shownPicture
	for rows.Next() {
		var p shownPicture
		if err := rows.Scan(&p.item, &p.role); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// selectPicture works out again which picture a title shows for a role. One a person chose
// (locked) stays. Otherwise it is the best there is: a picture beside the media first, then
// one from inside the file, then the provider's, each in the order they were found. A
// provider's picture that has not been fetched (no file_hash) cannot be shown.
func selectPicture(b *batchTx, itemID int64, role ImageRole) error {
	_, err := b.exec(`UPDATE images SET selected = (id = (SELECT id FROM images WHERE item_id = ? AND role = ? AND file_hash IS NOT NULL
		ORDER BY locked AND selected DESC, source, position, id LIMIT 1)) WHERE item_id = ? AND role = ?`, itemID, role, itemID, role)
	return err
}
