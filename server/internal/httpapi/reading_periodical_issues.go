package httpapi

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

type ReadingPeriodicalType string

func (ReadingPeriodicalType) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"magazine", "newspaper"}}
}

type ReadingIssueDate string

func (ReadingIssueDate) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}(-[0-9]{2})?$", "minLength": 7, "maxLength": 10}
}

type ReadingPeriodicalOrder string

func (ReadingPeriodicalOrder) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{reading.PeriodicalIssuesNewest, reading.PeriodicalIssuesOldest}}
}

type ReadingPeriodicalIssue struct {
	Item      ReadingItem      `json:"item"`
	IssueDate ReadingIssueDate `json:"issueDate"`
}

func (ReadingPeriodicalIssue) JSONSchema() map[string]any {
	type wire ReadingPeriodicalIssue
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	return shape
}

type ReadingPeriodicalYear struct {
	Year       string `json:"year"`
	IssueCount int64  `json:"issueCount"`
}

func (ReadingPeriodicalYear) JSONSchema() map[string]any {
	type wire ReadingPeriodicalYear
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["year"].(map[string]any)["pattern"] = "^[0-9]{4}$"
	props["issueCount"].(map[string]any)["minimum"] = 1
	props["issueCount"].(map[string]any)["maximum"] = 9007199254740991
	return shape
}

type ReadingPeriodicalDay struct {
	Date       ReadingIssueDate `json:"date"`
	IssueCount int64            `json:"issueCount"`
}

func (ReadingPeriodicalDay) JSONSchema() map[string]any {
	type wire ReadingPeriodicalDay
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["date"].(map[string]any)["pattern"] = "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"
	props["issueCount"].(map[string]any)["minimum"] = 1
	props["issueCount"].(map[string]any)["maximum"] = 9007199254740991
	return shape
}

type ReadingPeriodicalIssuesResponse struct {
	Month           string                   `json:"month,omitempty"`
	Days            []ReadingPeriodicalDay   `json:"days"`
	Year            string                   `json:"year,omitempty"`
	Years           []ReadingPeriodicalYear  `json:"years"`
	LibraryID       string                   `json:"libraryId"`
	SeriesID        string                   `json:"seriesId"`
	PublicationType ReadingPeriodicalType    `json:"publicationType"`
	IssueDate       ReadingIssueDate         `json:"issueDate,omitempty"`
	Order           ReadingPeriodicalOrder   `json:"order,omitempty"`
	Items           []ReadingPeriodicalIssue `json:"items"`
	NextCursor      string                   `json:"nextCursor,omitempty"`
}

func (ReadingPeriodicalIssuesResponse) JSONSchema() map[string]any {
	type wire ReadingPeriodicalIssuesResponse
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["year"].(map[string]any)["pattern"] = "^[0-9]{4}$"
	props["years"].(map[string]any)["maxItems"] = 9999
	props["month"].(map[string]any)["pattern"] = "^[0-9]{4}-[0-9]{2}$"
	props["order"].(map[string]any)["enum"] = []string{reading.PeriodicalIssuesNewest, reading.PeriodicalIssuesOldest}
	props["days"].(map[string]any)["maxItems"] = 31
	props["items"].(map[string]any)["maxItems"] = 100
	props["nextCursor"].(map[string]any)["pattern"] = "^[A-Za-z0-9_-]{171}\\.[A-Za-z0-9_-]{43}$"
	return shape
}

func (d Dependencies) readingPeriodicalIssues(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	library, series, kind := r.PathValue("libraryId"), r.PathValue("seriesId"), r.URL.Query().Get("publicationType")
	date := r.URL.Query().Get("issueDate")
	year := r.URL.Query().Get("year")
	month := r.URL.Query().Get("month")
	order := r.URL.Query().Get("order")
	if order != "" && order != reading.PeriodicalIssuesNewest && order != reading.PeriodicalIssuesOldest {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	page, err := d.readingService().PeriodicalIssuesInMonth(r.Context(), p, library, series, kind, date, year, month, order, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		readingFailure(w, err)
		return
	}
	out := ReadingPeriodicalIssuesResponse{Month: month, Days: make([]ReadingPeriodicalDay, 0, len(page.Days)), Year: year, Years: make([]ReadingPeriodicalYear, 0, len(page.Years)), LibraryID: library, SeriesID: series, PublicationType: ReadingPeriodicalType(kind), IssueDate: ReadingIssueDate(date), Order: ReadingPeriodicalOrder(order), Items: make([]ReadingPeriodicalIssue, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, day := range page.Days {
		out.Days = append(out.Days, ReadingPeriodicalDay{Date: ReadingIssueDate(day.Date), IssueCount: day.IssueCount})
	}
	for _, bucket := range page.Years {
		out.Years = append(out.Years, ReadingPeriodicalYear{Year: bucket.Year, IssueCount: bucket.IssueCount})
	}
	for _, item := range page.Items {
		out.Items = append(out.Items, ReadingPeriodicalIssue{Item: readingItemProjection(item), IssueDate: ReadingIssueDate(item.IssueDate)})
	}
	if out.Items, err = readingBudget(out.Items, readingPageBytes, page.Truncate); err != nil {
		readingFailure(w, err)
		return
	}
	out.NextCursor = page.NextCursor
	write(w, 200, out)
}
func registerReadingPeriodicalIssuesHTTPContract(registry *apikit.Registry) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_reading_periodical_issues", Method: "GET", Path: "/v1/libraries/{libraryId}/reading/periodicals/{seriesId}/issues", Summary: "Read dated issues in one accepted typed Reading series", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "request_in_progress", "internal"}, Query: []string{"publicationType", "issueDate", "year", "month", "order", "cursor", "limit"}, QueryIntegers: emptyLimitQuery(50, 100)}
	return apikit.RegisterHTTP[struct{}, ReadingPeriodicalIssuesResponse](registry, m)
}
