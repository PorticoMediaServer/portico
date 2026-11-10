package db

import (
	"context"
	"database/sql"
	"errors"
)

// MetadataDocument is what the metadata editor opens: a title's fields, lists, cast and crew,
// locks and match, and the revision an edit must name.
type MetadataDocument struct {
	Kind     ItemKind
	Revision int64
	Match    MatchState
	Fields   map[string]any      // by field name; nil for a field with no value
	Lists    map[string][]string // genres, studios, networks, countries, labels
	Credits  []DocumentCredit
	Locked   []string
	// Sources says where each value that is not the provider's came from: SourceFile or
	// SourceEdit, by field.
	Sources     map[string]string
	ExternalIDs []ExternalID
}

// DocumentCredit is one line of Cast & crew.
type DocumentCredit struct {
	Person    string // the person's public id
	Name      string
	Role      CreditRole
	Character string
}

// MetadataDocument reads one title for the editor.
func (d *DB) MetadataDocument(ctx context.Context, publicID string) (MetadataDocument, error) {
	var doc MetadataDocument
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return doc, err
	}
	defer tx.Rollback()
	var id int64
	var title, sortTitle string
	var original, summary, tagline, edition, release, rating, locked, local sql.NullString
	var year sql.NullInt64
	var community sql.NullFloat64
	err = tx.QueryRowContext(ctx, `SELECT id, kind, revision, match_state, title, sort_title, original_title, summary, tagline, edition, year,
		release_date, content_rating, community_rating, locked_fields, local_fields FROM items WHERE public_id = ?`, publicID).Scan(
		&id, &doc.Kind, &doc.Revision, &doc.Match, &title, &sortTitle, &original, &summary, &tagline, &edition, &year, &release, &rating,
		&community, &locked, &local)
	if errors.Is(err, sql.ErrNoRows) {
		return doc, ErrNotFound
	}
	if err != nil {
		return doc, err
	}
	text := func(v sql.NullString) any {
		if v.Valid {
			return v.String
		}
		return nil
	}
	doc.Fields = map[string]any{FieldTitle: title, FieldSortTitle: sortTitle, FieldOriginalTitle: text(original), FieldSummary: text(summary),
		FieldTagline: text(tagline), FieldEdition: text(edition), FieldReleaseDate: text(release), FieldContentRating: text(rating),
		FieldYear: nil, FieldRating: nil}
	if year.Valid {
		doc.Fields[FieldYear] = year.Int64
	}
	if community.Valid {
		doc.Fields[FieldRating] = community.Float64
	}
	doc.Locked, doc.Sources = sortedFields(fieldSet(locked)), fieldSources(local)
	tags, err := queryAll(ctx, tx, `SELECT t.kind, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id = ?
		AND t.kind IN ('genre', 'studio', 'network', 'country', 'label') ORDER BY t.name`, []any{id},
		func(t *TagName) []any { return []any{&t.Kind, &t.Name} })
	if err != nil {
		return doc, err
	}
	doc.Lists = map[string][]string{FieldGenres: {}, FieldStudios: {}, FieldNetworks: {}, FieldCountries: {}, "labels": {}}
	for _, tag := range tags {
		name := tag.Kind + "s"
		if tag.Kind == TagCountry {
			name = FieldCountries
		}
		doc.Lists[name] = append(doc.Lists[name], tag.Name)
	}
	if doc.Credits, err = queryAll(ctx, tx, `SELECT p.public_id, p.name, c.role, ifnull(c.character, '') FROM credits c
		JOIN people p ON p.id = c.person_id WHERE c.item_id = ? ORDER BY c.position`, []any{id},
		func(c *DocumentCredit) []any { return []any{&c.Person, &c.Name, &c.Role, &c.Character} }); err != nil {
		return doc, err
	}
	doc.ExternalIDs, err = queryAll(ctx, tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ? ORDER BY provider`,
		[]any{id}, func(e *ExternalID) []any { return []any{&e.Provider, &e.ID} })
	return doc, err
}
