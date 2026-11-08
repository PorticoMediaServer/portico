package read

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// A new composition around the frozen manifest/saved-view readers. The accepted
// settings bridge imports file values into the normalized stored document.
// This reads that last COMMITTED policy, never pretends to observe disk edits.
type HomeManifestSourceReader struct{ base *HomeManifestReader }

func NewHomeManifestSourceReader(pool *governor.Snapshots) (*HomeManifestSourceReader, error) {
	r, e := NewHomeManifestReader(pool)
	if e != nil {
		return nil, e
	}
	return &HomeManifestSourceReader{base: r}, nil
}

type HomeManifestCommittedPolicy struct {
	Field, Provenance, Reason        string
	Enabled, Resolved, CommittedOnly bool
	ConsoleRevision, PendingRevision *int64
}
type HomeManifestSourceDocument struct {
	Manifest        HomeManifestDocument
	CommunityPolicy HomeManifestCommittedPolicy
}
type homeManifestSourceFrame struct {
	Manifest homeManifestFrame
	Pending  [][]any
}

func homeManifestPolicy(frame homeManifestSourceFrame) HomeManifestCommittedPolicy {
	out := HomeManifestCommittedPolicy{Field: "home.communityActivityEnabled", Provenance: "unresolved", CommittedOnly: true}
	data := frame.Manifest
	if len(data.SettingsRevision) == 1 {
		v := data.SettingsRevision[0][0].(int64)
		if v > 0 {
			out.ConsoleRevision = &v
		}
	}
	if len(frame.Pending) == 1 {
		v := frame.Pending[0][0].(int64)
		out.PendingRevision = &v
	}
	source := ""
	if len(data.CommunitySource) == 1 {
		source = data.CommunitySource[0][0].(string)
	}
	var value any
	if len(data.Community) == 1 {
		value = data.Community[0][1]
	}
	if value == nil {
		if source != "" || out.PendingRevision != nil {
			out.Reason = "stored_source_has_no_typed_committed_value"
			return out
		}
		// Accepted DefaultSettings has this boolean false; this says nothing about
		// a disk edit that has not yet passed through the settings import boundary.
		out.Resolved = true
		out.Provenance = "registry_default"
		return out
	}
	if value != int64(0) && value != int64(1) {
		out.Reason = "invalid_stored_boolean"
		return out
	}
	if out.ConsoleRevision == nil {
		out.Reason = "committed_settings_revision_missing"
		return out
	}
	if out.PendingRevision != nil && (*out.PendingRevision <= 0 || *out.PendingRevision > *out.ConsoleRevision) {
		out.Reason = "pending_settings_revision_inconsistent"
		return out
	}
	out.Enabled = value == int64(1)
	out.Resolved = true
	switch source {
	case "file":
		out.Provenance = "committed_file_import"
	case "server":
		out.Provenance = "committed_server"
	case "":
		out.Provenance = "committed_unattributed"
	default:
		out.Resolved = false
		out.Reason = "unknown_stored_source"
		return out
	}
	if out.PendingRevision != nil {
		out.Provenance = "committed_pending_file_projection"
	}
	return out
}
func homeManifestSourceFrameRead(s *savedViewSession, q browseQuery, in BrowseInput) (homeManifestSourceFrame, error) {
	var principal publicationPrincipalPin
	return homeManifestSourcePinnedFrameRead(s, q, in, &principal)
}
func homeManifestSourcePinnedFrameRead(s *savedViewSession, q browseQuery, in BrowseInput, principal *publicationPrincipalPin) (homeManifestSourceFrame, error) {
	frame, e := homeManifestPinnedFrameRead(s, q, in, principal)
	if e != nil {
		return homeManifestSourceFrame{}, e
	}
	pending, e := q("rb.home.manifest.sources.pending", "home.communityActivityEnabled")
	if e != nil {
		return homeManifestSourceFrame{}, e
	}
	if len(pending) > 1 {
		return homeManifestSourceFrame{}, ErrUnqualified
	}
	return homeManifestSourceFrame{Manifest: frame, Pending: pending}, nil
}
func homeManifestSourceProject(frame homeManifestSourceFrame, at time.Time) HomeManifestSourceDocument {
	policy := homeManifestPolicy(frame)
	doc := homeManifestProject(frame.Manifest, at)
	if doc.Pending {
		return HomeManifestSourceDocument{doc, policy}
	}
	// Copy the projection, never replace real source='file' facts with a forged
	// source='server' row to trick the old projector. Its frame remains untouched.
	for n := range doc.Catalogue {
		row := &doc.Catalogue[n]
		if row.ID != "community_watching" {
			continue
		}
		row.PolicyState = "unresolved"
		row.Reason = "committed_owner_policy_unresolved"
		if policy.Resolved {
			row.PolicyState = "disabled_by_owner"
			if policy.Enabled {
				row.PolicyState = "available"
			}
			row.Reason = "community_source_not_attached"
		}
	}
	doc.LayoutRows = nil
	doc.Rows = nil
	for _, row := range doc.Catalogue {
		if row.PolicyState != "available" {
			continue
		}
		doc.LayoutRows = append(doc.LayoutRows, row)
		if row.Hidden {
			continue
		}
		if len(doc.Rows) == 64 {
			if !row.Required {
				continue
			}
			for n := len(doc.Rows) - 1; n >= 0; n-- {
				if !doc.Rows[n].Required {
					doc.Rows = append(doc.Rows[:n], doc.Rows[n+1:]...)
					break
				}
			}
		}
		doc.Rows = append(doc.Rows, row)
	}
	return HomeManifestSourceDocument{doc, policy}
}
func (r *HomeManifestSourceReader) Manifest(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in HomeManifestInput) (HomeManifestSourceDocument, BrowseReadResult, error) {
	if r == nil || r.base == nil || in.View.After != nil {
		return HomeManifestSourceDocument{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.base.native.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeManifestSourceDocument, BrowseReadResult, error) {
		in.View.Now = at
		s, e := newSavedViewSession(r.base.native, ctx, request, budget, out, in.View)
		if e != nil {
			return HomeManifestSourceDocument{}, BrowseReadResult{}, e
		}
		if e = s.reserve(1 << 20); e != nil {
			return HomeManifestSourceDocument{}, s.result(), e
		}
		var principal publicationPrincipalPin
		for attempt := 0; attempt < 2; attempt++ {
			s.cost.Attempts = attempt + 1
			var first homeManifestSourceFrame
			e = s.window(false, homeManifestQueries+1, func(q browseQuery) error {
				var e error
				first, e = homeManifestSourcePinnedFrameRead(s, q, in.View, &principal)
				return e
			})
			if e == nil {
				var doc HomeManifestSourceDocument
				if e = s.reserve(2048 * int64(12+len(first.Manifest.ViewDescriptors))); e == nil {
					doc = homeManifestSourceProject(first, s.input(in.View).Now)
				}
				prior := e
				final := s.window(true, homeManifestQueries+1, func(q browseQuery) error {
					current, e := homeManifestSourcePinnedFrameRead(s, q, in.View, &principal)
					if e != nil {
						return e
					}
					if !reflect.DeepEqual(current, first) {
						return ErrStale
					}
					return nil
				})
				e = s.finish(first.Manifest.Principal, prior, final)
				if e == nil {
					return doc, s.result(), nil
				}
			}
			if !errors.Is(e, ErrStale) || ctx.Err() != nil || !time.Now().Before(s.deadline) {
				break
			}
		}
		if errors.Is(e, ErrStale) {
			s.cost.Refusal = &governor.Refusal{Code: governor.Conflict}
			e = errors.Join(e, governor.ErrBusy)
		}
		return HomeManifestSourceDocument{}, s.result(), e
	})
}

