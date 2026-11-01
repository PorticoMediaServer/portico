package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// HomeRowMembership is an authorized server-side relation, not a whole-library
// list in Go memory. Only catalog can construct its SQL and bind parameters.
// Browse applies its own current viewer predicates as well as this Home scope.
type HomeRowMembership struct {
	query string // (id INTEGER, pos INTEGER)
	args  []any
	fence string
}

// ResolveHomeRowMembership resolves membership and order in the caller's read
// snapshot. Its revision participates in every browse cursor; a changed source
// requires an explicit restart rather than mixing two row orders.
func (s *Service) ResolveHomeRowMembership(r HomeRequest, id string) (HomeRowMembership, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out HomeRowMembership
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).ResolveHomeRowMembership(r, id)
			return err
		})
		return out, err
	}
	r = r.scoped()
	r.Libraries = homeUnique(r.Libraries)
	r.Now = s.recommendationNow(r.Now)
	if err := s.prepareViewer(r.Viewer); err != nil {
		return HomeRowMembership{}, err
	}
	specs, err := s.homeSpecs(r)
	if err != nil {
		return HomeRowMembership{}, err
	}
	s = s.WithRecommendationRestrictions(r.Restrictions)
	// As HomeSingleRow: this request's own source memo and composition.
	s.recSources = map[string]homeSource{}
	s.recComposition = newRecCompositionSources(s.Context())
	var spec *homeRowSpec
	for i := range specs {
		if specs[i].ID == id && specs[i].PolicyState == "available" {
			spec = &specs[i]
			break
		}
	}
	if strings.HasPrefix(id, recFamily+":") && !hidesFamily(r.HiddenRowIDs) {
		row, e := s.recPersonalRow(r, id)
		if e != nil {
			return HomeRowMembership{}, e
		}
		// A personal row's titles are the row the generator ranked, as the
		// row's own page serves them (HomeRow seeds the same source): the
		// engine has no source of its own for these rows.
		s.recSources[id+":"+idsJSON(r.Libraries)] = candidateSource(row.items)
		spec = &row.spec
	}
	if spec == nil || id == recFamily || id == "on_now" || id == "featured_collection" {
		return HomeRowMembership{}, ErrHomeRowUnknown
	}
	source, err := s.homeSource(r, *spec)
	if err != nil {
		return HomeRowMembership{}, err
	}
	revision, err := s.homePagingRevision(r.Libraries, r.Profile)
	if err != nil {
		return HomeRowMembership{}, err
	}
	order, _ := homeOrder(spec.Direction)
	return HomeRowMembership{
		query: `SELECT e.id,ROW_NUMBER() OVER(ORDER BY candidate.ord ` + order + `,candidate.id ` + order + `)-1 AS pos FROM (` + source.base + `) candidate CROSS JOIN catalog_entities e ON e.public_id=pid_blob(candidate.id)`,
		args:  source.args,
		fence: homeMembershipFence(revision, source.fingerprint),
	}, nil
}

