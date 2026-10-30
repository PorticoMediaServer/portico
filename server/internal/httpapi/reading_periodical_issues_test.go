package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// readingIssueMember is one actual admitted dated issue: its public item ID, its
// accepted issue date and the internal entity ID that completes the ordering
// tie-break.
type readingIssueMember struct {
	ID     string
	Date   string
	Entity int64
}

// newReadingIssuesFixture publishes one accepted typed series through the real
// scan and attribution projection, so ordering tests page actual membership.
func newReadingIssuesFixture(t *testing.T, kind string, dates []string) (*readingFixture, http.Handler, string, []readingIssueMember) {
	t.Helper()
	r := newReadingFixture(t, "book", "pdf")
	name := "Dated " + kind + " archive"
	paths := make([]string, 0, len(dates))
	for index, date := range dates {
		// One delimited date per file name, so a repeated date is an actual
		// same-day edition rather than a second parsed date.
		paths = append(paths, readingTreeFile(t, r, name+"/Issue "+date+" "+strconv.Itoa(index)+".epub"))
	}
	readingScan(t, r.f, r.library)
	for _, path := range paths {
		item := readingPathItem(t, r, path)
		readingSeriesOwnerField(t, r, item, "series", name)
		readingSeriesOwnerField(t, r, item, "publicationType", kind)
	}
	readingSeriesDrain(t, r)
	series := compactcatalog.ReadingSeriesID(r.library, name)
	rows, err := r.f.db.Query(`SELECT pid(e.public_id),e.id,m.issue_date FROM reading_attribution_members m JOIN catalog_entities e ON e.id=m.item_id WHERE m.library_id=(SELECT cl.id FROM catalog_libraries cl WHERE cl.library_id=?) AND m.series_id=? ORDER BY m.issue_date,e.id`, r.library, series)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	members := make([]readingIssueMember, 0, len(dates))
	for rows.Next() {
		var member readingIssueMember
		if err = rows.Scan(&member.ID, &member.Entity, &member.Date); err != nil {
			t.Fatal(err)
		}
		members = append(members, member)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(members) != len(dates) {
		t.Fatalf("dated membership = %d, want %d: %+v", len(members), len(dates), members)
	}
	return r, New(r.d), "/v1/libraries/" + r.library + "/reading/periodicals/" + series + "/issues", members
}

// readingIssuesExpected is the total order over the same date/item tuple this
// route publishes: issue date first, then the actual item ID, reversed with the
// requested direction.
func readingIssuesExpected(members []readingIssueMember, oldest bool) []readingIssueMember {
	out := append([]readingIssueMember{}, members...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			if oldest {
				return out[i].Date < out[j].Date
			}
			return out[i].Date > out[j].Date
		}
		if oldest {
			return out[i].Entity < out[j].Entity
		}
		return out[i].Entity > out[j].Entity
	})
	return out
}

func readingIssuesRequest(t *testing.T, h http.Handler, token, base string, query url.Values) (*httptest.ResponseRecorder, ReadingPeriodicalIssuesResponse) {
	t.Helper()
	w := readingHTTP(t, h, "GET", base+"?"+query.Encode(), token, "", nil)
	var page ReadingPeriodicalIssuesResponse
	if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &page) != nil {
		t.Fatal("periodical issues response is not the declared shape", w.Body.String())
	}
	return w, page
}

