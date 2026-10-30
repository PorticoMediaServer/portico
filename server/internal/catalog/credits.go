package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"strconv"
	"time"
)

// A title's credits are complete, never cut off. The detail response carries
// what a detail screen shows first — the cast's first page by billing and the
// key crew (directing, writing, creator, music, producing) — with each group's
// total; the rest of either group pages from ItemCredits by cursor, in billing
// order. A title's credits are its own list (a few hundred at most in practice,
// a few thousand for the largest productions), so an offset into it costs no
// more than the title.

// creditFirstCast is the cast shown before a client asks for more.
const creditFirstCast = 24

// creditKeyCrew is how many distinct key crew people the detail carries.
const creditKeyCrew = 8

// creditPageMax bounds one page of ItemCredits; the cursor continues.
const creditPageMax = 200

// CreditTotals counts a title's cast and crew.
type CreditTotals struct {
	Cast int `json:"cast"`
	Crew int `json:"crew"`
}

// CreditPage is one page of a title's cast or crew.
type CreditPage struct {
	Revision   ContentRevision `json:"revision"`
	Group      string          `json:"group"`
	Credits    []Credit        `json:"credits"`
	Total      int             `json:"total"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// A credit is cast when its department says so; everything else is crew.
const creditIsCast = `lower(d.label) IN('acting','cast')`

// creditCrewRank orders key crew as a detail screen names them; -1 is not key.
const creditCrewRank = `CASE WHEN lower(trim(d.label))='directing' OR lower(r.label) LIKE '%director%' THEN 0
 WHEN lower(trim(d.label))='writing' OR lower(r.label) LIKE '%writ%' THEN 1
 WHEN lower(r.label) LIKE '%screenplay%' THEN 2
 WHEN lower(trim(d.label))='creator' OR lower(r.label) LIKE '%creator%' OR lower(r.label) LIKE '%created by%' THEN 3
 WHEN lower(trim(d.label))='music' OR lower(r.label) LIKE '%composer%' THEN 4
 WHEN lower(r.label) LIKE '%producer%' OR lower(trim(d.label))='production' AND trim(r.label)='' THEN 5 ELSE -1 END`

const creditColumns = `c.credit_id,c.credited_name,r.label,d.label,c.provider,COALESCE(o.digest,''),c.provider_person_id,COALESCE(p.token,''),` + artworkref.NullableColumns

const creditFrom = ` FROM catalog_credits c
 JOIN catalog_credit_labels r ON r.id=c.role_id JOIN catalog_credit_labels d ON d.id=c.department_id
 LEFT JOIN catalog_people p ON p.id=c.person_id
 LEFT JOIN artwork_selections a ON a.kind='item' AND a.entity_id=c.entity_id AND a.role='portrait' AND a.subject=c.provider||':'||c.provider_person_id
	LEFT JOIN artwork_sources o ON o.digest=a.digest
	LEFT JOIN artwork_blobs sb ON sb.digest=o.small_blob LEFT JOIN artwork_blobs lb ON lb.digest=o.large_blob
 WHERE c.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`

// queryCredits runs a credits query whose first argument is the item.
func (s *Service) queryCredits(item, query string, args ...any) ([]Credit, error) {
	rows, err := s.read().Query(query, append([]any{item}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Credit{}
	for rows.Next() {
		var v Credit
		var digest, person string
		var d artworkref.Descriptor
		if err = rows.Scan(append([]any{&v.ID, &v.Name, &v.Role, &v.Department, &v.Provider, &digest, &person, &v.PersonID}, d.ScanTargets()...)...); err != nil {
			return nil, err
		}
		v.PortraitURL = selectedArtworkURL("item", item, "portrait", v.Provider+":"+person, digest, d)
		out = append(out, v)
	}
	return out, rows.Err()
}

// creditGroupClause selects one group's credits.
func creditGroupClause(group string) string {
	if group == "cast" {
		return ` AND ` + creditIsCast
	}
	return ` AND NOT ` + creditIsCast
}

func (s *Service) creditTotals(item string) (CreditTotals, error) {
	var out CreditTotals
	err := s.read().QueryRow(`SELECT COALESCE(sum(`+creditIsCast+`),0),COALESCE(sum(NOT `+creditIsCast+`),0) FROM catalog_credits c JOIN catalog_credit_labels d ON d.id=c.department_id
	 WHERE c.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, item).Scan(&out.Cast, &out.Crew)
	return out, err
}

// detailCredits is what the detail response carries: the cast's first page
// and the key crew (one credit per person, in rank then billing order).
func (s *Service) detailCredits(item string) ([]Credit, CreditTotals, error) {
	totals, err := s.creditTotals(item)
	if err != nil {
		return nil, totals, err
	}
	cast, err := s.queryCredits(item, `SELECT `+creditColumns+creditFrom+creditGroupClause("cast")+` ORDER BY c.ord LIMIT ?`, creditFirstCast)
	if err != nil {
		return nil, totals, err
	}
	// Key crew is a handful of ranked people out of a title's crew; the walk
	// reads the crew rows that rank, a title's own list.
	ranked, err := s.queryCredits(item, `SELECT `+creditColumns+creditFrom+creditGroupClause("crew")+` AND `+creditCrewRank+`>=0 ORDER BY `+creditCrewRank+`,c.ord`)
	if err != nil {
		return nil, totals, err
	}
	seen := map[string]bool{}
	out := cast
	for _, credit := range ranked {
		key := credit.PersonID
		if key == "" {
			key = "name:" + asciiLower(credit.Name)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, credit)
		if len(seen) == creditKeyCrew {
			break
		}
	}
	return out, totals, nil
}

// ItemCredits pages a title's cast or crew, in billing order, by cursor.
func (s *Service) ItemCredits(viewer Viewer, item, group, cursor string, limit int) (CreditPage, error) {
	out := CreditPage{Group: group, Credits: []Credit{}}
	if group != "cast" && group != "crew" {
		return out, browseIssue("group", "group must be cast or crew")
	}
	if err := s.VisibleItem(s.Context(), viewer, item); err != nil {
		return out, err
	}
	library, err := s.LibraryForItem(item)
	if err != nil {
		return out, err
	}
	if limit < 1 || limit > creditPageMax {
		limit = creditPageMax
	}
	scope := cursorScope{Library: library, Profile: viewer.Profile, Viewer: viewer.Fence, View: "credits", Entity: item, Section: group, Limit: limit}
	revision, err := s.ContentRevision(library, viewer.Profile)
	if err != nil {
		return out, err
	}
	out.Revision = revision
	offset := 0
	if cursor != "" {
		value, err := s.decodeDetailCursor(cursor, scope, revision)
		if err != nil {
			return out, err
		}
		if offset, err = strconv.Atoi(value.Value); err != nil || offset < 0 {
			return out, ErrCursor
		}
	}
	totals, err := s.creditTotals(item)
	if err != nil {
		return out, err
	}
	out.Total = totals.Crew
	if group == "cast" {
		out.Total = totals.Cast
	}
	if out.Credits, err = s.queryCredits(item, `SELECT `+creditColumns+creditFrom+creditGroupClause(group)+` ORDER BY c.ord,c.credit_id LIMIT ? OFFSET ?`, limit, offset); err != nil {
		return out, err
	}
	if next := offset + len(out.Credits); next < out.Total && len(out.Credits) > 0 {
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: strconv.Itoa(next), Expires: time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()}, revision)
	}
	after, e := s.ContentRevision(library, viewer.Profile)
	if err == nil && e != nil {
		err = e
	}
	if err == nil && after != revision {
		err = ErrStaleContinuation
	}
	return out, err
}