// homeRecentPagedSource reads maintained work rows without a source-length
// ceiling. SQL performs edition grouping; each request hydrates only its page.
// Availability and authorization must hold on the same current member.
func (s *Service) homeRecentPagedSource(r HomeRequest, audio bool) (homeSource, error) {
	kinds := "i.kind IN(1,2) AND l.kind IN('movie','tv','anime')"
	if audio {
		kinds = "i.kind IN(6,8) AND l.kind IN('music','audiobook')"
	}
	visible, args := r.Viewer.itemVisibilitySQL("member.item_id")
	metricVisible, metricArgs := r.Viewer.itemVisibilitySQL("metric.item_id")
	ord := `COALESCE((SELECT max(metric.added) FROM catalog_browse_member_metrics metric
 CROSS JOIN catalog_item_availability available ON available.entity_id=metric.item_id
 WHERE metric.entity_id=i.id AND available.available=1 AND available.retired=0 AND ` + metricVisible + recordingsClause("metric.item_id", r.Restrictions) + `),'')`
	works := `
 FROM catalog_browse_rows br CROSS JOIN catalog_entities i ON i.id=br.entity_id
 JOIN catalog_libraries cl ON cl.id=i.library_id JOIN libraries l ON l.id=cl.library_id
 WHERE i.retired=0 AND cl.retired=0 AND cl.library_id IN(SELECT value FROM json_each(?)) AND ` + kinds + `
 AND EXISTS(SELECT 1 FROM catalog_browse_memberships member CROSS JOIN catalog_item_availability available ON available.entity_id=member.item_id
 WHERE member.entity_id=i.id AND available.available=1 AND available.retired=0 AND ` + visible + recordingsClause("member.item_id", r.Restrictions) + `)
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id)`
	key := homeRecentWorkKeySQL("i") + ` AS work`
	bind := append(append(append([]any{}, metricArgs...), idsJSON(r.Libraries)), args...)
	return homeSource{
		base: `SELECT min(id) AS id,max(ord) AS ord FROM (SELECT pid(i.public_id) AS id,` + ord + ` AS ord,` + key + works + `) GROUP BY work`,
		args: bind,
		// The row's total is its number of works, which needs no order values.
		count:          `SELECT count(*) FROM (SELECT 1 FROM (SELECT ` + key + works + `) GROUP BY work)`,
		countArgs:      append([]any{idsJSON(r.Libraries)}, args...),
		selfRestricted: true,
		walk:           s.homeRecentWalker(r, ord, key, works, metricArgs, append([]any{idsJSON(r.Libraries)}, args...)),
	}, nil
}

// HomeDisplayTarget relates a stable work membership to the real episode or
// season card served by Home. Normal generic title browse keeps its canonical
// work IDs. A Home-origin See all explicitly requests this projection instead.
type HomeDisplayTarget struct {
	MembershipID string `json:"membershipId"`
	EntryID      string `json:"entryId"`
}
type HomeProjectedEntry struct {
	MembershipID string
	Entry        ContentEntry
}

// ProjectHomeRowDisplayTargets is the bounded, current-authority seam for lane2.03b.
// The caller passes one canonical browse page, not a full source or a snapshot
// of client IDs. Every ID is checked against the current row membership first.
// Returned target actions must be recomposed for the actual target; this method
// never copies a work's command offers to a different episode or season.
func (s *Service) ProjectHomeRowDisplayTargets(r HomeRequest, rowID string, membershipIDs []string) ([]HomeProjectedEntry, error) {
	if len(membershipIDs) > HomeRowMaxLimit {
		return nil, ErrCursor
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var out []HomeProjectedEntry
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).ProjectHomeRowDisplayTargets(r, rowID, membershipIDs)
			return err
		})
		return out, err
	}
	r = r.scoped()
	r.Now = s.recommendationNow(r.Now)
	source, err := s.ResolveHomeRowMembership(r, rowID)
	if err != nil {
		return nil, err
	}
	rows, err := s.read().Query(`SELECT pid(e.public_id) FROM (`+source.query+`) member JOIN catalog_entities e ON e.id=member.id WHERE e.public_id IN(SELECT pid_blob(value) FROM json_each(?))`, append(append([]any{}, source.args...), idsJSON(membershipIDs))...)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		allowed[id] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range membershipIDs {
		if !allowed[id] {
			return nil, ErrStaleContinuation
		}
	}
	targets := membershipIDs
	if rowID == "new_on_server" {
		targets, err = s.homeRecentPageTargets(r, membershipIDs)
		if err != nil {
			return nil, err
		}
	}
	scoped := s.WithRecommendationRestrictions(r.Restrictions)
	entries, err := scoped.homeEngineEntries(r.Profile, targets)
	if err != nil {
		return nil, err
	}
	if err = scoped.homeEnrich(r.Profile, entries); err != nil {
		return nil, err
	}
	out := make([]HomeProjectedEntry, len(entries))
	for i, entry := range entries {
		out[i] = HomeProjectedEntry{MembershipID: membershipIDs[i], Entry: entry}
	}
	return out, nil
}

// homeMembershipFence is a See all's fence: a computed list's fingerprint and the
// viewer, or for a keyset row the order and viewer revisions.
func homeMembershipFence(revision ContentRevision, fingerprint string) string {
	if fingerprint != "" {
		return fmt.Sprintf("list:%d:%s", revision.Viewer, fingerprint)
	}
	return fmt.Sprintf("%d:%d:", revision.Catalog, revision.Viewer)
}