func readingIssuesQuery(order, cursor string, limit int) url.Values {
	query := url.Values{"publicationType": {"magazine"}}
	if order != "" {
		query.Set("order", order)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return query
}

// TestReadingPeriodicalIssuesDefaultNewestFirst holds the unchanged default:
// issue date descending, item ID descending, with an opaque continuation.
func TestReadingPeriodicalIssuesDefaultNewestFirst(t *testing.T) {
	r, h, base, members := newReadingIssuesFixture(t, "magazine", []string{"1929-11", "1929-12", "1930-01"})
	token := r.f.owner.AccessToken
	w, page := readingIssuesRequest(t, h, token, base, readingIssuesQuery("", "", 0))
	if w.Code != 200 || len(page.Items) != len(members) || page.NextCursor != "" {
		t.Fatal("default issue page", w.Code, w.Body.String())
	}
	if page.Order != "" {
		t.Fatal("default request echoed an order", w.Body.String())
	}
	want := readingIssuesExpected(members, false)
	for index, item := range page.Items {
		if item.Item.ID != want[index].ID || string(item.IssueDate) != want[index].Date {
			t.Fatalf("default order %d = %s %s, want %s %s", index, item.Item.ID, item.IssueDate, want[index].ID, want[index].Date)
		}
	}
	first, head := readingIssuesRequest(t, h, token, base, readingIssuesQuery("", "", 2))
	if first.Code != 200 || len(head.Items) != 2 || head.NextCursor == "" || head.Order != "" {
		t.Fatal("first default page", first.Code, first.Body.String())
	}
	second, tail := readingIssuesRequest(t, h, token, base, readingIssuesQuery("", head.NextCursor, 2))
	if second.Code != 200 || len(tail.Items) != 1 || tail.NextCursor != "" || tail.Items[0].Item.ID != want[2].ID || string(tail.Items[0].IssueDate) != want[2].Date {
		t.Fatal("continued default page", second.Code, second.Body.String())
	}
}

// TestReadingPeriodicalIssuesOldestOrderPaging walks the ascending order across
// pages: actual issue date ascending with the same item ID tie-break reversed,
// and no duplicate or missing member.
func TestReadingPeriodicalIssuesOldestOrderPaging(t *testing.T) {
	r, h, base, members := newReadingIssuesFixture(t, "magazine", []string{"1929-11", "1929-12", "1929-12", "1930-01"})
	token := r.f.owner.AccessToken
	_, newest := readingIssuesRequest(t, h, token, base, readingIssuesQuery("newest", "", 0))
	want := readingIssuesExpected(members, true)
	var walked []ReadingPeriodicalIssue
	cursor := ""
	for page := 0; ; page++ {
		if page > len(members) {
			t.Fatal("oldest paging did not terminate")
		}
		w, out := readingIssuesRequest(t, h, token, base, readingIssuesQuery("oldest", cursor, 1))
		if w.Code != 200 || out.Order != "oldest" || len(out.Items) != 1 {
			t.Fatal("oldest issue page", page, w.Code, w.Body.String())
		}
		walked = append(walked, out.Items[0])
		cursor = out.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(walked) != len(want) {
		t.Fatalf("oldest walk = %d items, want %d", len(walked), len(want))
	}
	for index, item := range walked {
		if item.Item.ID != want[index].ID || string(item.IssueDate) != want[index].Date {
			t.Fatalf("oldest order %d = %s %s, want %s %s", index, item.Item.ID, item.IssueDate, want[index].ID, want[index].Date)
		}
	}
	// Both directions publish the same complete membership, exactly reversed.
	if len(newest.Items) != len(walked) {
		t.Fatalf("newest/oldest membership differs: %d/%d", len(newest.Items), len(walked))
	}
	for index := range walked {
		if newest.Items[len(walked)-1-index].Item.ID != walked[index].Item.ID {
			t.Fatal("oldest order is not the reverse of the default order")
		}
	}
}

// TestReadingPeriodicalIssuesCursorBoundToOrder refuses a continuation produced
// under the other order, while an explicit newest request keeps the default
// cursor domain valid.
func TestReadingPeriodicalIssuesCursorBoundToOrder(t *testing.T) {
	r, h, base, _ := newReadingIssuesFixture(t, "magazine", []string{"1929-11", "1929-12", "1930-01"})
	token := r.f.owner.AccessToken
	_, newest := readingIssuesRequest(t, h, token, base, readingIssuesQuery("", "", 2))
	if newest.NextCursor == "" {
		t.Fatal("newest page produced no continuation")
	}
	_, oldest := readingIssuesRequest(t, h, token, base, readingIssuesQuery("oldest", "", 2))
	if oldest.NextCursor == "" {
		t.Fatal("oldest page produced no continuation")
	}
	for _, attempt := range []struct {
		order, cursor string
	}{
		{"oldest", newest.NextCursor},
		{"newest", oldest.NextCursor},
		{"", oldest.NextCursor},
	} {
		w, page := readingIssuesRequest(t, h, token, base, readingIssuesQuery(attempt.order, attempt.cursor, 2))
		if w.Code != http.StatusPreconditionFailed || bodyErrorCode(w.Body.String()) != "revision_mismatch" || len(page.Items) != 0 {
			t.Fatalf("order %q reused the other order's continuation: %d %s", attempt.order, w.Code, w.Body.String())
		}
	}
	for _, order := range []string{"", "newest"} {
		w, page := readingIssuesRequest(t, h, token, base, readingIssuesQuery(order, newest.NextCursor, 2))
		if w.Code != 200 || len(page.Items) != 1 {
			t.Fatalf("newest continuation under order %q: %d %s", order, w.Code, w.Body.String())
		}
	}
}

// TestReadingPeriodicalIssuesOrderInvalidValue rejects any other direction with
// the route's existing invalid-request error.
func TestReadingPeriodicalIssuesOrderInvalidValue(t *testing.T) {
	r, h, base, _ := newReadingIssuesFixture(t, "magazine", []string{"1929-11", "1929-12"})
	token := r.f.owner.AccessToken
	for _, raw := range []string{"sideways", "Oldest", "NEWEST", "newest,oldest", " newest"} {
		query := readingIssuesQuery("", "", 0)
		query.Set("order", raw)
		w, _ := readingIssuesRequest(t, h, token, base, query)
		if w.Code != 400 || bodyErrorCode(w.Body.String()) != "invalid_request" {
			t.Fatalf("order %q: %d %s", raw, w.Code, w.Body.String())
		}
	}
	query := readingIssuesQuery("", "", 0)
	query["order"] = []string{"newest", "oldest"}
	if w, _ := readingIssuesRequest(t, h, token, base, query); w.Code != 400 || bodyErrorCode(w.Body.String()) != "invalid_request" {
		t.Fatalf("repeated order: %d %s", w.Code, w.Body.String())
	}
}
