package catalog

import (
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The whole-selection aggregate backs the drawn "Select all N" for one library
// view. It describes the same authorized result the page returned, at the same
// revision, so a client can offer a whole-view action without inventing a count
// from the loaded window.

// BoundBrowseQuery is the exact WHERE a browse page was read with: the
// authorized library scope, the client's own expression and the viewer's
// restriction predicate, in that order. The page carries it out of the read so
// the aggregate cannot re-derive (and drift from) the page's own query.
type BoundBrowseQuery struct {
	Where    string
	Args     []any
	Library  string
	Pivot    string
	Sorts    []BrowseSortSelection
	Query    *BrowseNode
	Revision string
	// snapshot is the read transaction the page was composed in. The aggregate
	// refuses to answer from any other state, so a carried Bound cannot publish
	// stale counts under an old revision.
	snapshot     *sql.Tx
	orderInstant time.Time
}

// SelectionCapabilityCounts is the number of displayed titles of one kind for
// which each existing selection action is genuinely offered. Every count is
// exact, non-negative and never larger than that kind's count.
type SelectionCapabilityCounts struct {
	Play       int `json:"play"`
	Queue      int `json:"queue"`
	Playlist   int `json:"playlist"`
	MyList     int `json:"myList"`
	Watched    int `json:"watched"`
	Collection int `json:"collection"`
	Favorite   int `json:"favorite"`
	Download   int `json:"download"`
	Edit       int `json:"edit"`
	Refresh    int `json:"refresh"`
	Delete     int `json:"delete"`
}

type BrowseSelectionLibrary struct {
	LibraryID string `json:"libraryId"`
	Count     int    `json:"count"`
}

type BrowseSelectionType struct {
	Kind             string                    `json:"kind"`
	Count            int                       `json:"count"`
	CapabilityCounts SelectionCapabilityCounts `json:"capabilityCounts"`
	Libraries        []BrowseSelectionLibrary  `json:"libraries"`
}

// BrowseSelectionSelector is deliberately query-only: this aggregate describes a
// library view, and the existing job selector's container/items forms cannot.
type BrowseSelectionSelector struct {
	Query JobQuery `json:"query"`
}

// BrowseSelectionDescriptor is the optional additive field on a browse page.
// Selector reuses the existing job query wrapper exactly, so a client can hand
// it to the job API unchanged.
type BrowseSelectionDescriptor struct {
	Selector           BrowseSelectionSelector            `json:"selector"`
	CatalogRevision    string                             `json:"catalogRevision"`
	Total              int                                `json:"total"`
	Types              []BrowseSelectionType              `json:"types"`
	UnavailableActions *BrowseSelectionUnavailableActions `json:"unavailableActions,omitempty"`
}

// Consumer support is separate from truthful per-title capabilities.
type BrowseSelectionUnavailableActions struct {
	Play            string `json:"play,omitempty"`
	Queue           string `json:"queue,omitempty"`
	Playlist        string `json:"playlist,omitempty"`
	Delete          string `json:"delete,omitempty"`
	MetadataPreview string `json:"metadataPreview,omitempty"`
}

func (BrowseSelectionUnavailableActions) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"play":            map[string]any{"type": "string", "enum": []string{"unsupported-sort"}},
		"queue":           map[string]any{"type": "string", "enum": []string{"unsupported-sort"}},
		"playlist":        map[string]any{"type": "string", "enum": []string{"unsupported-sort"}},
		"delete":          map[string]any{"type": "string", "enum": []string{"unsupported-sort"}},
		"metadataPreview": map[string]any{"type": "string", "enum": []string{"preview-limit"}},
	}}
}

// selectionAggregateKindOrder is the wire order: the persisted compact-kind
// enum order, never count order or map iteration.
var selectionAggregateKindOrder = []string{
	"movie", "show", "season", "episode", "artist", "album", "song", "book",
	"audiobook_file", "collection", "extra", "disc", "author",
}

// selectionAggregateLibraries are the library kinds whose job-selector replay
// is already proven: the existing browse, bulk and delete paths compile the same
// expression for them. A new library kind (for example a Reading library) is
// omitted until its selector, revision and action equivalence is covered.
var selectionAggregateLibraries = map[string]bool{"movie": true, "tv": true, "anime": true, "music": true, "audiobook": true}

// browseQueryReplayable reports whether this view can be replayed exactly by the
// job compiler. Relative-time predicates and seeded random orders would match
// different titles when the job runs, and a Home-row condition is not
// expressible in a job selector at all.
func browseQueryReplayable(node *BrowseNode, sorts []BrowseSortSelection) bool {
	for _, entry := range sorts {
		if entry.Field == "random" || entry.Seed != 0 {
			return false
		}
	}
	return browseNodeReplayable(node)
}

