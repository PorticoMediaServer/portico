package read

import (
	"bytes"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type uoPersonalMembership uint8

const (
	uoMembershipMyList        uoPersonalMembership = 1
	uoMembershipFavorite      uoPersonalMembership = 2
	uoMembershipRating        uoPersonalMembership = 3
	uoMembershipNotInterested uoPersonalMembership = 4
)

type uoPersonalMembershipCursor struct {
	public     [16]byte
	membership uoPersonalMembership
	rating     int64
	fence      browseFence
}
type uoPersonalMembershipItem struct {
	subject, revision int64
	public            [16]byte
}
type uoPersonalMembershipPage struct {
	items                 []uoPersonalMembershipItem
	next                  *uoPersonalMembershipCursor
	rawRows, rawPositions int
	exhausted             bool
}

// A parent readStep invokes this only inside publishRead/current authority.
// Exact sparse partial predicates consume <=64 raw personal rows, including
// removed/wrong-library/hidden items. Return IDs only; catalogue title hydration
// belongs to the parent's separately byte-admitted bounded point recipe.
func readUOPersonalMembership(q browseQuery, f browseFence, m uoPersonalMembership, rating int64, after *uoPersonalMembershipCursor, limit int) (out uoPersonalMembershipPage, err error) {
	if limit < 1 || limit > MaxPage || m < uoMembershipMyList || m > uoMembershipNotInterested || m == uoMembershipRating && (rating < 0 || rating > 10) || m != uoMembershipRating && rating != 0 {
		return out, ErrInput
	}
	// Container restriction aggregates require RB's separate typed recipe.
	// Own-age alone cannot prove that a restricted viewer may see the container.
	if f.container != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
		return out, ErrUnqualified
	}
	var anchor [16]byte
	if after != nil {
		if after.fence != f || after.membership != m || after.rating != rating {
			return out, ErrStale
		}
		anchor = after.public
	}
	args := []any{f.profile}
	if m == uoMembershipRating {
		args = append(args, rating)
	}
	args = append(args, anchor[:], int64(MaxRaw))
	rows, err := q(fmt.Sprintf("uo.personal.membership.%d", m), args...)
	if err != nil {
		return out, err
	}
	if len(rows) > MaxRaw {
		return out, ErrUnqualified
	}
	out.rawRows = len(rows)
	for _, r := range rows {
		if len(r) != 8 {
			return uoPersonalMembershipPage{}, ErrUnqualified
		}
		public, ok := r[1].([]byte)
		if !ok || len(public) != 16 || bytes.Compare(public, anchor[:]) <= 0 {
			return uoPersonalMembershipPage{}, ErrUnqualified
		}
		copy(anchor[:], public)
		out.rawPositions++
		out.next = &uoPersonalMembershipCursor{anchor, m, rating, f}
		if r[3] == nil || r[4] == nil || r[5] == nil || r[6] == nil || r[7] == nil {
			continue
		}
		if r[3].(int64) != f.libraryID || r[4].(int64) != f.kind || r[7].(int64) != 0 {
			continue
		}
		age := r[6].(int64)
		if age < 0 && f.allowUnrated == 0 || age >= 0 && f.maximumAge >= 0 && age > f.maximumAge {
			continue
		}
		out.items = append(out.items, uoPersonalMembershipItem{subject: r[0].(int64), public: anchor, revision: r[5].(int64)})
		if len(out.items) == limit {
			return out, nil
		}
	}
	if len(rows) < MaxRaw {
		out.exhausted = true
		out.next = nil
	}
	return out, nil
}

// The first three flag indexes are UO-001 DRAFT DDL, installed only by own test
// fixtures until INT adopts/embeds them. Existing rating is an exact-value seek.
func UOPersonalMembershipDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, m := range []uoPersonalMembership{uoMembershipMyList, uoMembershipFavorite, uoMembershipRating, uoMembershipNotInterested} {
		index, predicate := "personal_current_watchlist", "watchlisted=1"
		switch m {
		case uoMembershipFavorite:
			index, predicate = "personal_current_favorite", "favorite=1"
		case uoMembershipRating:
			index, predicate = "personal_current_rating", "p.rating=?"
		case uoMembershipNotInterested:
			index, predicate = "personal_current_not_interested", "not_interested=1"
		}
		params := []stmt.Param{{Name: "profile", Type: stmt.Text}}
		args := []any{"profile-one"}
		if m == uoMembershipRating {
			params = append(params, stmt.Param{Name: "rating", Type: stmt.Integer})
			args = append(args, int64(0))
		}
		params = append(params, stmt.Param{Name: "after", Type: stmt.Blob}, stmt.Param{Name: "limit", Type: stmt.Integer})
		args = append(args, make([]byte, 16), int64(64))
		query := `SELECT p.item_id,p.item_public_id,p.revision,e.library_id,e.kind,e.revision,COALESCE(e.admission_own_age,-1),e.retired FROM personal_items AS p INDEXED BY ` + index + ` LEFT JOIN catalog_entities AS e ON e.id=p.item_id AND e.public_id=p.item_public_id WHERE p.profile_id=? AND ` + predicate + ` AND p.item_public_id>? ORDER BY p.item_public_id LIMIT ?`
		out = append(out, stmt.Definition{ID: readStatementID(fmt.Sprintf("uo.personal.membership.%d", m)), Owner: "store/read UO personal draft", Kind: stmt.Read, SQL: query, Params: params, CallSites: []string{"store/read/uo_personal_membership.go"}, Cohorts: []stmt.Cohort{{Name: "sparse", Bindings: args}}, Plan: stmt.Plan{Hot: true, Indexes: []string{index}, PrimaryKeys: []string{"e"}, Seeks: []string{"profile_id=?"}}})
	}
	return out
}
