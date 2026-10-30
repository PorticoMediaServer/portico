package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestTitleCursorSeeksSortIndex(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	library := c.Library("movies", "Movies", "movie", "/movies")
	for i, title := range []string{"Alpha", "Bravo", "Bravo", "Charlie", "Delta", "Echo"} {
		c.Movie(library, fmt.Sprintf("/movies/%d.mkv", i), title, 2000+i/2)
	}
	c.Drain()
	var seq int
	var name, path string
	if err := f.deps.DB.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	var statement string
	captured := sql.OpenDB(titleQueryConnector{driver: f.deps.DB.Driver(), path: path, statement: &statement})
	captured.SetMaxOpenConns(1)
	t.Cleanup(func() { captured.Close() })
	service := catalog.New(captured)
	viewer := catalog.Viewer{Profile: "owner-profile", Libraries: []string{"movies"}, Fence: "owner"}
	for _, sort := range []string{"title", "year", "added"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sort+"/"+order, func(t *testing.T) {
				query := catalog.TitleQuery{Library: "movies", Kind: "movie", Sort: sort, Order: order, Limit: 2}
				seen := map[string]bool{}
				for {
					statement = ""
					page, err := service.QueryTitles(viewer, query)
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range page.Items {
						if seen[item.ID] {
							t.Fatalf("cursor repeated title %s", item.ID)
						}
						seen[item.ID] = true
					}
					if query.Cursor != "" {
						found := false
						for _, sql := range []string{statement} {
							if !strings.Contains(sql, "FROM catalog_browse_rows_serving e JOIN catalog_entities ce") {
								continue
							}
							found = true
							args := make([]any, strings.Count(sql, "?"))
							rows, err := f.deps.DB.Query("EXPLAIN QUERY PLAN "+sql, args...)
							if err != nil {
								t.Fatal(err)
							}
							// The page is read from the serving rows: the current
							// generation and, during a rebuild, the one it replaces.
							// Each is entered twice through its order index, at the
							// cursor's key after its id and at the next key, and each
							// run is limited, so a page costs its own rows however
							// large the library or the cursor's tie cohort. Only the
							// two bounded runs are merged; the serving view is never
							// materialized or scanned.
							tie, retainedTie, seek, retained, scanned := false, false, false, false, false
							for rows.Next() {
								var id, parent, unused int
								var detail string
								if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
									rows.Close()
									t.Fatal(err)
								}
								t.Log(detail)
								tied := strings.Contains(detail, "=? AND entity_id>?)") || strings.Contains(detail, "=? AND entity_id<?)")
								ranged := strings.Contains(detail, "kind=? AND") && (strings.HasSuffix(detail, ">?)") || strings.HasSuffix(detail, "<?)")) && !tied
								if strings.Contains(detail, "SEARCH r USING INDEX catalog_browse_") {
									tie, seek = tie || tied, seek || ranged
								}
								if strings.Contains(detail, "SEARCH p USING INDEX catalog_browse_") && strings.Contains(detail, "_previous") {
									retainedTie, retained = retainedTie || tied, retained || ranged
								}
								if strings.Contains(detail, "MATERIALIZE catalog_browse_rows_serving") || strings.Contains(detail, "AUTOMATIC") || servingScan.MatchString(detail) {
									scanned = true
								}
							}
							err = rows.Err()
							rows.Close()
							if err != nil {
								t.Fatal(err)
							}
							if !tie || !retainedTie || !seek || !retained {
								t.Error("cursor query does not seek its tie and its later keys in both generations")
							}
							if scanned {
								t.Error("cursor query scans or materializes the serving rows instead of reading two bounded runs")
							}
						}
						if !found {
							t.Fatal("title query was not traced")
						}
					}
					if page.NextCursor == "" {
						break
					}
					query.Cursor = page.NextCursor
				}
				if len(seen) != 6 {
					t.Fatalf("walk returned %d titles, want 6", len(seen))
				}
			})
		}
	}
}

var servingScan = regexp.MustCompile(`^SCAN (r|p|e)( |$)`)

// Capture the full statement at the SQL boundary. The general diagnostic trace
// intentionally truncates SQL, which cannot establish an index seek's plan.
type titleQueryConnector struct {
	driver    driver.Driver
	path      string
	statement *string
}

func (c titleQueryConnector) Driver() driver.Driver { return c.driver }
func (c titleQueryConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.driver.Open(c.path)
	if err != nil {
		return nil, err
	}
	return &titleQueryCapture{Conn: conn, statement: c.statement}, nil
}

type titleQueryCapture struct {
	driver.Conn
	statement *string
}

func (c *titleQueryCapture) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "FROM catalog_browse_rows_serving e JOIN catalog_entities ce") {
		*c.statement = query
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}