func browseNodeReplayable(node *BrowseNode) bool {
	if node == nil {
		return true
	}
	if node.Operator == "within-last-days" || node.Field == "homeRow" {
		return false
	}
	for _, child := range node.All {
		if !browseNodeReplayable(&child) {
			return false
		}
	}
	for _, child := range node.Any {
		if !browseNodeReplayable(&child) {
			return false
		}
	}
	return node.Not == nil || browseNodeReplayable(node.Not)
}

type selectionAggregateGroup struct {
	count     int
	available int
	delete    int
	libraries map[string]int
}

// BrowseSelectionAggregate answers the exact count and per-action counts for the
// bound browse query, inside the caller's read snapshot. authority is the same
// live command authority the page's own homeActions.Selection were projected
// with, so the aggregate cannot offer an action the entries do not.
func (s *Service) BrowseSelectionAggregate(request BrowseRequest, viewer Viewer, bound BoundBrowseQuery, authority HomeActionAuthority) (BrowseSelectionDescriptor, error) {
	out := BrowseSelectionDescriptor{Types: []BrowseSelectionType{}}
	if bound.Where == "" {
		return out, nil
	}
	// The aggregate must be read in the very snapshot the page was composed in:
	// Bound, the authority and the restriction predicate all belong to that
	// state, and any other snapshot could mix in a later one.
	current := dbwork.Snapshot(s.Context())
	if current == nil || bound.snapshot == nil || current != bound.snapshot {
		return out, sql.ErrTxDone
	}
	lib, err := s.library(bound.Library)
	if err != nil {
		return out, err
	}
	pivot, ok := pivotForKind(lib.Kind, bound.Pivot)
	if !ok || !selectionLeafPivot(pivot) {
		// A container pivot's job replay expands to its members, so the drawn
		// "select all N" would not act on the N titles shown. Omit it until that
		// action equivalence is proven, and never advertise a merely-compiled one.
		return out, nil
	}
	if !selectionAggregateLibraries[lib.Kind] || !browseQueryReplayable(bound.Query, bound.Sorts) || len(request.Rows) > 0 || len(request.RowSources) > 0 {
		return out, nil
	}
	// Availability is the entries' own predicate, not the global asset column: a
	// container counts as available only through a member this viewer may see.
	availableSQL, availableArgs := homeActionAvailableSQLFor(viewer, "e.entity_id")
	args := append([]any{}, availableArgs...)
	args = append(args, bound.Args...)
	rows, err := s.read().Query(`SELECT `+compactBrowseKindSQL("e.kind")+`,l.library_id,count(*),`+
		`COALESCE(sum(CASE WHEN `+availableSQL+` THEN 1 ELSE 0 END),0)`+
		` FROM catalog_browse_rows e`+
		` JOIN catalog_libraries l ON l.id=e.library_id`+
		` JOIN catalog_kinds k ON k.id=e.kind`+
		` LEFT JOIN catalog_item_availability a ON a.entity_id=e.entity_id AND a.retired=0`+
		` WHERE `+bound.Where+` GROUP BY e.kind,l.library_id`, args...)
	if err != nil {
		return out, err
	}
	groups := map[string]*selectionAggregateGroup{}
	for rows.Next() {
		var kind, library string
		var count, available int
		if err = rows.Scan(&kind, &library, &count, &available); err != nil {
			rows.Close()
			return out, err
		}
		group, ok := groups[kind]
		if !ok {
			group = &selectionAggregateGroup{libraries: map[string]int{}}
			groups[kind] = group
		}
		group.count += count
		group.available += available
		group.libraries[library] += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return selectionAggregateDescriptor(bound, authority, groups)
}

func selectionAggregateDescriptor(bound BoundBrowseQuery, authority HomeActionAuthority, groups map[string]*selectionAggregateGroup) (BrowseSelectionDescriptor, error) {
	out := BrowseSelectionDescriptor{Types: []BrowseSelectionType{}}
	var err error
	// Delete is offered per library by the real administration setting, exactly
	// as the entries' own selection block reads it.
	deletion := map[string]bool{}
	for _, kind := range selectionAggregateKindOrder {
		group, ok := groups[kind]
		if !ok || group.count == 0 {
			continue
		}
		if authority.DeleteLibrary != nil && selectionKindDeletable(kind) {
			for library, count := range group.libraries {
				allowed, known := deletion[library]
				if !known {
					allowed, err = authority.DeleteLibrary(library)
					if err != nil {
						return out, err
					}
					deletion[library] = allowed
				}
				if allowed {
					group.delete += count
				}
			}
		}
		offers := selectionOffers(authority, kind, true, group.delete > 0)
		capabilities := selectionOfferedCounts(offers, group.count, group.available, group.delete)
		libraries := make([]BrowseSelectionLibrary, 0, len(group.libraries))
		for library, count := range group.libraries {
			libraries = append(libraries, BrowseSelectionLibrary{LibraryID: library, Count: count})
		}
		sort.Slice(libraries, func(i, j int) bool { return libraries[i].LibraryID < libraries[j].LibraryID })
		out.Total += group.count
		out.Types = append(out.Types, BrowseSelectionType{Kind: kind, Count: group.count, CapabilityCounts: capabilities, Libraries: libraries})
	}
	selector := JobQuery{LibraryID: bound.Library, Pivot: bound.Pivot, Sort: bound.Sorts}
	if bound.Query != nil {
		raw, marshalErr := json.Marshal(bound.Query)
		if marshalErr != nil {
			return out, marshalErr
		}
		selector.Filter = raw
	}
	out.Selector = BrowseSelectionSelector{Query: selector}
	out.CatalogRevision = bound.Revision
	return out, nil
}

// selectionLeafPivot is true only when every displayed kind is a leaf, where the
// job selector's member expansion is the identity.
func selectionLeafPivot(pivot browsePivot) bool {
	if len(pivot.EntityKinds) == 0 {
		return false
	}
	for _, kind := range pivot.EntityKinds {
		if !selectionLeafKind(kind) {
			return false
		}
	}
	return true
}

// selectionOfferedCounts applies the shared offer rule to one kind's totals:
// availability-gated actions count only the available titles, everything else
// counts every displayed title, and Delete uses its per-library sum.
func selectionOfferedCounts(offers HomeSelectionActions, total, available, deletable int) SelectionCapabilityCounts {
	count := func(offered bool, value int) int {
		if offered {
			return value
		}
		return 0
	}
	return SelectionCapabilityCounts{
		Play:       count(offers.Play, available),
		Queue:      count(offers.Queue, available),
		Playlist:   count(offers.Playlist, total),
		MyList:     count(offers.MyList, total),
		Watched:    count(offers.Watched, total),
		Collection: count(offers.Collection, total),
		Favorite:   count(offers.Favorite, total),
		Download:   count(offers.Download, available),
		Edit:       count(offers.Edit, total),
		Refresh:    count(offers.Refresh, total),
		Delete:     count(offers.Delete, deletable),
	}
}

func selectionKindDeletable(kind string) bool {
	switch kind {
	case "movie", "episode", "song", "audiobook_file", "show", "season", "album", "artist", "book":
		return true
	}
	return false
}

// selectionOffers is the one selection offer rule. The per-entry projection and
// the whole-view aggregate both call it, so an action can never be offered for a
// whole view that the same entries do not offer.
func selectionOffers(authority HomeActionAuthority, kind string, available, deletable bool) HomeSelectionActions {
	leaf := selectionLeafKind(kind)
	container := selectionContainerKind(kind)
	// Kinds the projection skips (an author, a disc, an extra) never offer a
	// selection action, whatever the grants.
	if !leaf && !container {
		return HomeSelectionActions{}
	}
	play := authority.Playback && available
	queue := authority.Queue && available
	playlist := authority.Organize && (leaf || container && authority.Bulk)
	collection := authority.Organize && (leaf || container && authority.Bulk)
	favorite := authority.Personal && (leaf || containerSaves(kind, "favorite"))
	watched := authority.Personal && (kind == "movie" || kind == "episode" || kind == "show" || kind == "season")
	download := authority.Downloads && available && (leaf || kind == "show" || kind == "season" || kind == "album" || kind == "book")
	return HomeSelectionActions{
		Play: play, Queue: queue,
		Playlist: playlist && authority.Bulk, Collection: collection && authority.Bulk,
		MyList: authority.Personal && authority.Bulk,
		// The canonical selection dispatcher uses jobs for leaf personal
		// commands and refresh. Supported container favorite/watched have
		// genuine direct commands independent of the job scheduler.
		Favorite: favorite && (container || authority.Bulk),
		Watched:  watched && (container || authority.Bulk),
		Download: download,
		Edit:     authority.Metadata && (leaf || kind == "show" || kind == "season" || kind == "album" || kind == "artist" || kind == "book"),
		Refresh:  authority.Refresh && (leaf || kind == "show") && authority.Bulk,
		Delete:   deletable,
	}
}

// selectionLeafKind mirrors the projection's own leaf test.
func selectionLeafKind(kind string) bool {
	return kind == "movie" || kind == "episode" || kind == "song" || kind == "audiobook_file"
}

func selectionContainerKind(kind string) bool {
	return kind == "show" || kind == "season" || kind == "album" || kind == "artist" || kind == "book" || kind == "collection"
}
