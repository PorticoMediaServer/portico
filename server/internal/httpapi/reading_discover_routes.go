package httpapi

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// ReadingDiscoverRow is one server-composed Discover row. The key names the row
// the client asked for; items use the same public projection as List/Continue.
// NextCursor continues this row alone (request it with row=key and cursor).
type ReadingDiscoverRow struct {
	Key        string                   `json:"key"`
	NextCursor string                   `json:"nextCursor,omitempty"`
	Items      []ReadingItem            `json:"items"`
	Series     []ReadingDiscoverSeries  `json:"series,omitempty"`
	Newspaper  *ReadingPeriodicalGroup  `json:"newspaper,omitempty"`
	NewIssues  []ReadingPeriodicalIssue `json:"newIssues,omitempty"`
	ShortReads []ReadingShortRead       `json:"shortReads,omitempty"`
}

// Seconds round up for display only; membership uses the exact complete count.
type ReadingShortRead struct {
	Item             ReadingItem `json:"item"`
	WordCount        int64       `json:"wordCount"`
	EstimatedSeconds int64       `json:"estimatedSeconds"`
}

func (ReadingShortRead) JSONSchema() map[string]any {
	type wire ReadingShortRead
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["wordCount"].(map[string]any)["minimum"] = 1
	props["wordCount"].(map[string]any)["maximum"] = 44999
	props["estimatedSeconds"].(map[string]any)["minimum"] = 1
	props["estimatedSeconds"].(map[string]any)["maximum"] = 10800
	return shape
}

// ReadingDiscoverSeries is a summary of actual eligible library membership.
// Preview is bounded canonical membership, not a replacement for the signed
// series endpoint. Counts are exact over all visible titles in this series.
type ReadingDiscoverSeries struct {
	ID        string        `json:"id"`
	LibraryID string        `json:"libraryId"`
	Name      string        `json:"name"`
	Count     int           `json:"count"`
	ReadCount int           `json:"readCount"`
	Preview   []ReadingItem `json:"preview"`
}

func (ReadingDiscoverSeries) JSONSchema() map[string]any {
	type wire ReadingDiscoverSeries
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["id"].(map[string]any)["pattern"] = "^[A-Za-z0-9_-]{43}$"
	props["libraryId"].(map[string]any)["minLength"] = 1
	props["name"].(map[string]any)["minLength"] = 1
	props["name"].(map[string]any)["maxLength"] = 4096
	props["count"].(map[string]any)["minimum"] = 1
	props["readCount"].(map[string]any)["minimum"] = 0
	props["preview"].(map[string]any)["minItems"] = 1
	props["preview"].(map[string]any)["maxItems"] = 3
	return shape
}

// ReadingDiscoverResponse publishes only rows supported by real producers.
type ReadingDiscoverResponse struct {
	Rows []ReadingDiscoverRow `json:"rows"`
}

