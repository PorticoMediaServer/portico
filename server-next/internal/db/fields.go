package db

import (
	"database/sql"
	"encoding/json"
)

// The names of the fields and lists a title can have locked (items.locked_fields) or given by
// its files (items.local_fields). They are also the names the metadata editor uses.
const (
	FieldTitle         = "title"
	FieldSortTitle     = "sortTitle"
	FieldOriginalTitle = "originalTitle"
	FieldSummary       = "summary"
	FieldTagline       = "tagline"
	FieldYear          = "year"
	FieldReleaseDate   = "releaseDate"
	FieldContentRating = "contentRating"
	FieldRating        = "rating"
	FieldGenres        = "genres"
	FieldStudios       = "studios"
	FieldNetworks      = "networks"
	FieldCountries     = "countries"
	FieldCredits       = "credits"
	FieldMatch         = "match"
	FieldEdition       = "edition"
)

// tagFields are the lists a provider or an NFO file speaks for, by tag kind. Labels and moods
// are not here: no provider's answer and no scan ever changes them.
var tagFields = map[string]string{TagGenre: FieldGenres, TagStudio: FieldStudios, TagNetwork: FieldNetworks, TagCountry: FieldCountries}

// fieldSet is the field names in JSON lists such as items.locked_fields.
func fieldSet(lists ...sql.NullString) map[string]bool {
	out := map[string]bool{}
	for _, list := range lists {
		var names []string
		if list.Valid {
			json.Unmarshal([]byte(list.String), &names)
		}
		for _, name := range names {
			out[name] = true
		}
	}
	return out
}

// fieldList is field names as the JSON list a column stores, or NULL for none.
func fieldList(fields []string) any {
	if len(fields) == 0 {
		return nil
	}
	raw, _ := json.Marshal(fields)
	return string(raw)
}