type HomeManifestContentInput struct {
	View  BrowseInput
	RowID string
	After *HomeManifestContentCursor
}
type HomeManifestContentItem struct {
	BrowseItem
	Personal PersonalState
}
type HomeManifestContentPage struct {
	Items                 []HomeManifestContentItem
	Next                  *HomeManifestContentCursor
	RawRows, RawPositions int
	Exhausted             bool
	RowID                 string
	ResourceRevision      int64
}
type HomeManifestContentCursor struct {
	row      string
	context  homeManifestSourceFrame
	resource savedViewResource
	browse   *BrowseCursor
}

func homeManifestPersonal(q browseQuery, view BrowseInput, item BrowseItem, f browseFence) (PersonalState, [][]any, error) {
	rows, e := q("foreground.uo.personal.point", item.ID, view.TokenHash, view.Library, view.Kind)
	if e != nil {
		return PersonalState{}, nil, e
	}
	if len(rows) != 1 || len(rows[0]) != 18 || !admittedItem(rows[0][:7], f) {
		return PersonalState{}, nil, ErrDenied
	}
	row := rows[0]
	public, ok := row[2].([]byte)
	if !ok || len(public) != 16 || string(public) != string(item.PublicID[:]) {
		return PersonalState{}, nil, ErrStale
	}
	state := PersonalState{PublicID: item.PublicID}
	if row[7] != nil {
		personal, ok := row[17].([]byte)
		if !ok || len(personal) != 16 || string(personal) != string(public) {
			return PersonalState{}, nil, ErrUnqualified
		}
		state.UserState = stateValue(row[7:15])
		state.NotInterested = row[15].(int64) != 0
		if row[16] != nil {
			v := row[16].(int64)
			state.Rating = &v
		}
	}
	return state, rows, nil
}

