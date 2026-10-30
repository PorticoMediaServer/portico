package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strings"
	"time"
	"unicode/utf8"
)

// These are personal resources. The existing library collections remain catalog
// groupings and are intentionally not repurposed as a viewer-owned store.
// A saved view stores the whole browse query: the pivot it was written against,
// the expression tree and the sort. It is validated against the same
// capabilities the browse engine publishes, so a saved view can never name a
// field or sort the engine cannot execute.
type SavedViewDefinition struct {
	LibraryID    string                `json:"libraryId"`
	Pivot        string                `json:"pivot"`
	Query        *BrowseNode           `json:"query"`
	Sort         []BrowseSortSelection `json:"sort"`
	Presentation string                `json:"presentation"`
}
type SavedResource struct {
	ServerID          string               `json:"serverId"`
	ViewerFence       string               `json:"viewerFence"`
	ID                string               `json:"id"`
	Kind              string               `json:"kind"`
	Name              string               `json:"name"`
	Summary           string               `json:"summary"`
	Visibility        string               `json:"visibility"`
	Revision          int64                `json:"revision"`
	Role              string               `json:"role"`
	EntryCount        int                  `json:"entryCount"`
	Actions           []string             `json:"actions"`
	Definition        *SavedViewDefinition `json:"definition,omitempty"`
	Status            string               `json:"status"`
	InvalidComponents []string             `json:"invalidComponents"`
	Pinned            bool                 `json:"pinned"`
	PinRevision       int64                `json:"pinRevision"`
	Shares            []PlaylistShare      `json:"shares,omitempty"`
}
type SavedResourcePage struct {
	ServerID    string          `json:"serverId"`
	ViewerFence string          `json:"viewerFence"`
	Revision    int64           `json:"revision"`
	Resources   []SavedResource `json:"resources"`
	NextCursor  string          `json:"nextCursor"`
}
type SavedResourceEntry struct {
	ID     string        `json:"id"`
	Hidden bool          `json:"hidden"`
	Media  *ContentEntry `json:"media,omitempty"`
}
type SavedResourceContent struct {
	Resource   SavedResource        `json:"resource"`
	Entries    []SavedResourceEntry `json:"entries"`
	NextCursor string               `json:"nextCursor"`
	Browse     *BrowseResult        `json:"browse,omitempty"`
	// A collection's page: the query it was read with, the orders and filters
	// it offers (see ResourceContentQuery), how many entries match, the index of
	// its first entry in the whole query and, in the collection's own order,
	// the entries around it. A saved view's page carries its browse instead.
	Query      *ContentQuery        `json:"query,omitempty"`
	Sorts      []ContentSort        `json:"sorts,omitempty"`
	Filters    []ContentFilter      `json:"filters,omitempty"`
	TotalCount *int                 `json:"totalCount,omitempty"`
	Start      int                  `json:"start"`
	Order      *ContentOrderWitness `json:"order,omitempty"`
}
type SavedResourceMutation struct {
	OperationID      string               `json:"operationId"`
	ExpectedRevision int64                `json:"expectedRevision"`
	Kind             string               `json:"kind,omitempty"`
	Name             *string              `json:"name,omitempty"`
	Summary          *string              `json:"summary,omitempty"`
	Visibility       *string              `json:"visibility,omitempty"`
	Definition       *SavedViewDefinition `json:"definition,omitempty"`
	AddItemIDs       []string             `json:"addItemIds,omitempty"`
	RemoveEntryIDs   []string             `json:"removeEntryIds,omitempty"`
	// UndoToken puts back the entries a removal just took out (the removal's
	// receipt carries it), in place of adding or removing.
	UndoToken string `json:"undoToken,omitempty"`
	// EntryIDs and AfterEntryID reorder a collection (at most 200 entries):
	// with AfterEntryID the entries move, in the order given, to just after
	// it ("" the front); without it they swap among the places they hold.
	EntryIDs     []string `json:"entryIds,omitempty"`
	AfterEntryID *string  `json:"afterEntryId,omitempty"`
	Authority    string   `json:"authority,omitempty"`
	AccountID    string   `json:"accountId,omitempty"`
	ProfileID    string   `json:"profileId,omitempty"`
	Role         string   `json:"role,omitempty"`
}
type SavedResourceReceipt struct {
	OperationID string         `json:"operationId"`
	ResourceID  string         `json:"resourceId"`
	Revision    int64          `json:"revision"`
	Deleted     bool           `json:"deleted"`
	Entries     *EntryOutcomes `json:"entries,omitempty"`
	// A removal can be undone until UndoUntil by sending UndoToken back.
	UndoToken string `json:"undoToken,omitempty"`
	UndoUntil string `json:"undoUntil,omitempty"`
}

