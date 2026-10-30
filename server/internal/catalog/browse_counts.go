package catalog

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// A browse page asks three questions whose honest answers used to cost a pass
// over the whole library each: how many entities are there, where does each
// letter start, and where in the order does this one entity sit. Measured on a
// million rows, counting from the index is 114 ms, counting under a viewer's
// restriction is 3.9 seconds, and grouping the letters under a restriction is
// 6.3 seconds. Three of those on one request is the whole of the gap between
// what this server did and what it is for.
//
// `browse_entity_buckets` (internal/persistence/browse_rows.go) answers all
// three from one small read. Every entity is counted into a bucket keyed by the
// library, the pivot's entity kind, whether it is a container, the first
// character of its title, and the two keys the restriction predicate is a
// function of — the set of content ratings it carries and the set of labels.
// Reading the buckets for one (library, kind) in title order and deciding each
// one against the viewer's restriction gives:
//
//   - the exact total, as a sum;
//   - the exact letter index, as a prefix sum, in the same order `MIN(position)`
//     produced it;
//   - the starting position of any letter, which is what an anchor rank needs
//     before it counts the handful of rows inside one letter.
//
// The decision is made here, in Go, rather than in SQL, because it needs the
// rating table — and because a few hundred buckets decided in a loop is cheaper
// than any join. The answers are exact, not approximate: a bucket either
// entirely passes the restriction or entirely fails it, since every entity in it
// carries the same two keys.
//
// Containers are the exception. A show is visible when *some* episode of it is,
// and that is a property of the show's members rather than of the show, so a
// container bucket cannot be decided from its key. Container pivots therefore
// fall back to counting through the membership table — which is a real indexed
// table now, and there are thousands of shows where there are millions of
// episodes.

// browseBucket is one counted group.
type browseBucket struct {
	container bool
	head      string
	ratingKey string
	labelKey  string
	total     int
}

// browseSummary is what the buckets answered.
type browseSummary struct {
	// exact is false when the answer could not be decided from the buckets, in
	// which case the caller must count through SQL.
	exact  bool
	total  int
	letter []BrowsePositionAnchor
	// headStart is the absolute position at which each first character's run
	// begins, which is what an anchor rank uses to avoid ranking the whole set.
	headStart map[string]int
	// headSize is the number of visible rows in each head's run. A head that
	// appears in several bucket rows (the '#' case) accumulates them here,
	// while headStart keeps the position of its first run.
	headSize   map[string]int
	classKey   string
	classID    int64
	generation int64
}

// ratingAges is the classification table, read once per summary. It is the
// catalogue's distinct content-rating spellings, which is tens of rows.
type ratingAges map[string]int

