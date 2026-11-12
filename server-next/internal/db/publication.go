package db

import "strings"

// A book's or comic's kind of publication (items.publication). File metadata and owner edits
// take precedence over recognisable issue names and publication folders.
const (
	PublicationBook      = 0
	PublicationMagazine  = 1
	PublicationNewspaper = 2
)

const FieldPublicationType = "publicationType"

var publicationNames = map[string]int{"book": PublicationBook, "magazine": PublicationMagazine, "newspaper": PublicationNewspaper}

// ParsePublication reads a publication's name as a list filter or a file names it.
func ParsePublication(name string) (int, bool) {
	p, ok := publicationNames[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// PublicationName is the name of a publication, as the API says it.
func PublicationName(p int) string {
	for name, v := range publicationNames {
		if v == p {
			return name
		}
	}
	return "book"
}

// publicationFilter keeps the titles of one kind of publication: an issue or book of that kind,
// or a series with at least one (a magazine's or newspaper's run of issues).
func (q ItemQuery) publicationFilter(w *query, acc access) {
	if q.Publication == nil {
		return
	}
	p := itoa(int64(*q.Publication))
	w.add(` AND (i.publication = `, p, ` AND i.kind <> `, itoa(int64(KindSeries)), ` OR i.kind = `, itoa(int64(KindSeries)),
		` AND EXISTS (SELECT 1 FROM items v WHERE `, acc.visible("v"), ` AND v.publication = `, p, ` AND (v.parent_id = i.id OR v.id IN (SELECT related_item_id FROM item_relations WHERE kind = `,
		itoa(int64(RelationSeriesMember)), ` AND item_id = i.id))))`)
}

func (im *importer) describeReading(id int64, it *ImportItem) error {
	if it.WordCount != nil {
		if _, err := im.exec(`UPDATE items SET word_count = ? WHERE id = ?`, *it.WordCount, id); err != nil {
			return err
		}
	}
	// A book's or comic's publication and its issue date, as its own file gives them.
	if it.Kind.Reading() {
		if _, err := im.exec(`UPDATE items SET publication = ?, release_date = coalesce(?, release_date) WHERE id = ?`,
			it.Publication, nz(it.ReleaseDate), id); err != nil {
			return err
		}
	}
	return nil
}