func actorKey(a ResourceActor) string {
	return identity.PersonalKey(identity.Viewer{Authority: a.Authority, AccountID: a.AccountID, ProfileID: a.ProfileID})
}
func savedResourceRole(q personalReader, id string, a ResourceActor) (string, string, int64, bool, error) {
	var owner, kind, visibility string
	var rev int64
	var deleted bool
	e := q.QueryRow(`SELECT owner_key,kind,revision,deleted,visibility FROM saved_resources WHERE id=?`, id).Scan(&owner, &kind, &rev, &deleted, &visibility)
	if e != nil {
		return "", "", 0, false, e
	}
	if owner == actorKey(a) {
		return "owner", kind, rev, deleted, nil
	}
	var role string
	e = q.QueryRow(`SELECT role FROM saved_resource_shares WHERE resource_id=? AND authority=? AND account_id=? AND profile_id=?`, id, a.Authority, a.AccountID, a.ProfileID).Scan(&role)
	if e == nil {
		return role, kind, rev, deleted, nil
	}
	// A server-visible collection is readable by every member. Editing it still
	// needs an explicit share; visibility publishes, it does not delegate.
	if visibility == "server" && kind == "collection" && !deleted {
		return "viewer", kind, rev, deleted, nil
	}
	return "", "", 0, false, sql.ErrNoRows
}
func validationPath(e error, fallback string) string {
	var issue *BrowseValidationError
	if errors.As(e, &issue) && issue.Path != "" {
		return issue.Path
	}
	return fallback
}

