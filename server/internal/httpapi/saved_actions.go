package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Project only the catalogue members already admitted by this Saved read. The
// caller owns composition and the live post-read command-family fence.
func (d Dependencies) projectSavedActions(ctx context.Context, p identity.Principal, viewer catalog.Viewer, cat *catalog.Service, entries []*catalog.ContentEntry) error {
	pointers := make([]*catalog.ContentEntry, 0, len(entries))
	values := make([]catalog.ContentEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Kind == "person" || entry.Kind == "channel" || entry.Kind == "playlist" || entry.Kind == "playlist_entry" || (entry.Hidden != nil && *entry.Hidden) {
			continue
		}
		pointers = append(pointers, entry)
		values = append(values, *entry)
	}
	rows := []catalog.HomeRow{{Entries: values}}
	if err := d.projectHomeActions(ctx, p, viewer, cat, rows, nil); err != nil {
		return err
	}
	for i, entry := range pointers {
		entry.HomeActions = rows[0].Entries[i].HomeActions
	}
	return nil
}

// projectContainerActions offers the aggregate selection commands on the
// displayed containers (show, season, album, artist, book, collection) of a
// read that the caller composed and fences after (MOB-030). Leaves keep their
// own Detail actions, so they are left untouched. A library page's entries may
// leave their library implied by the route; library names it.
func (d Dependencies) projectContainerActions(ctx context.Context, p identity.Principal, viewer catalog.Viewer, cat *catalog.Service, library string, entries []*catalog.ContentEntry) error {
	containers, shadows := []*catalog.ContentEntry{}, []catalog.ContentEntry{}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		switch entry.Kind {
		case "show", "season", "album", "artist", "book", "collection":
			containers = append(containers, entry)
			shadow := *entry
			if shadow.LibraryID == "" {
				shadow.LibraryID = library
			}
			shadows = append(shadows, shadow)
		}
	}
	if len(containers) == 0 {
		return nil
	}
	pointers := make([]*catalog.ContentEntry, len(shadows))
	for i := range shadows {
		pointers[i] = &shadows[i]
	}
	if err := d.projectSavedActions(ctx, p, viewer, cat, pointers); err != nil {
		return err
	}
	for i, entry := range containers {
		entry.HomeActions = shadows[i].HomeActions
	}
	return nil
}

// containerOffersFenceBefore is the viewer fence a page carrying container
// offers is read under; containerOffersFenceAfter proves it held, and that the
// command family was not revoked, once the page is composed.
func (d Dependencies) containerOffersFenceBefore(r *http.Request, p identity.Principal) (string, error) {
	return d.homeFenceAfter(r, p)
}

func (d Dependencies) containerOffersFenceAfter(r *http.Request, p identity.Principal, before string) error {
	after, err := d.homeActionFenceAfter(r, p)
	if err != nil {
		return err
	}
	if after != before {
		return catalog.ErrStaleContinuation
	}
	return nil
}

// containerOffersValidator names the command authority a page's offers were
// drawn from, so a conditional read revalidates when a grant changes (a
// profile's downloads, the owner's deletion policy for the library).
func (d Dependencies) containerOffersValidator(r *http.Request, p identity.Principal, library string) string {
	if d.DB == nil || p.Authority != "local" {
		return "offers:none"
	}
	out := "offers:"
	err := dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		authority, err := d.homeActionAuthority(ctx, p)
		if err != nil {
			return err
		}
		deletable := false
		if authority.DeleteLibrary != nil && library != "" {
			if deletable, err = authority.DeleteLibrary(library); err != nil {
				return err
			}
		}
		out += fmt.Sprint(authority.Personal, authority.Organize, authority.Bulk, authority.Playback, authority.Queue, authority.Metadata, authority.Refresh, authority.Downloads, authority.ReadingCollections, deletable)
		return nil
	})
	if err != nil {
		// No validator: the page is composed, never answered from a cache.
		return "offers:" + gridProcessNonce + ":" + fmt.Sprint(time.Now().UnixNano())
	}
	return out
}

func savedContentEntries(out *catalog.ContentEnvelope) []*catalog.ContentEntry {
	entries := []*catalog.ContentEntry{}
	for i := range out.Sections {
		for j := range out.Sections[i].Entries {
			entry := &out.Sections[i].Entries[j]
			if entry.Kind == "playlist_entry" {
				if entry.Hidden == nil || !*entry.Hidden {
					entries = append(entries, entry.Media)
				}
			} else {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}