// Content integrates ONLY actual currently supported saved-view rows with the
// merged UO scalar-state point. Fixed/recommendation/live/continue families have
// no qualified Home composition recipe here and return ErrUnqualified, never a
// successful empty shelf. No permission callback or source selection is accepted.
func (r *HomeManifestSourceReader) Content(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in HomeManifestContentInput) (HomeManifestContentPage, BrowseReadResult, error) {
	if r == nil || r.base == nil || in.View.After != nil || in.RowID == "" || !utf8.ValidString(in.RowID) || utf8.RuneCountInString(in.RowID) > 128 {
		return HomeManifestContentPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.base.native.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeManifestContentPage, BrowseReadResult, error) {
		in.View.Now = at
		s, e := newSavedViewSession(r.base.native, ctx, request, budget, out, in.View)
		if e != nil {
			return HomeManifestContentPage{}, BrowseReadResult{}, e
		}
		// Bounded decoder scratch plus one retained closed filter, reused under one
		// hold across every frame/window/retry. Actual copied strings charge separately.
		if e = s.reserve(2 << 20); e != nil {
			return HomeManifestContentPage{}, s.result(), e
		}
		var principal publicationPrincipalPin
		for attempt := 0; attempt < 2; attempt++ {
			s.cost.Attempts = attempt + 1
			var first homeManifestSourceFrame
			var page HomeManifestContentPage
			var resource savedViewResource
			var fence browseFence
			var filter rbScalarFilter
			effective := in.View
			e = s.window(false, homeManifestQueries+1, func(q browseQuery) error {
				var e error
				first, e = homeManifestSourcePinnedFrameRead(s, q, in.View, &principal)
				if e != nil {
					return e
				}
				if first.Manifest.Pending {
					return ErrNotReady
				}
				doc := homeManifestSourceProject(first, s.input(in.View).Now)
				var row *HomeManifestDescriptor
				for n := range doc.Manifest.Catalogue {
					if doc.Manifest.Catalogue[n].ID == in.RowID {
						row = &doc.Manifest.Catalogue[n]
						break
					}
				}
				if row == nil {
					return ErrDenied
				}
				if row.ReadState != "deferred" || row.Kind != "saved_view" {
					return ErrUnqualified
				}
				visible := false
				for _, shown := range doc.Manifest.Rows {
					if shown.ID == in.RowID {
						visible = true
						break
					}
				}
				if !visible {
					return ErrDenied
				}
				for _, fact := range first.Manifest.Views {
					if fact.ID == in.RowID && fact.State == "current" {
						resource = fact.Resource
						fence = fact.Fence
						break
					}
				}
				if resource.id == "" || resource.id != strings.TrimPrefix(in.RowID, "view:") {
					return ErrDenied
				}
				effective.Library = fence.libraryID
				effective.Kind = fence.kind
				effective.After = nil
				effective.Descending = fence.descending
				effective.KeepArticles = fence.keepArticles
				resource.fence = fence
				effective, filter, e = savedViewExecutionPlan(resource, effective)
				if e != nil {
					return e
				}
				fence.descending, fence.keepArticles = effective.Descending, effective.KeepArticles
				resource.fence = fence
				if in.After != nil {
					if in.After.row != in.RowID || in.After.resource != resource || !reflect.DeepEqual(in.After.context, first) || in.After.browse == nil || in.After.browse.fence != fence {
						return ErrStale
					}
					effective.After = in.After.browse
				}
				return nil
			})
			ready := e == nil
			var personal [][][]any
			if ready {
				e = s.window(false, MaxPage+2, func(q browseQuery) error {
					raw, e := readSavedViewScalarPage(ctx, q, effective, fence, filter)
					if e != nil {
						return e
					}
					page = HomeManifestContentPage{RawRows: raw.RawRows, RawPositions: raw.RawPositions, Exhausted: raw.Exhausted, RowID: in.RowID, ResourceRevision: resource.revision}
					for _, item := range raw.Items {
						state, proof, e := homeManifestPersonal(q, effective, item, fence)
						if e != nil {
							return e
						}
						personal = append(personal, proof)
						page.Items = append(page.Items, HomeManifestContentItem{item, state})
					}
					if raw.Next != nil {
						page.Next = &HomeManifestContentCursor{row: in.RowID, context: first, resource: resource, browse: raw.Next}
					}
					return nil
				})
			}
			prior := e
			if ready {
				final := s.window(true, homeManifestQueries+1+MaxPage, func(q browseQuery) error {
					current, e := homeManifestSourcePinnedFrameRead(s, q, in.View, &principal)
					if e != nil {
						return e
					}
					if !reflect.DeepEqual(current, first) {
						return ErrStale
					}
					for n, item := range page.Items {
						_, proof, e := homeManifestPersonal(q, effective, item.BrowseItem, fence)
						if e != nil {
							return e
						}
						if !reflect.DeepEqual(proof, personal[n]) {
							return ErrStale
						}
					}
					return nil
				})
				e = s.finish(first.Manifest.Principal, prior, final)
			}
			if e == nil {
				return page, s.result(), nil
			}
			if !errors.Is(e, ErrStale) || ctx.Err() != nil || !time.Now().Before(s.deadline) {
				break
			}
		}
		if errors.Is(e, ErrStale) {
			s.cost.Refusal = &governor.Refusal{Code: governor.Conflict}
			e = errors.Join(e, governor.ErrBusy)
		}
		return HomeManifestContentPage{}, s.result(), e
	})
}
func rbHomeManifestSourceDefinitions() []stmt.Definition {
	field := stmt.AutoIndexBinding{Table: "server_configuration_pending", Name: "sqlite_autoindex_server_configuration_pending_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "field", Collation: "BINARY"}}}
	return []stmt.Definition{{ID: readStatementID("rb.home.manifest.sources.pending"), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_manifest_sources.go"}, SQL: "SELECT revision FROM server_configuration_pending INDEXED BY sqlite_autoindex_server_configuration_pending_1 WHERE field=?", Params: []stmt.Param{{Name: "field", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"home.communityActivityEnabled"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{field.Name}, AutoIndexes: []stmt.AutoIndexBinding{field}, Seeks: []string{"field=?"}}}}
}