// validateSavedView reports the definition paths that fail, so a client can
// attach each failure to the control that produced it.
func validateSavedView(q personalReader, v SavedViewDefinition) []string {
	bad := []string{}
	var kind string
	if e := q.QueryRow(`SELECT kind FROM libraries WHERE id=?`, v.LibraryID).Scan(&kind); e != nil {
		return append(bad, "libraryId")
	}
	pivot, known := pivotForKind(kind, v.Pivot)
	if !known || !pivot.Browsable {
		bad = append(bad, "pivot")
	}
	if v.Presentation != "grid" && v.Presentation != "list" {
		bad = append(bad, "presentation")
	}
	if known && pivot.Browsable {
		if len(v.Sort) == 0 {
			bad = append(bad, "sort")
		} else if _, e := resolveBrowseSorts(pivot, v.Sort); e != nil {
			bad = append(bad, validationPath(e, "sort"))
		}
	}
	raw, e := json.Marshal(v.Query)
	if e != nil {
		bad = append(bad, "query")
	} else if _, e = ParseBrowseQuery(raw, "query"); e != nil {
		bad = append(bad, validationPath(e, "query"))
	}
	return bad
}
func (s *Service) SavedResource(server, fence, id string, a ResourceActor) (SavedResource, error) {
	out := SavedResource{ServerID: server, ViewerFence: fence, ID: id, Actions: []string{"pin"}, Status: "ready", InvalidComponents: []string{}}
	role, kind, rev, deleted, e := savedResourceRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if deleted {
		return out, sql.ErrNoRows
	}
	out.Role = role
	out.Kind = kind
	out.Revision = rev
	var definition string
	if e = s.read().QueryRow(`SELECT name,summary,visibility,definition,(SELECT count(*) FROM saved_resource_entries WHERE resource_id=saved_resources.id) FROM saved_resources WHERE id=?`, id).Scan(&out.Name, &out.Summary, &out.Visibility, &definition, &out.EntryCount); e != nil {
		return out, e
	}
	if e = s.read().QueryRow(`SELECT COALESCE((SELECT pinned FROM saved_pins WHERE owner_key=? AND kind=? AND resource_id=?),0),COALESCE((SELECT revision FROM saved_pins WHERE owner_key=? AND kind=? AND resource_id=?),0)`, actorKey(a), kind, id, actorKey(a), kind, id).Scan(&out.Pinned, &out.PinRevision); e != nil {
		return out, e
	}
	if kind == "view" {
		var v SavedViewDefinition
		if e = json.Unmarshal([]byte(definition), &v); e != nil {
			out.Status = "needs-review"
			out.InvalidComponents = []string{"definition"}
		} else {
			out.Definition = &v
			out.InvalidComponents = validateSavedView(s.read(), v)
			if len(out.InvalidComponents) > 0 {
				out.Status = "needs-review"
			}
		}
	}
	if role == "owner" {
		out.Actions = append(out.Actions, "update", "delete")
		if kind == "collection" {
			out.Actions = append(out.Actions, "entries", "reorder", "share")
		}
	}
	if role == "editor" && kind == "collection" {
		out.Actions = append(out.Actions, "entries", "reorder")
	}
	if role == "owner" && kind == "collection" {
		out.Shares = []PlaylistShare{}
		rows, e := s.read().Query(`SELECT authority,account_id,profile_id,role FROM saved_resource_shares WHERE resource_id=? ORDER BY authority,account_id,profile_id`, id)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var share PlaylistShare
			if e = rows.Scan(&share.Authority, &share.AccountID, &share.ProfileID, &share.Role); e != nil {
				rows.Close()
				return out, e
			}
			share.DisplayName = profileLabel(share.ProfileID)
			out.Shares = append(out.Shares, share)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	_, _, after, dead, e := savedResourceRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if after != rev || dead {
		return out, ErrStaleContinuation
	}
	return out, nil
}
func (s *Service) SavedResources(server, fence string, a ResourceActor, kind, cursor string, pinned bool, limit int) (SavedResourcePage, error) {
	out := SavedResourcePage{ServerID: server, ViewerFence: fence, Resources: []SavedResource{}}
	if (kind != "collection" && kind != "view") || limit < 1 || limit > 100 {
		return out, ErrCursor
	}
	if e := s.read().QueryRow(`SELECT revision FROM saved_resource_generation WHERE id=1`).Scan(&out.Revision); e != nil {
		return out, e
	}
	scope := cursorScope{Profile: actorKey(a), View: "saved_" + kind, Viewer: fence, Limit: limit}
	if pinned {
		scope.Category = "pinned"
	}
	// A pinned listing is returned in the viewer's own arrangement; anything
	// pinned but never arranged sorts after the arranged entries, by name.
	sortKey := `lower(r.name)`
	selectArgs := []any{}
	if pinned {
		sortKey = `printf('%08d',COALESCE((SELECT o.position FROM saved_pin_order o WHERE o.owner_key=? AND o.kind=r.kind AND o.resource_id=r.id),99999999))||char(31)||lower(r.name)`
		selectArgs = append(selectArgs, actorKey(a))
	}
	// Server-visible collections are part of every member's collection list.
	where := ` FROM saved_resources r WHERE r.deleted=0 AND r.kind=? AND (r.owner_key=? OR EXISTS(SELECT 1 FROM saved_resource_shares s WHERE s.resource_id=r.id AND s.authority=? AND s.account_id=? AND s.profile_id=?) OR (r.kind='collection' AND r.visibility='server'))`
	args := append(selectArgs, kind, actorKey(a), a.Authority, a.AccountID, a.ProfileID)
	if pinned {
		where += ` AND EXISTS(SELECT 1 FROM saved_pins p WHERE p.owner_key=? AND p.kind=r.kind AND p.resource_id=r.id AND p.pinned=1)`
		args = append(args, actorKey(a))
	}
	rev := ContentRevision{Catalog: out.Revision}
	outer := ``
	if cursor != "" {
		c, e := s.decodeRevisionCursor(cursor, scope, rev)
		if e != nil {
			return out, e
		}
		outer = ` WHERE (sort_key>? OR (sort_key=? AND id>?))`
		args = append(args, c.Value, c.Value, c.ID)
	}
	args = append(args, limit+1)
	rows, e := s.read().Query(`SELECT id,name,sort_key FROM (SELECT r.id AS id,r.name AS name,`+sortKey+` AS sort_key`+where+`)`+outer+` ORDER BY sort_key,id LIMIT ?`, args...)
	if e != nil {
		return out, e
	}
	ids, keys := []string{}, []string{}
	for rows.Next() {
		var id, name, key string
		if e = rows.Scan(&id, &name, &key); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
		keys = append(keys, key)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > limit {
		ids = ids[:limit]
		out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: keys[limit-1], ID: ids[limit-1], Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return out, e
		}
	}
	for _, id := range ids {
		r, e := s.SavedResource(server, fence, id, a)
		if e != nil {
			return out, e
		}
		out.Resources = append(out.Resources, r)
	}
	var after int64
	if e = s.read().QueryRow(`SELECT revision FROM saved_resource_generation WHERE id=1`).Scan(&after); e != nil {
		return out, e
	}
	if after != out.Revision {
		return out, ErrStaleContinuation
	}
	return out, nil
}
func (s *Service) SavedResourceContent(viewer Viewer, server, fence, id string, a ResourceActor, query ResourceContentQuery, cursor string, limit int, readingAdmission ...SavedGroupAdmission) (SavedResourceContent, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out SavedResourceContent
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			out, readErr = s.WithContext(ctx).SavedResourceContent(viewer, server, fence, id, a, query, cursor, limit, readingAdmission...)
			return readErr
		})
		return out, err
	}
	libraries := viewer.Libraries
	if err := s.prepareViewer(viewer); err != nil {
		return SavedResourceContent{}, err
	}
	out := SavedResourceContent{Entries: []SavedResourceEntry{}}
	if limit < 1 || limit > 100 {
		return out, ErrCursor
	}
	r, e := s.SavedResource(server, fence, id, a)
	if e != nil {
		return out, e
	}
	out.Resource = r
	if r.Kind == "view" {
		// A saved view's order and filters are its definition; it has no
		// temporary page query.
		if query != (ResourceContentQuery{}) {
			return out, requestinput.New("a saved view has no page query")
		}
		if r.Status == "needs-review" {
			return out, nil
		}
		v := r.Definition
		allowed := false
		for _, library := range libraries {
			if library == v.LibraryID {
				allowed = true
			}
		}
		if !allowed {
			return out, identity.ErrUnauthorized
		}
		// The saved query runs through the same engine the browse routes use; a
		// saved view is a stored request, never a second query implementation.
		viewScope := viewer
		viewScope.Fence = fence + ":" + id + ":" + operationHash(r.Revision)
		result, e := s.BrowseEntities(viewScope, BrowseRequest{ServerID: server, Profile: actorKey(a), ViewerFence: viewScope.Fence, Library: v.LibraryID, Pivot: v.Pivot, Query: v.Query, Sort: v.Sort, Limit: limit, Cursor: cursor})
		if e != nil {
			return out, e
		}
		out.Browse = &result
		out.NextCursor = result.PageInfo.NextCursor
	} else {
		raw, _ := json.Marshal(libraries)
		rev, e := s.homeRevision(libraries, actorKey(a))
		if e != nil {
			return out, e
		}
		rev.Catalog += r.Revision
		// A build's admitted Reading formats are part of this paging domain.
		// A capability change cannot continue a page made under another domain.
		domain := operationHash(struct {
			Mobi   bool
			Comics string
		}{s.readingMobiConfigured(), readingcap.ComicFormatsSQL()})
		query, sort, err := query.normalize()
		if err != nil {
			return out, err
		}
		out.Query = &ContentQuery{Sort: query.Sort, Direction: query.Direction, Category: query.Filter, Limit: limit, SearchMode: "none"}
		out.Sorts = resourceContentSortOptions()
		scope := cursorScope{Profile: actorKey(a), View: "saved_collection", Entity: id, Viewer: fence + ":" + domain, Sort: query.Sort, Direction: query.Direction, Category: query.Filter, Limit: limit}
		// Inaccessible membership has an opaque stable entry ID and no media facts.
		// A collection lists in its own order: new entries at the end, wherever its
		// owner (or an editor) has moved them since.
		restriction, bound := ItemRestrictionSQL("i.id", viewer.EffectiveRestrictions())
		from := ` FROM (SELECT ? AS profile_id) viewer JOIN saved_resource_entries e LEFT JOIN catalog_entities i ON i.id=e.item_id AND i.retired=0 AND EXISTS(SELECT 1 FROM catalog_item_details detail WHERE detail.entity_id=i.id) AND i.library_id IN(SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?)) AND retired=0) WHERE e.resource_id=? AND (i.id IS NULL OR ` + restriction + `)`
		fromArgs := append([]any{actorKey(a), string(raw), id}, bound...)
		counted := make([]int, len(resourceContentFilterIDs))
		counts := make([]any, len(counted))
		for n := range counted {
			counts[n] = &counted[n]
		}
		if e = s.read().QueryRow(resourceContentCountsSQL()+from, fromArgs...).Scan(counts...); e != nil {
			return out, e
		}
		out.Filters = resourceContentFilterOptions(counted)
		for n, f := range resourceContentFilterIDs {
			if f == query.Filter {
				out.TotalCount = &counted[n]
			}
		}
		order := newResourceContentOrder(query, sort, "e.id", false)
		where := ` AND ` + resourceContentFilterSQL(query.Filter)
		args := append([]any{}, fromArgs...)
		if cursor != "" {
			c, e := s.decodeRevisionCursor(cursor, scope, rev)
			if e != nil {
				return out, e
			}
			seek, seekArgs, e := order.after(c.Keys)
			if e != nil {
				return out, e
			}
			where += ` AND ` + seek
			args = append(args, seekArgs...)
			out.Start = c.Start
		}
		args = append(args, limit+1)
		rows, e := s.read().Query(`SELECT e.id,COALESCE(pid(i.public_id),''),`+order.selectSQL()+from+where+` ORDER BY `+order.orderBy+` LIMIT ?`, args...)
		if e != nil {
			return out, e
		}
		ids, keys := []string{}, [][]any{}
		for rows.Next() {
			var entry SavedResourceEntry
			var item string
			key := make([]any, len(order.columns))
			dest := []any{&entry.ID, &item}
			for n := range key {
				dest = append(dest, &key[n])
			}
			if e = rows.Scan(dest...); e != nil {
				rows.Close()
				return out, e
			}
			entry.Hidden = item == ""
			out.Entries = append(out.Entries, entry)
			ids = append(ids, item)
			keys = append(keys, key)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		following := ""
		if len(out.Entries) > limit {
			following = out.Entries[limit].ID
			out.Entries = out.Entries[:limit]
			ids = ids[:limit]
			out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Keys: resourceOrderKeys(keys[limit-1]), Start: out.Start + limit, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
			if e != nil {
				return out, e
			}
		}
		// The witnesses read the same membership the page does: a restricted
		// entry is not in this viewer's order, so it is never a neighbour.
		if query.manual() && len(out.Entries) > 0 {
			witness := &ContentOrderWitness{Next: following}
			edge := func(extra string, extraArgs []any, desc bool) (string, error) {
				dir := " ASC"
				if desc {
					dir = " DESC"
				}
				var token string
				e := s.read().QueryRow(`SELECT e.id`+from+extra+` ORDER BY e.order_key`+dir+`,e.id`+dir+` LIMIT 1`, append(append([]any{}, fromArgs...), extraArgs...)...).Scan(&token)
				if errors.Is(e, sql.ErrNoRows) {
					return "", nil
				}
				return token, e
			}
			if cursor != "" {
				if witness.Previous, e = edge(` AND (e.order_key,e.id)<(?,?)`, keys[0], true); e != nil {
					return out, e
				}
			}
			if following == "" {
				witness.Last = out.Entries[len(out.Entries)-1].ID
			} else if witness.Last, e = edge("", nil, true); e != nil {
				return out, e
			}
			out.Order = witness
		}
		visibleIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			if id != "" {
				visibleIDs = append(visibleIDs, id)
			}
		}
		items, e := s.mediaPage(actorKey(a), visibleIDs, false)
		if e != nil {
			return out, e
		}
		byID := map[string]Item{}
		for _, item := range items {
			byID[item.ID] = item
		}
		projected := make(map[string]ContentEntry, len(byID))
		for id, item := range byID {
			projected[id] = contentItem(item)
		}
		var admit SavedGroupAdmission
		if len(readingAdmission) == 1 {
			admit = readingAdmission[0]
		}
		if e = s.resourceReadingEntries(viewer, visibleIDs, projected, admit); e != nil {
			return out, e
		}
		for i, item := range ids {
			if item == "" {
				continue
			}
			entry, ok := projected[item]
			if !ok {
				return out, ErrStaleContinuation
			}
			out.Entries[i].Media = &entry
		}
		after, e := s.homeRevision(libraries, actorKey(a))
		if e != nil {
			return out, e
		}
		after.Catalog += r.Revision
		if after != rev {
			return out, ErrStaleContinuation
		}
	}
	_, _, after, dead, e := savedResourceRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if after != r.Revision || dead {
		return out, ErrStaleContinuation
	}
	return out, nil
}
func (s *Service) MutateSavedResource(a ResourceActor, id, action string, m SavedResourceMutation, authorize func(*sql.Tx) error, admitItem ...func(*sql.Tx, string) error) (SavedResourceReceipt, error) {
	if err := requireCatalogMutationContext(s.Context(), m.OperationID); err != nil {
		return SavedResourceReceipt{}, err
	}
	out := SavedResourceReceipt{OperationID: m.OperationID, ResourceID: id}
	if !personalOperation.MatchString(m.OperationID) || m.ExpectedRevision < 0 || a.Authority == "" || a.AccountID == "" || a.ProfileID == "" {
		return out, requestinput.New("invalid resource operation")
	}
	if m.Name != nil && !resourceName(*m.Name) {
		return out, requestinput.New("resource name must contain 1-200 characters")
	}
	if m.Summary != nil && (!utf8.ValidString(*m.Summary) || utf8.RuneCountInString(*m.Summary) > 4000 || strings.ContainsRune(*m.Summary, 0)) {
		return out, requestinput.New("invalid resource summary")
	}
	if len(m.AddItemIDs) > 100 || len(m.RemoveEntryIDs) > 100 {
		return out, requestinput.New("membership batches are limited to 100 entries")
	}
	if m.Visibility != nil && *m.Visibility != "private" && *m.Visibility != "server" {
		return out, requestinput.New("collection visibility must be private or server")
	}
	if m.UndoToken != "" && (action != "entries" || m.AddItemIDs != nil || m.RemoveEntryIDs != nil || !personalOperation.MatchString(m.UndoToken)) {
		return out, requestinput.New("an undo token undoes a removal on its own")
	}
	if (m.EntryIDs != nil || m.AfterEntryID != nil) && action != "reorder" || action == "reorder" && (len(m.EntryIDs) == 0 || len(m.EntryIDs) > 200) {
		return out, requestinput.New("a reorder names 1-200 entries")
	}
	if m.Kind != "" && action != "create" || (m.Name != nil || m.Summary != nil || m.Definition != nil || m.Visibility != nil) && action != "create" && action != "update" || (m.AddItemIDs != nil || m.RemoveEntryIDs != nil) && action != "entries" || (m.Authority != "" || m.AccountID != "" || m.ProfileID != "") && action != "share" && action != "unshare" || m.Role != "" && action != "share" {
		return out, requestinput.New("fields do not apply to resource operation")
	}
	hash := operationHash(struct {
		ID, Action string
		Mutation   SavedResourceMutation
	}{id, action, m})
	gated, e := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	_, _, protocol := usermutation.RequestFrom(s.Context())
	priorReceipt, e := usermutation.CheckContext(s.Context(), tx, time.Now().UnixMilli())
	if e != nil {
		return out, e
	}
	if priorReceipt != nil {
		out.ResourceID, out.Revision = priorReceipt.TargetID, priorReceipt.Revision
		role, _, _, deleted, err := savedResourceRole(tx, out.ResourceID, a)
		if err != nil {
			var exists bool
			if e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM saved_resources WHERE id=?)`, out.ResourceID).Scan(&exists); e != nil {
				return out, e
			}
			if exists || !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
			deleted = true
		} else if deleted && role != "owner" {
			return out, sql.ErrNoRows
		} else if role != "owner" && (role != "editor" || action != "entries" && action != "reorder") {
			return out, identity.ErrUnauthorized
		}
		out.Deleted = deleted || priorReceipt.Status == "deleted"
		return out, nil
	}
	if e = authorize(tx); e != nil {
		return out, e
	}
	role, kind, rev, deleted := "owner", m.Kind, int64(0), false
	if action != "create" {
		role, kind, rev, deleted, e = savedResourceRole(tx, id, a)
		if e != nil {
			return out, e
		}
		if role != "owner" && (role != "editor" || action != "entries" && action != "reorder") {
			return out, identity.ErrUnauthorized
		}
	}
	if !protocol {
		var prior, raw string
		e = tx.QueryRow(`SELECT request_hash,response FROM saved_resource_receipts WHERE owner_key=? AND operation_id=? AND created_at>=?`, actorKey(a), m.OperationID, time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339)).Scan(&prior, &raw)
		if e == nil {
			if prior != hash {
				return out, ErrOperationConflict
			}
			if e = json.Unmarshal([]byte(raw), &out); e != nil {
				return out, e
			}
			_, _, _, _, e = savedResourceRole(tx, out.ResourceID, a)
			return out, e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e = checkRetired(tx, actorKey(a), "resource", m.OperationID, hash); e != nil {
			return out, e
		}
	}
	if deleted {
		return out, sql.ErrNoRows
	}
	if rev != m.ExpectedRevision {
		return out, ErrPlaylistConflict
	}
	switch action {
	case "create":
		if m.Name == nil || (kind != "collection" && kind != "view") {
			return out, requestinput.New("resource kind and name are required")
		}
		if kind == "view" && m.Definition == nil || kind == "collection" && m.Definition != nil {
			return out, requestinput.New("only saved views require a query definition")
		}
		definition := "{}"
		if m.Definition != nil {
			if len(validateSavedView(tx, *m.Definition)) > 0 {
				return out, requestinput.New("saved view needs review")
			}
			b, _ := json.Marshal(m.Definition)
			definition = string(b)
		}
		id = identity.Token()
		out.ResourceID = id
		summary := ""
		if m.Summary != nil {
			summary = *m.Summary
		}
		visibility := "private"
		if m.Visibility != nil {
			if kind != "collection" {
				return out, requestinput.New("only collections carry visibility")
			}
			visibility = *m.Visibility
		}
		_, e = tx.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,summary,visibility,definition,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, kind, actorKey(a), a.Authority, a.AccountID, a.ProfileID, *m.Name, summary, visibility, definition, time.Now().UTC().Format(time.RFC3339))
		rev = 1
	case "update":
		if m.Name == nil && m.Summary == nil && m.Definition == nil && m.Visibility == nil {
			return out, requestinput.New("set name, summary, visibility or saved query")
		}
		if m.Visibility != nil && kind != "collection" {
			return out, requestinput.New("only collections carry visibility")
		}
		var definition *string
		if m.Definition != nil {
			if kind != "view" || len(validateSavedView(tx, *m.Definition)) > 0 {
				return out, requestinput.New("invalid saved view definition")
			}
			b, _ := json.Marshal(m.Definition)
			v := string(b)
			definition = &v
		}
		_, e = tx.Exec(`UPDATE saved_resources SET name=COALESCE(?,name),summary=COALESCE(?,summary),visibility=COALESCE(?,visibility),definition=COALESCE(?,definition) WHERE id=?`, m.Name, m.Summary, m.Visibility, definition, id)
	case "delete":
		_, e = tx.Exec(`UPDATE saved_resources SET deleted=1 WHERE id=?`, id)
		if e == nil {
			_, e = tx.Exec(`DELETE FROM saved_resource_entries WHERE resource_id=?`, id)
		}
		if e == nil {
			_, e = tx.Exec(`DELETE FROM saved_resource_shares WHERE resource_id=?`, id)
		}
		if e == nil {
			_, e = tx.Exec(`DELETE FROM saved_pins WHERE resource_id=? AND kind=?`, id, kind)
		}
		out.Deleted = true
	case "entries":
		if kind != "collection" || len(m.AddItemIDs)+len(m.RemoveEntryIDs) == 0 && m.UndoToken == "" {
			return out, requestinput.New("collection membership changes are required")
		}
		// Membership is reported per item: an item that vanished between the
		// client's read and this write must not discard the rest of the batch.
		outcomes := newEntryOutcomes()
		e = nil
		if m.UndoToken != "" {
			// The same entries return under their own ids, so each keeps its place.
			var removed []removedEntry
			if removed, e = takeEntryUndo(tx, actorKey(a), "collection", id, m.UndoToken); e != nil {
				return out, e
			}
			for _, r := range removed {
				var result sql.Result
				if result, e = tx.Exec(`INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, r.entry, id, r.item, r.orderKey); e != nil {
					return out, e
				}
				if n, _ := result.RowsAffected(); n == 1 {
					outcomes.Added = append(outcomes.Added, r.entry)
				} else {
					outcomes.Unchanged = append(outcomes.Unchanged, r.entry)
				}
			}
		}
		var removed []removedEntry
		for _, item := range m.AddItemIDs {
			// Filter in the accepting transaction, after receipt reservation. A
			// replay must never reconsider an earlier rejected addition.
			if len(admitItem) > 0 && admitItem[0] != nil {
				if err := admitItem[0](tx, item); errors.Is(err, sql.ErrNoRows) {
					outcomes.fail(item, "not_found")
					continue
				} else if err != nil {
					return out, err
				}
			}
			var entityID int64
			if err := tx.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND retired=0`, item).Scan(&entityID); errors.Is(err, sql.ErrNoRows) {
				outcomes.fail(item, "not_found")
				continue
			} else if err != nil {
				return out, err
			}
			var member int
			if e = tx.QueryRow(`SELECT count(*) FROM saved_resource_entries WHERE resource_id=? AND item_id=?`, id, entityID).Scan(&member); e != nil {
				return out, e
			}
			if member > 0 {
				outcomes.Unchanged = append(outcomes.Unchanged, item)
				continue
			}
			if e = appendSavedEntry(tx, id, identity.Token(), entityID); e != nil {
				return out, e
			}
			outcomes.Added = append(outcomes.Added, item)
		}
		for _, entry := range m.RemoveEntryIDs {
			r := removedEntry{entry: entry}
			err := tx.QueryRow(`DELETE FROM saved_resource_entries WHERE resource_id=? AND id=? RETURNING item_id,order_key`, id, entry).Scan(&r.item, &r.orderKey)
			if errors.Is(err, sql.ErrNoRows) {
				outcomes.fail(entry, "not_found")
				continue
			}
			if err != nil {
				return out, err
			}
			outcomes.Removed = append(outcomes.Removed, entry)
			removed = append(removed, r)
		}
		if len(m.RemoveEntryIDs) > 0 {
			if out.UndoToken, out.UndoUntil, e = captureEntryUndo(tx, actorKey(a), "collection", id, removed); e != nil {
				return out, e
			}
		}
		out.Entries = &outcomes
	case "reorder":
		if kind != "collection" {
			return out, requestinput.New("only a collection has an order")
		}
		e = reorderSavedEntries(tx, id, m.EntryIDs, m.AfterEntryID)
	case "share", "unshare":
		if kind != "collection" || m.Authority == "" || m.AccountID == "" || m.ProfileID == "" || (ResourceActor{m.Authority, m.AccountID, m.ProfileID}) == a {
			return out, requestinput.New("invalid collection share")
		}
		if action == "share" {
			if m.Role != "viewer" && m.Role != "editor" {
				return out, requestinput.New("invalid share role")
			}
			var n int
			if e = tx.QueryRow(`SELECT count(*) FROM saved_resource_shares WHERE resource_id=? AND NOT(authority=? AND account_id=? AND profile_id=?)`, id, m.Authority, m.AccountID, m.ProfileID).Scan(&n); e != nil {
				return out, e
			}
			if n >= MaxPlaylistShares {
				return out, ErrPlaylistCapacity
			}
			_, e = tx.Exec(`INSERT INTO saved_resource_shares VALUES(?,?,?,?,?) ON CONFLICT(resource_id,authority,account_id,profile_id) DO UPDATE SET role=excluded.role`, id, m.Authority, m.AccountID, m.ProfileID, m.Role)
		} else {
			_, e = tx.Exec(`DELETE FROM saved_resource_shares WHERE resource_id=? AND authority=? AND account_id=? AND profile_id=?`, id, m.Authority, m.AccountID, m.ProfileID)
		}
	default:
		return out, requestinput.New("unsupported resource operation")
	}
	if e != nil {
		return out, e
	}
	if action != "create" {
		rev++
		if _, e = tx.Exec(`UPDATE saved_resources SET revision=? WHERE id=?`, rev, id); e != nil {
			return out, e
		}
	}
	out.Revision = rev
	if protocol {
		status := "applied"
		if out.Deleted {
			status = "deleted"
		} else if out.Entries != nil && len(out.Entries.Failed) > 0 {
			status = "partial"
		}
		if e = usermutation.SaveContext(s.Context(), tx, usermutation.Result{Status: status, TargetID: out.ResourceID, Revision: out.Revision}, time.Now().UnixMilli()); e != nil {
			return out, e
		}
		return out, gated.Commit()
	}
	if e = fenceOperation(tx, actorKey(a), "resource", m.OperationID, hash); e != nil {
		return out, e
	}
	b, _ := json.Marshal(out)
	if _, e = tx.Exec(`INSERT INTO saved_resource_receipts VALUES(?,?,?,?,?)`, actorKey(a), m.OperationID, hash, string(b), time.Now().UTC().Format(time.RFC3339)); e != nil {
		return out, e
	}
	return out, gated.Commit()
}

func (s *Service) SavedResourceReceiptStatus(id string, a ResourceActor) (bool, int64, error) {
	role, _, revision, deleted, e := savedResourceRole(s.read(), id, a)
	if e != nil {
		return false, 0, e
	}
	if deleted && role != "owner" {
		return false, 0, sql.ErrNoRows
	}
	return deleted, revision, nil
}
