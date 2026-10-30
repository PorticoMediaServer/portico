package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/readingpaths"
	"reflect"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

type ReadingPeriodicalGroup struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	IssueCount int64                  `json:"issueCount"`
	Latest     ReadingPeriodicalIssue `json:"latest"`
}

func (ReadingPeriodicalGroup) JSONSchema() map[string]any {
	type wire ReadingPeriodicalGroup
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["id"].(map[string]any)["pattern"] = "^[A-Za-z0-9_-]{43}$"
	props["name"].(map[string]any)["minLength"] = 1
	props["name"].(map[string]any)["maxLength"] = 4096
	props["issueCount"].(map[string]any)["minimum"] = 1
	props["issueCount"].(map[string]any)["maximum"] = 9007199254740991
	return shape
}

type ReadingPeriodicalGroupsResponse struct {
	LibraryID       string                   `json:"libraryId"`
	PublicationType ReadingPeriodicalType    `json:"publicationType"`
	Groups          []ReadingPeriodicalGroup `json:"groups"`
	NextCursor      string                   `json:"nextCursor,omitempty"`
}

func (ReadingPeriodicalGroupsResponse) JSONSchema() map[string]any {
	type wire ReadingPeriodicalGroupsResponse
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	props := shape["properties"].(map[string]any)
	props["groups"].(map[string]any)["maxItems"] = 100
	props["nextCursor"].(map[string]any)["pattern"] = "^[A-Za-z0-9_-]{171}\\.[A-Za-z0-9_-]{43}$"
	return shape
}
func (d Dependencies) readingPeriodicalGroups(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	limit := 24
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			readingFailure(w, reading.ErrInvalid)
			return
		}
	}
	library, kind := r.PathValue("libraryId"), r.URL.Query().Get("publicationType")
	page, err := d.readingService().PeriodicalGroups(r.Context(), p, library, kind, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		readingFailure(w, err)
		return
	}
	out := ReadingPeriodicalGroupsResponse{LibraryID: library, PublicationType: ReadingPeriodicalType(kind), Groups: make([]ReadingPeriodicalGroup, 0, len(page.Groups)), NextCursor: page.NextCursor}
	for _, group := range page.Groups {
		out.Groups = append(out.Groups, ReadingPeriodicalGroup{ID: group.ID, Name: group.Name, IssueCount: group.IssueCount, Latest: ReadingPeriodicalIssue{Item: readingItemProjection(group.Latest), IssueDate: ReadingIssueDate(group.Latest.IssueDate)}})
	}
	if out.Groups, err = readingBudget(out.Groups, readingPageBytes, page.Truncate); err != nil {
		readingFailure(w, err)
		return
	}
	out.NextCursor = page.NextCursor
	write(w, 200, out)
}
func registerReadingPeriodicalGroupsHTTPContract(registry *apikit.Registry) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_reading_periodical_groups", Method: "GET", Path: readingpaths.PeriodicalGroups, Summary: "Read accepted typed and dated Reading periodical groups", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "request_in_progress", "internal"}, Query: []string{"publicationType", "cursor", "limit"}, QueryIntegers: emptyLimitQuery(24, 100)}
	return apikit.RegisterHTTP[struct{}, ReadingPeriodicalGroupsResponse](registry, m)
}
