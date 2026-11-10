package scan

import (
	"path"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/contentrating"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/localmetadata"
)

// nfo reads the first NFO file among the given paths that holds a record of the wanted kind.
// It answers nil when the library's depth leaves sidecars unread, when there is none, and
// when one cannot be read: a sidecar that cannot be read changes nothing.
func (r *run) nfo(kind string, paths ...string) *localmetadata.VideoNFO {
	if r.lib.Options.Depth == db.DepthFiles {
		return nil
	}
	for _, rel := range paths {
		found, known := r.nfos[rel]
		if !known {
			if raw, err := readSmall(r.ctx, r.src, rel, 256<<10); err == nil {
				if records, err := localmetadata.ParseVideoNFO(raw, r.lib.Kind == db.LibraryAnime); err == nil {
					found = records
				}
			}
			r.nfos[rel] = found
		}
		for i := range found {
			if found[i].Kind == kind || found[i].Kind == "work" {
				return &found[i]
			}
		}
	}
	return nil
}

// describe fills an item from an NFO record: an id in it is an exact match, and what it says
// is the files' own word, which wins over the folder's name and over any provider. The item
// is told which of its fields the file gave.
func describe(item *db.ImportItem, nfo *localmetadata.VideoNFO, kind string) {
	if nfo == nil {
		return
	}
	text := func(field string, to *string, value string) {
		if value != "" {
			*to = value
			item.LocalFields = append(item.LocalFields, field)
		}
	}
	text(db.FieldTitle, &item.Title, nfo.Title)
	text(db.FieldSortTitle, &item.SortTitle, nfo.SortTitle)
	text(db.FieldOriginalTitle, &item.OriginalTitle, nfo.OriginalTitle)
	text(db.FieldSummary, &item.Summary, nfo.Overview)
	text(db.FieldTagline, &item.Tagline, nfo.Tagline)
	text(db.FieldReleaseDate, &item.ReleaseDate, nfo.Date)
	text(db.FieldContentRating, &item.ContentRating, nfo.Certification)
	if age, ok := contentrating.Age(nfo.Certification); ok {
		item.RatingAge = &age
	}
	if nfo.Year > 0 {
		item.Year = nfo.Year
		item.LocalFields = append(item.LocalFields, db.FieldYear)
	}
	for _, genre := range nfo.Genres {
		item.Tags = append(item.Tags, db.TagName{Kind: db.TagGenre, Name: genre})
	}
	for _, studio := range nfo.Studios {
		item.Tags = append(item.Tags, db.TagName{Kind: db.TagStudio, Name: studio})
	}
	if len(nfo.Genres) > 0 {
		item.LocalFields = append(item.LocalFields, db.FieldGenres)
	}
	if len(nfo.Studios) > 0 {
		item.LocalFields = append(item.LocalFields, db.FieldStudios)
	}
	for _, credit := range nfo.Credits {
		role, character := db.RoleActor, credit.Role
		switch credit.Department {
		case "Acting":
		case "Directing":
			role, character = db.RoleDirector, ""
		case "Writing":
			role, character = db.RoleWriter, ""
		default:
			continue
		}
		item.Credits = append(item.Credits, db.ImportCredit{Name: credit.Name, Role: role, Character: character})
	}
	if len(item.Credits) > 0 {
		item.LocalFields = append(item.LocalFields, db.FieldCredits)
	}
	if kind == "episode" {
		return // an episode is found by its place in its show, not by an id
	}
	have := map[string]bool{}
	for _, id := range item.ExternalIDs {
		have[id.Provider] = true
	}
	var keys []db.ItemKey
	for _, id := range nfo.IDs {
		if have[id.Provider] || id.Type == "episode" || id.Type == "season" {
			continue
		}
		have[id.Provider] = true
		item.ExternalIDs = append(item.ExternalIDs, db.ExternalID{Provider: id.Provider, ID: id.ID})
		keys = append(keys, db.ItemKey{Kind: db.KeyProvider, Key: id.Provider + ":" + kind + ":" + id.ID})
	}
	// Ids go ahead of the name and folder keys and after the file keys.
	at := 0
	for at < len(item.Keys) && item.Keys[at].Kind == db.KeyFile {
		at++
	}
	item.Keys = append(item.Keys[:at:at], append(keys, item.Keys[at:]...)...)
}

// filmNFOs are where a film's NFO file may be: beside it with its name, or movie.nfo in a
// folder the film has to itself.
func filmNFOs(rel string) []string {
	dir := path.Dir(rel)
	paths := []string{strings.TrimSuffix(rel, path.Ext(rel)) + ".nfo"}
	if dir != "." {
		paths = append(paths, path.Join(dir, "movie.nfo"))
	}
	return paths
}