func (s *Service) ratingAges() (ratingAges, error) {
	out := ratingAges{}
	rows, err := s.read().Query(`SELECT value_key,minimum_age FROM content_rating_ages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var age int
		if err = rows.Scan(&key, &age); err != nil {
			return nil, err
		}
		out[key] = age
	}
	return out, rows.Err()
}

// keySet splits a canonical key back into its members. An empty key is an empty
// set, not a set holding one empty string.
func keySet(key string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, "|")
}

// bucketVisible answers the restriction predicate for an item-backed bucket. It
// is the same three clauses `itemRestrictionClause` compiles, read off the two
// keys instead of probed out of the attribute table.
func bucketVisible(bucket browseBucket, ages ratingAges, r identity.ContentRestrictions, blocked map[string]bool) bool {
	ratings := keySet(bucket.ratingKey)
	if r.MaximumAge != nil {
		// "no content rating this server recognises admits only people older than
		// the ceiling" — an unrecognised spelling contributes nothing, exactly as
		// the join contributes no row for it.
		for _, key := range ratings {
			if age, known := ages[key]; known && age > *r.MaximumAge {
				return false
			}
		}
	}
	if r.BlockUnrated {
		rated := false
		for _, key := range ratings {
			if age, known := ages[key]; known && age >= 0 {
				rated = true
				break
			}
		}
		if !rated {
			return false
		}
	}
	if len(blocked) > 0 {
		for _, label := range keySet(bucket.labelKey) {
			if blocked[label] {
				return false
			}
		}
	}
	return true
}

func browseHeadLetter(head string) string { return sorttext.Letter(head) }

// browseSummarise reads the counting read model for one scope and folds it into
// a total, a letter index and the letter start positions.
//
// The buckets are read in title order — `ORDER BY head COLLATE NOCASE`, the same
// collation the page's ORDER BY uses — so the prefix sum walks the rows in the
// order the page would return them. That is what makes the positions the same
// numbers `ROW_NUMBER()` produced, including for '#', whose runs are not
// contiguous: its index is the position of its first run, which is the first
// time the loop meets a non-letter head.
func (s *Service) browseSummarise(library string, kinds []string, r identity.ContentRestrictions) (browseSummary, error) {
	if r.Active() {
		materialized, ready, err := s.visibilitySummary(library, kinds, r)
		if err != nil {
			return materialized, err
		}
		if ready {
			// A published class can lag a source edit while its refresh is
			// queued: it is the last published generation and serves; each page
			// row is still checked against current restrictions below.
			return materialized, nil
		}
		// Until a new class publishes, the item buckets still provide exact
		// counts for age and labels. Every page keeps its live restriction
		// predicate; never replace the reader's content with a preparation gap.

	}
	if r.Active() {
		ambiguous, err := s.ambiguousRestrictionKeys()
		if err != nil {
			return browseSummary{}, err
		}
		if ambiguous {
			return browseSummary{}, nil
		}
	}
	out := browseSummary{headStart: map[string]int{}, headSize: map[string]int{}}
	args := make([]any, 0, len(kinds)+1)
	args = append(args, library)
	for _, kind := range kinds {
		parsed, parseErr := compactcatalog.ParseKind(kind)
		if parseErr != nil {
			return out, parseErr
		}
		args = append(args, int(parsed))
	}
	rows, err := s.read().Query(`SELECT container,value,rating_key,label_key,total FROM catalog_browse_buckets`+
		` WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind IN (`+placeholders(len(kinds))+`) AND axis=0 AND total>0 ORDER BY value COLLATE NOCASE`, args...)
	if err != nil {
		return out, err
	}
	buckets := []browseBucket{}
	containers := false
	for rows.Next() {
		var bucket browseBucket
		if err = rows.Scan(&bucket.container, &bucket.head, &bucket.ratingKey, &bucket.labelKey, &bucket.total); err != nil {
			rows.Close()
			return out, err
		}
		containers = containers || bucket.container
		buckets = append(buckets, bucket)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	active := r.Active()
	if len(r.MemberDeniedLabels) > 0 {
		// A cold tiny class uses the authoritative SQL path until the background
		// generation publishes. The former full-tag-set movie buckets are gone.
		return out, nil
	}
	if active && containers {
		// A container's visibility is a fact about its members. Counting it needs
		// the membership join, so this scope is not answerable from the buckets.
		return out, nil
	}
	var ages ratingAges
	blocked := map[string]bool{}
	if active {
		if ages, err = s.ratingAges(); err != nil {
			return out, err
		}
		for _, label := range r.BlockedLabels {
			blocked[strings.ToLower(label)] = true
		}
	}
	position := 0
	seen := map[string]bool{}
	index := 0
	for index < len(buckets) {
		head := buckets[index].head
		// All the buckets sharing a first character form one contiguous run of the
		// page's ordering, whatever the secondary sorts inside it are.
		run := 0
		for index < len(buckets) && buckets[index].head == head {
			if !active || bucketVisible(buckets[index], ages, r, blocked) {
				run += buckets[index].total
			}
			index++
		}
		if run == 0 {
			continue
		}
		if _, known := out.headStart[head]; !known {
			out.headStart[head] = position
		}
		out.headSize[head] += run
		letter := browseHeadLetter(head)
		if !seen[letter] {
			seen[letter] = true
			out.letter = append(out.letter, BrowsePositionAnchor{Key: letter, Index: position})
		}
		position += run
	}
	out.total = position
	out.exact = true
	return out, nil
}

// BrowseCountBatch is one bounded, read-only relax-suggestion request.
const BrowseCountMaximum = 8

type BrowseCountQuery struct {
	ID    string      `json:"id"`
	Query *BrowseNode `json:"query"`
}
type BrowseCountRequest struct {
	Pivot   string             `json:"pivot"`
	Queries []BrowseCountQuery `json:"queries"`
}
type BrowseCount struct {
	ID                string `json:"id"`
	Total             int    `json:"total"`
	TotalIsLowerBound bool   `json:"totalIsLowerBound"`
	Revision          string `json:"revision"`
}
type BrowseCountBatch struct {
	Counts []BrowseCount `json:"counts"`
}

func (s *Service) BrowseCounts(viewer Viewer, library string, request BrowseCountRequest, sources map[string]HomeRowMembership) (BrowseCountBatch, error) {
	out := BrowseCountBatch{Counts: []BrowseCount{}}
	if len(request.Queries) < 1 || len(request.Queries) > BrowseCountMaximum {
		return out, browseIssue("queries", "send between 1 and 8 count queries")
	}
	if dbwork.Snapshot(s.Context()) == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).BrowseCounts(viewer, library, request, sources)
			return err
		})
		return out, err
	}
	seen := map[string]bool{}
	for _, query := range request.Queries {
		if query.ID == "" || len(query.ID) > 128 || seen[query.ID] {
			return out, browseIssue("queries.id", "each count needs a unique bounded id")
		}
		seen[query.ID] = true
		// Direct service callers have the same grammar and bounds as HTTP callers.
		if err := validateMeasureNode(query.Query); err != nil {
			return out, err
		}
		if library == "" && acrossAnchor(query.Query) == nil {
			return out, ErrBrowseAcrossGroup
		}
		scope, args, revision, err := s.browseMeasureScope(viewer, FacetRequest{Library: library, Pivot: request.Pivot, Query: query.Query, RowSources: sources})
		if err != nil {
			return out, err
		}
		total, hit := s.cachedBrowseTotal("count:" + revision)
		if !hit {
			if err = s.read().QueryRow(`SELECT count(*) FROM (`+scope+` LIMIT ?)`, append(args, browseTotalThreshold+1)...).Scan(&total); err != nil {
				return out, err
			}
			s.storeBrowseTotal("count:"+revision, total)
		}
		out.Counts = append(out.Counts, BrowseCount{ID: query.ID, Total: min(total, browseTotalThreshold), TotalIsLowerBound: total > browseTotalThreshold, Revision: revision})
	}
	return out, nil
}
func validateMeasureNode(node *BrowseNode) error {
	if node == nil {
		return nil
	}
	raw, err := node.MarshalJSON()
	if err != nil {
		return requestinput.New("invalid query")
	}
	_, err = ParseBrowseQuery(raw, "query")
	return err
}

// browseQueryDuration sums each visible playable member once, using the
// maintained longest-file metric (seconds). Missing duration or a capped member
// set is explicitly a lower bound. No page or source length limits are imposed.
func (s *Service) browseQueryDuration(viewer Viewer, request FacetRequest) (int64, bool, error) {
	scope, args, revision, err := s.browseMeasureScope(viewer, request)
	if err != nil {
		return 0, false, err
	}
	key := "duration:" + revision + ":" + RestrictionFence(viewer.Restrictions)

	if cached, hit := s.state.facetCache.get(key, 0); hit {
		return cached.durationMs, cached.durationLower, nil
	}
	aggregate, err := s.state.facetCache.do(key, func() (FacetPage, error) {
		if cached, hit := s.state.facetCache.get(key, 0); hit {
			return cached, nil
		}
		visible, va := viewer.itemVisibilitySQL("member.item_id")
		// One bounded member stream supplies the sum, the missing-metric flag and
		// the sentinel. Keep integer milliseconds intact on 32-bit Go targets too.
		query := `WITH targets AS (` + scope + `), members AS MATERIALIZED (SELECT DISTINCT member.item_id FROM targets target CROSS JOIN catalog_browse_memberships member ON member.entity_id=target.entity_id WHERE ` + visible + ` LIMIT ` + fmt.Sprint(browseTotalThreshold+1) + `)
 SELECT COALESCE(CAST(round(sum(CASE WHEN n<=` + fmt.Sprint(browseTotalThreshold) + ` AND duration>0 THEN duration ELSE 0 END)*1000) AS INTEGER),0),count(*),COALESCE(sum(n<=` + fmt.Sprint(browseTotalThreshold) + ` AND (duration IS NULL OR duration<=0)),0)
 FROM (SELECT metric.duration,row_number() OVER() AS n FROM members LEFT JOIN catalog_browse_item_metrics metric ON metric.item_id=members.item_id)`
		var duration int64
		var count, missing int
		if err := s.read().QueryRow(query, append(args, va...)...).Scan(&duration, &count, &missing); err != nil {
			return FacetPage{}, err
		}
		out := FacetPage{durationMs: duration, durationLower: count > browseTotalThreshold || missing > 0}
		s.state.facetCache.put(key, facetCacheEntry{revision: 0, publications: dbwork.Publications(), expires: time.Now().Add(time.Minute), page: out})
		return out, nil
	})
	return aggregate.durationMs, aggregate.durationLower, err
}