func (d Dependencies) readingDiscover(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	library, row, cursor := q.Get("libraryId"), q.Get("row"), q.Get("cursor")
	// bounded reads one optional limit parameter; rows other than recent need a library.
	bounded := func(name string, low, high int, library bool) (int, bool) {
		if !q.Has(name) {
			return 0, true
		}
		value, err := strconv.Atoi(q.Get(name))
		return value, err == nil && value >= low && value <= high && (!library || q.Get("libraryId") != "")
	}
	var limits reading.DiscoverLimits
	valid := true
	if row == "" {
		var ok [5]bool
		limits.Recent, ok[0] = bounded("limit", 1, 100, false)
		limits.Series, ok[1] = bounded("seriesLimit", 1, 20, true)
		limits.ShortReads, ok[2] = bounded("shortReadsLimit", 1, 20, true)
		limits.NewIssues, ok[3] = bounded("newIssuesLimit", 1, 20, true)
		limits.Newspaper, ok[4] = bounded("newspaperLimit", 1, 1, true)
		valid = ok == [5]bool{true, true, true, true, true} && !q.Has("cursor")
	} else {
		// One row continues alone, bounded by limit; the other rows' limits are absent.
		high := 20
		if row == reading.DiscoverRecent {
			high = 100
		}
		limit, ok := bounded("limit", 1, high, row != reading.DiscoverRecent)
		if !q.Has("limit") {
			limit = 20
		}
		valid = ok && !q.Has("seriesLimit") && !q.Has("shortReadsLimit") && !q.Has("newIssuesLimit") && !q.Has("newspaperLimit")
		limits.Row, limits.Cursor = row, cursor
		switch row {
		case reading.DiscoverRecent:
			limits.Recent = limit
		case reading.DiscoverSeriesRow:
			limits.Series = limit
		case reading.DiscoverShortReads:
			limits.ShortReads = limit
		case reading.DiscoverNewIssues:
			limits.NewIssues = limit
		default:
			valid = false
		}
	}
	if !valid {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	discover, err := d.readingService().Discover(r.Context(), p, library, limits)
	if err != nil {
		readingFailure(w, err)
		return
	}
	truncate := func(key string) func(int) error { return func(n int) error { return discover.Truncate(key, n) } }
	out := ReadingDiscoverResponse{Rows: []ReadingDiscoverRow{}}
	emit := func(key string, present bool, compose func() (ReadingDiscoverRow, error)) bool {
		if !present && row != key {
			return true
		}
		composed, err := compose()
		if err != nil {
			readingFailure(w, err)
			return false
		}
		composed.Key, composed.NextCursor = key, discover.Next[key]
		out.Rows = append(out.Rows, composed)
		return true
	}
	if !emit(reading.DiscoverRecent, row == "", func() (ReadingDiscoverRow, error) {
		items := make([]ReadingItem, 0, len(discover.Recent))
		for _, entry := range discover.Recent {
			items = append(items, readingItemProjection(entry))
		}
		items, err := readingBudget(items, readingDiscoverRowBytes, truncate(reading.DiscoverRecent))
		return ReadingDiscoverRow{Items: items}, err
	}) || !emit(reading.DiscoverSeriesRow, len(discover.Series) > 0, func() (ReadingDiscoverRow, error) {
		series := []ReadingDiscoverSeries{}
		for _, entry := range discover.Series {
			summary := ReadingDiscoverSeries{ID: entry.ID, LibraryID: entry.LibraryID, Name: entry.Name, Count: entry.Count, ReadCount: entry.ReadCount, Preview: []ReadingItem{}}
			for _, item := range entry.Preview {
				summary.Preview = append(summary.Preview, readingItemProjection(item))
			}
			series = append(series, summary)
		}
		series, err := readingBudget(series, readingDiscoverRowBytes, truncate(reading.DiscoverSeriesRow))
		return ReadingDiscoverRow{Items: []ReadingItem{}, Series: series}, err
	}) || !emit(reading.DiscoverShortReads, len(discover.ShortReads) > 0, func() (ReadingDiscoverRow, error) {
		short := make([]ReadingShortRead, 0, len(discover.ShortReads))
		for _, entry := range discover.ShortReads {
			short = append(short, ReadingShortRead{Item: readingItemProjection(entry.Item), WordCount: entry.WordCount, EstimatedSeconds: entry.EstimatedSeconds})
		}
		short, err := readingBudget(short, readingDiscoverRowBytes, truncate(reading.DiscoverShortReads))
		return ReadingDiscoverRow{Items: []ReadingItem{}, ShortReads: short}, err
	}) || !emit(reading.DiscoverNewIssues, len(discover.NewIssues) > 0, func() (ReadingDiscoverRow, error) {
		issues := make([]ReadingPeriodicalIssue, 0, len(discover.NewIssues))
		for _, item := range discover.NewIssues {
			issues = append(issues, ReadingPeriodicalIssue{Item: readingItemProjection(item), IssueDate: ReadingIssueDate(item.IssueDate)})
		}
		issues, err := readingBudget(issues, readingDiscoverRowBytes, truncate(reading.DiscoverNewIssues))
		return ReadingDiscoverRow{Items: []ReadingItem{}, NewIssues: issues}, err
	}) {
		return
	}
	if group := discover.Newspaper; group != nil {
		newspaper := &ReadingPeriodicalGroup{ID: group.ID, Name: group.Name, IssueCount: group.IssueCount, Latest: ReadingPeriodicalIssue{Item: readingItemProjection(group.Latest), IssueDate: ReadingIssueDate(group.Latest.IssueDate)}}
		out.Rows = append(out.Rows, ReadingDiscoverRow{Key: "newspaper", Items: []ReadingItem{}, Newspaper: newspaper})
	}
	write(w, 200, out)
}

// registerReadingDiscoverHTTPContract publishes only the additive Discover
// operation; the vocabulary is a rows-only payload of the existing item shape.
func registerReadingDiscoverHTTPContract(registry *apikit.Registry) error {
	errors := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_required", "revision_mismatch", "request_in_progress", "body_too_large", "internal"}
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_reading_discover", Method: "GET", Path: "/v1/reading/discover", Summary: "Read the server-composed Discover rows", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: errors, Query: []string{"libraryId", "limit", "seriesLimit", "shortReadsLimit", "newIssuesLimit", "newspaperLimit", "row", "cursor"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 20, Maximum: 100, Description: "With row other than recent, the maximum is 20; otherwise the maximum is 100."}}}
	return apikit.RegisterHTTP[struct{}, ReadingDiscoverResponse](registry, m)
}
