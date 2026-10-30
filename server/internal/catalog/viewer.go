package catalog

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Viewer is the complete authority for a catalogue projection. A nil or empty
// Libraries slice sees no catalogue rows. Restrictions is the viewer's whole
// content policy: the profile's own restrictions already narrowed by its
// account's limits (contentaccess.RestrictionsTx), so nothing here can widen
// either.
type Viewer struct {
	Profile      string
	Fence        string
	Libraries    []string
	Restrictions identity.ContentRestrictions
}

// EffectiveRestrictions is the policy the visibility predicate compiles.
func (v Viewer) EffectiveRestrictions() identity.ContentRestrictions {
	return v.Restrictions
}

func (v Viewer) AllowsLibrary(library string) bool {
	for _, id := range v.Libraries {
		if id == library {
			return true
		}
	}
	return false
}

func (v Viewer) librariesJSON() string {
	raw, _ := json.Marshal(v.Libraries)
	return string(raw)
}

// itemVisibilitySQL requires the item to belong to a visible library and pass
// the restriction predicate inside the same SQL statement as paging/counting.
// `item` must be a SQL expression yielding the INTEGER catalog_entities.id.
func (v Viewer) itemVisibilitySQL(item string) (string, []any) {
	if len(v.Libraries) == 0 {
		return "0", nil
	}
	clause := `EXISTS(SELECT 1 FROM catalog_entities visible_item JOIN catalog_libraries visible_library ON visible_library.id=visible_item.library_id
	 WHERE visible_item.id=` + item + ` AND visible_item.retired=0
	 AND visible_library.library_id IN(SELECT value FROM json_each(?)) AND visible_library.retired=0)`
	args := []any{v.librariesJSON()}
	if restriction, values := ItemRestrictionSQL(item, v.EffectiveRestrictions()); restriction != "1" {
		clause += ` AND ` + restriction
		args = append(args, values...)
	}
	return clause, args
}

// entityVisibilitySQL applies the same scope to browse containers. A container
// is visible only when it has a visible member, except a truly empty container
// in a visible library, which the existing catalogue contract permits.
// `entity` yields the INTEGER catalog_browse_rows.entity_id; `library` yields
// the public TEXT library id.
func (v Viewer) entityVisibilitySQL(entity, library string) (string, []any) {
	if len(v.Libraries) == 0 {
		return "0", nil
	}
	member, args := v.itemVisibilitySQL("visible_member.id")
	clause := `EXISTS(SELECT 1 FROM catalog_browse_rows scoped JOIN catalog_libraries scope_library ON scope_library.id=scoped.library_id
	 WHERE scoped.entity_id=` + entity + ` AND scope_library.library_id=` + library + `
	 AND scope_library.library_id IN(SELECT value FROM json_each(?)) AND scope_library.retired=0
	 AND (NOT EXISTS(SELECT 1 FROM catalog_browse_memberships any_member WHERE any_member.entity_id=scoped.entity_id)
	 OR EXISTS(SELECT 1 FROM catalog_browse_memberships member_edge JOIN catalog_entities visible_member ON visible_member.id=member_edge.item_id
	 WHERE member_edge.entity_id=scoped.entity_id AND ` + member + `)))`
	return clause, append([]any{v.librariesJSON()}, args...)
}

func (s *Service) prepareViewer(v Viewer) error {
	return nil
}

// VisibleItem is the same predicate used by catalogue pages, for single-item
// routes such as artwork and title detail. Absence and restriction both return
// sql.ErrNoRows, so knowing an opaque id reveals no hidden title.
func (s *Service) VisibleItem(ctx context.Context, v Viewer, item string) error {
	if err := s.prepareViewer(v); err != nil {
		return err
	}
	// The predicates name the outer row's integer id: an expression with its
	// own placeholder would need one argument for every place a restriction
	// clause repeats it.
	clause, args := v.itemVisibilitySQL("i.id")
	clause += recordingsClause("i.id", v.Restrictions)
	var found int
	err := s.read().QueryRowContext(ctx, `SELECT 1 FROM catalog_entities i WHERE i.public_id=pid_blob(?) AND `+clause, append([]any{item}, args...)...).Scan(&found)
	if err != nil {
		return err
	}
	return nil
}

// VisibleEntity applies the same membership rule used by catalogue container
// pages before an individual container's artwork is opened. A hidden container
// and an unknown ID both return sql.ErrNoRows.
func (s *Service) VisibleEntity(ctx context.Context, v Viewer, kind, id string) error {
	if kind == "item" {
		return s.VisibleItem(ctx, v, id)
	}
	switch kind {
	case "album", "show", "artist", "book", "season", "collection":
	default:
		return sql.ErrNoRows
	}
	compactKind, err := compactcatalog.ParseKind(kind)
	if err != nil {
		return sql.ErrNoRows
	}
	clause, args := v.entityVisibilitySQL("e.entity_id", "l.library_id")
	values := append([]any{id, compactKind}, args...)
	var found int
	return s.read().QueryRowContext(ctx, `SELECT 1 FROM catalog_browse_rows e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND e.kind=? AND `+clause, values...).Scan(&found)
}
