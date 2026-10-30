package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// homeScope resolves the viewer, the libraries they may see, and the fence that
// identifies that authority. It is the first half of every composite catalogue
// read.
//
// It used to resolve the allowed-library set by asking the policy once per
// library, each question in its own read transaction. Nine libraries meant
// thirteen transactions and forty-nine statements — and every composite handler
// ran it twice. It now asks once for the whole set.
func (d Dependencies) homeScope(r *http.Request) (identity.Principal, []string, string, error) {
	p, e := d.principal(r)
	if e != nil {
		return p, nil, "", e
	}
	ids, fence, e := d.homeScopeFor(r, p)
	return p, ids, fence, e
}

// homeScopeFor is homeScope with the principal already resolved.
func (d Dependencies) homeScopeFor(r *http.Request, p identity.Principal) ([]string, string, error) {
	ids, fence, _, e := d.homeScopeFences(r, p)
	return ids, fence, e
}

// homeScopeFences is homeScopeFor plus the privacy fence: the same inputs without the
// allowed library ID list. The viewer fence changes whenever a library is added or
// removed; the privacy fence changes only when who the viewer is, or what they may see,
// changes (account epoch, policy, restrictions, content fence). Clients rebuild their
// whole shell on the privacy fence and merely refresh on the viewer fence.
func (d Dependencies) homeScopeFences(r *http.Request, p identity.Principal) ([]string, string, string, error) {
	var ids []string
	var fence, privacy string
	resolve := func(ctx context.Context) error {
		libraries, e := d.Catalog.WithContext(ctx).Libraries()
		if e != nil {
			return e
		}
		// The server-wide check ("") and every per-library check are one batch:
		// they are the same question asked of different arguments, and asking
		// them together is what makes the whole scope one read transaction.
		names := make([]string, 0, len(libraries)+1)
		names = append(names, "")
		for _, library := range libraries {
			names = append(names, library.ID)
		}
		allowed, e := d.allowedLibrarySet(ctx, p, names)
		if e != nil {
			return e
		}
		if !allowed[""] {
			return identity.ErrUnauthorized
		}
		ids = []string{}
		for _, library := range libraries {
			if allowed[library.ID] {
				ids = append(ids, library.ID)
			}
		}
		sort.Strings(ids)
		var policy, restriction int64
		if p.Authority != "local" {
			if e = d.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0),COALESCE((SELECT revision FROM restrictions WHERE profile_id=?),0)`, d.Identity.ID(), p.ProfileID).Scan(&policy, &restriction); e != nil {
				return e
			}
		}
		raw, _ := json.Marshal(ids)
		_, contentFence, e := d.viewerRestrictions(r.WithContext(ctx), p)
		if e != nil {
			return e
		}
		fence = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d:%s:%s", presentationIdentity(p), p.Epoch, policy, restriction, raw, contentFence))))
		privacy = homePrivacyFence(p, policy, restriction, contentFence)
		return nil
	}
	if d.DB == nil {
		e := resolve(r.Context())
		return ids, fence, privacy, e
	}
	if e := dbwork.WithReadSnapshot(r.Context(), d.DB, resolve); e != nil {
		return nil, "", "", e
	}
	return ids, fence, privacy, nil
}

// homePrivacyFence hashes every viewer-fence input except the library list, under its own
// label so it can never equal a viewer fence.
func homePrivacyFence(p identity.Principal, policy, restriction int64, contentFence string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("privacy:%s:%d:%d:%d:%s", presentationIdentity(p), p.Epoch, policy, restriction, contentFence))))
}

// homeFenceAfter is the post-read authorisation proof. The catalogue half of the
// old "resolve the whole scope again" check is satisfied by construction once
// the composition runs inside one read snapshot — a WAL snapshot cannot observe
// a write — but a revocation landing mid-request still has to be caught, so the
// authority half is re-resolved against live state and compared to the fence the
// response was composed under. Same 409, same condition, one resolution instead
// of a second full composition.
func (d Dependencies) homeFenceAfter(r *http.Request, p identity.Principal) (string, error) {
	_, fence, err := d.homeScopeFor(r, p)
	return fence, err
}

func (d Dependencies) serveGlobalSavedContent(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("view") {
	case "watchlist", "favorites", "playlists":
		d.savedContent(w, r)
	default:
		failure(w, requestinput.New("choose a saved list or playlist directory"))
	}
}

// compose runs a composite handler's body on one read snapshot, and hands the
// body a catalogue bound to it. Every read inside joins that snapshot, so a page
// is built from one pooled connection and one WAL frame boundary instead of one
// per query, and the rows cannot disagree with each other about which
// publication they came from.
// composeScope is compose with the caller's own preparation folded in: the
// resolution of the request's scope and the composition of its body run on one
// read snapshot instead of two.
//
// Pending ratings are classified by a background worker. Request composition
// opens one read snapshot and treats pending spellings as unrated.
func (d Dependencies) composeScope(r *http.Request, restrictions identity.ContentRestrictions, body func(context.Context, *catalog.Service) error) error {
	if err := d.Catalog.WithContext(r.Context()).PrepareRead(r.Context(), restrictions); err != nil {
		return err
	}
	if d.DB == nil {
		return body(r.Context(), d.Catalog.WithContext(r.Context()))
	}
	return dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		return body(ctx, d.Catalog.WithContext(ctx))
	})
}

func (d Dependencies) compose(r *http.Request, restrictions identity.ContentRestrictions, body func(*catalog.Service) error) error {
	return d.composeScope(r, restrictions, func(_ context.Context, cat *catalog.Service) error {
		return body(cat.WithRecommendationRestrictions(restrictions))
	})
}

func (d Dependencies) savedContent(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		if len(values) != 1 || (key != "view" && key != "limit" && key != "sort" && key != "direction" && key != "cursor" && key != "filter" && key != "shape") {
			failure(w, catalog.ErrCursor)
			return
		}
	}
	limit := 40
	if q.Has("limit") {
		var e error
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil || limit < 1 || limit > 100 {
			failure(w, catalog.ErrCursor)
			return
		}
	}
	p, libraries, fence, e := d.homeScope(r)
	if e != nil {
		failure(w, e)
		return
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	viewer, e := d.catalogViewer(r, p, libraries, fence)
	if e != nil {
		failure(w, e)
		return
	}
	request := catalog.ContentRequest{Viewer: viewer, ServerID: d.Identity.ID(), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, View: q.Get("view"), Sort: q.Get("sort"), Direction: q.Get("direction"), Filter: q.Get("filter"), Shape: q.Get("shape"), Cursor: q.Get("cursor"), Limit: limit, Restrictions: restrictions}
	var out catalog.SavedContentEnvelope
	e = d.compose(r, restrictions, func(cat *catalog.Service) error {
		var composeErr error
		if q.Get("view") == "playlists" {
			out.ContentEnvelope, composeErr = cat.PlaylistDirectory(request, resourceActor(p))
			if composeErr == nil {
				for i := range out.Sections {
					if composeErr = cat.ProjectPlaylistSamples(viewer, out.Sections[i].Entries); composeErr != nil {
						break
					}
				}
			}
		} else {
			var admit catalog.SavedGroupAdmission
			if identity.CanUsePersonalState(p) {
				admit = d.savedReadingGroupsAdmission(p, viewer)
			}
			out, composeErr = cat.SavedWithReadingGroups(request, libraries, admit)
			if composeErr == nil {
				entries := savedContentEntries(&out.ContentEnvelope)
				for i := range out.SavedSections {
					for j := range out.SavedSections[i].Cards {
						if entry := out.SavedSections[i].Cards[j].Entry; entry != nil {
							entries = append(entries, entry)
						}
					}
				}
				composeErr = d.projectSavedActions(cat.Context(), p, viewer, cat, entries)
			}
		}
		return composeErr
	})
	if e != nil {
		failure(w, e)
		return
	}
	after, e := d.homeActionFenceAfter(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	if after != fence {
		failure(w, catalog.ErrStaleContinuation)
		return
	}
	write(w, 200, out)
}
