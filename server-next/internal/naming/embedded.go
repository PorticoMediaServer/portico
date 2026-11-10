package naming

const EmbeddedMetadata Source = "embedded_metadata"

type Embedded struct {
	Title     string
	ShowTitle string
	Year      int
	Season    *int
	Number    *int
	Edition   string
	IDs       []ProviderID
}

// ReadWithEmbedded combines file tags with the same folder/file reading.
// A movie title or explicit show tag is file evidence; an episode's display
// title is not a series name. Explicit filename coordinates retain precedence.
func ReadWithEmbedded(relative, kind string, e Embedded) Reading {
	r := Read(relative, kind)
	title := e.Title
	if kind == "tv" || kind == "anime" {
		title = e.ShowTitle
	}
	if title != "" {
		r.Titles = append([]Evidence[string]{ev(clean(title), EmbeddedMetadata, relative, .99)}, r.Titles...)
	}
	if e.Year > 0 {
		r.Years = append([]Evidence[int]{ev(e.Year, EmbeddedMetadata, relative, .99)}, r.Years...)
	}
	if e.Edition != "" {
		r.Editions = append([]Evidence[string]{ev(clean(e.Edition), EmbeddedMetadata, relative, .99)}, r.Editions...)
	}
	fileSeason := false
	for _, s := range r.Seasons {
		fileSeason = fileSeason || s.Source == FileName
	}
	if !fileSeason && e.Season != nil {
		r.Seasons = append([]Evidence[int]{ev(*e.Season, EmbeddedMetadata, relative, .99)}, r.Seasons...)
	}
	if len(r.Episodes) == 0 && e.Number != nil {
		r.Episodes = append(r.Episodes, ev([]int{*e.Number}, EmbeddedMetadata, relative, .99))
	}
	ids := []Evidence[ProviderID]{}
	for _, id := range e.IDs {
		ids = append(ids, ev(id, EmbeddedMetadata, relative, .99))
	}
	r.ProviderIDs = append(ids, r.ProviderIDs...)
	return r
}
