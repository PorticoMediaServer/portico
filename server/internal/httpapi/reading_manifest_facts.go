package httpapi

import "github.com/PorticoMediaServer/portico/server/internal/reading"

// Same fixed 250 words/minute estimate as the existing Short reads producer.
type ReadingEstimatedSeconds int64

func (ReadingEstimatedSeconds) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "maximum": 16106128}
}

type ReadingManifestSeries struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Number    *float64                 `json:"number,omitempty"`
	Source    ReadingAttributionSource `json:"source"`
	LibraryID string                   `json:"libraryId"`
}

func (ReadingManifestSeries) JSONSchema() map[string]any {
	shape := ReadingSeries{}.JSONSchema()
	properties := shape["properties"].(map[string]any)
	properties["id"] = map[string]any{"type": "string", "minLength": 43, "maxLength": 43, "pattern": "^[A-Za-z0-9_-]{43}$"}
	properties["libraryId"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 256}
	shape["required"] = []string{"id", "name", "source", "libraryId"}
	return shape
}

type ReadingWordCount int64

func (ReadingWordCount) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "maximum": 67108864}
}

// Actual admitted comic-page names establish the CBZ count. A PDF count is the
// prepared complete structural page tree from the pinned isolated extractor
// (blank pages included; never the declared /Count or a sample). EPUB screen-page
// counts remain the real client renderer's fact.
type ReadingPageCount int64

func (ReadingPageCount) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": reading.MaxEntries}
}

type ReadingOpeningLines string

func (ReadingOpeningLines) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}
}

// Called only after Prepare's original source/principal admission. Versions and
// catalogue lists do not receive this file-body excerpt through generic metadata.
func readingManifestFactsProjection(out *ReadingMetadata, m reading.Metadata) {
	if m.EstimatedSeconds != nil {
		value := ReadingEstimatedSeconds(*m.EstimatedSeconds)
		out.EstimatedSeconds = &value
	}
	if m.WordCount != nil {
		value := ReadingWordCount(*m.WordCount)
		out.WordCount = &value
	}
	if m.PageCount != nil {
		value := ReadingPageCount(*m.PageCount)
		out.PageCount = &value
	}
	out.OpeningLines = ReadingOpeningLines(m.OpeningLines)
	if m.Series != nil {
		out.Series = &ReadingManifestSeries{ID: m.Series.ID, Name: m.Series.Name, Number: m.Series.Number, Source: ReadingAttributionSource(m.Series.Source), LibraryID: m.SeriesLibraryID}
	}
}
