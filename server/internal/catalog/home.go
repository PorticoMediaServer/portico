package catalog

import (
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type HomeRequest struct {
	ReadingPersonalAuthorize       func(*sql.Tx, string) error
	Viewer                         Viewer
	ServerID, Profile, ViewerFence string
	// PrivacyFence is ViewerFence without the library list (see the httpapi Home scope).
	PrivacyFence      string
	Libraries         []string
	RowOrder          []string
	HiddenRowIDs      []string
	LayoutRevision    int64
	CommunityActivity bool
	Limit             int
	// Rows, when set, stops composing once this many rows with entries are
	// composed (the Top Shelf feed shows four): the rest are never read.
	Rows          int
	LiveAvailable bool
	OnNow         func(HomeRowPage) (HomeRow, error)
	Now           time.Time
	// Restrictions is applied to every row by homeSource; see restrictions.go.
	Restrictions identity.ContentRestrictions
	// visibleShows, when set, is a first test of a started show (w.show_id)
	// against the published visibility classes (publishedClassesSQL).
	visibleShows string
}

func (r HomeRequest) scoped() HomeRequest {
	r.Profile, r.ViewerFence, r.Libraries, r.Restrictions = r.Viewer.Profile, r.Viewer.Fence, r.Viewer.Libraries, r.Viewer.EffectiveRestrictions()
	return r
}

type homeCandidate struct{ id, order string }

// HomeRevision is the catalogue and viewer revision the home surface is
// composed against. It is exported so the HTTP layer can fold it into a response
// validator and answer a conditional request before composing anything.
func (s *Service) HomeRevision(libraries []string, profile string) (ContentRevision, error) {
	return s.homeRevision(homeUnique(libraries), profile)
}

func (s *Service) homeRevision(libraries []string, profile string) (ContentRevision, error) {
	raw, _ := json.Marshal(libraries)
	var revision ContentRevision
	e := s.read().QueryRow(`SELECT COALESCE(sum(r.revision),0)+(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1),COALESCE(sum(v.revision),0) FROM library_revisions r LEFT JOIN viewer_revisions v ON v.library_id=r.library_id AND v.profile_id=? WHERE r.library_id IN(SELECT value FROM json_each(?))`, profile, string(raw)).Scan(&revision.Catalog, &revision.Viewer)
	return revision, e
}

// homePagingRevision fences a Home row's pages. Like Browse it follows the
// order revision (membership, order and availability) and the viewer's own
// revision, so the artwork, extras, analysis and other decoration that
// background work writes all day never strands a row's next page. Rows built
// from a computed list are fenced by that list's fingerprint as well.
func (s *Service) homePagingRevision(libraries []string, profile string) (ContentRevision, error) {
	raw, _ := json.Marshal(libraries)
	var revision ContentRevision
	e := s.read().QueryRow(`SELECT COALESCE(sum(r.order_revision),0)+(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1),COALESCE(sum(v.revision),0) FROM library_revisions r LEFT JOIN viewer_revisions v ON v.library_id=r.library_id AND v.profile_id=? WHERE r.library_id IN(SELECT value FROM json_each(?))`, profile, string(raw)).Scan(&revision.Catalog, &revision.Viewer)
	return revision, e
}

func mergeHome(a, b []homeCandidate, limit int) []homeCandidate {
	a = append(a, b...)
	sort.Slice(a, func(i, j int) bool {
		if a[i].order != a[j].order {
			return a[i].order > a[j].order
		}
		return a[i].id > a[j].id
	})
	if len(a) > limit {
		a = a[:limit]
	}
	return a
}
func (s *Service) homeCandidates(query string, args ...any) ([]homeCandidate, error) {
	rows, e := s.read().Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []homeCandidate{}
	for rows.Next() {
		var row homeCandidate
		if e = rows.Scan(&row.id, &row.order); e != nil {
			return nil, e
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
