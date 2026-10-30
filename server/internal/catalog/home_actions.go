package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// HomeActions names implemented, authorized commands for the displayed entity.
// Device support is deliberately separate; these facts never grant a write.
// Every command rechecks its own authority and current target on execution.
type HomeActions struct {
	NotInterested      bool                 `json:"notInterested,omitempty"`
	RemoveFromContinue bool                 `json:"removeFromContinue,omitempty"`
	Play               bool                 `json:"play"`
	Queue              bool                 `json:"queue"`
	Playlist           bool                 `json:"playlist"`
	Collection         bool                 `json:"collection"`
	MyList             bool                 `json:"myList"`
	Favorite           bool                 `json:"favorite"`
	Watched            bool                 `json:"watched"`
	Played             bool                 `json:"played"`
	Rating             bool                 `json:"rating"`
	EditMetadata       bool                 `json:"editMetadata"`
	RefreshMetadata    bool                 `json:"refreshMetadata"`
	Delete             bool                 `json:"delete"`
	Download           bool                 `json:"download"`
	Details            bool                 `json:"details"`
	State              HomeActionState      `json:"state"`
	Selection          HomeSelectionActions `json:"selection"`
}

// HomeSelectionActions is the authority for selection commands, independently
// of the title-level toggles above. In particular a container's bulk My List
// command changes its permitted playable members, not its own saved flag.
type HomeSelectionActions struct {
	Play       bool `json:"play"`
	Queue      bool `json:"queue"`
	Playlist   bool `json:"playlist"`
	Collection bool `json:"collection"`
	MyList     bool `json:"myList"`
	Favorite   bool `json:"favorite"`
	Watched    bool `json:"watched"`
	Download   bool `json:"download"`
	Edit       bool `json:"edit"`
	Refresh    bool `json:"refresh"`
	Delete     bool `json:"delete"`
}

type HomeActionState struct {
	Watchlisted bool     `json:"watchlisted"`
	Favorite    bool     `json:"favorite"`
	Watched     bool     `json:"watched"`
	Rating      *float64 `json:"rating"`
	// PersonalRevision belongs to the displayed entity, never its play target.
	PersonalRevision  int64 `json:"personalRevision"`
	ContainerRevision int64 `json:"containerRevision"`
}

// HomeChannelActions applies only to the real source/channel pair. There is no
// catalogue bulk contract for a channel.
type HomeChannelActions struct {
	Play     bool `json:"play"`
	Favorite bool `json:"favorite"`
}

// HomeActionAuthority is resolved by the HTTP command authorizers in the same
// snapshot as Home. None of these facts is inferred from a role by catalog.
type HomeActionAuthority struct {
	Personal, Organize, Bulk, Playback, Queue, Metadata, Refresh, Downloads bool
	ReadingCollections                                                      bool
	DeleteLibrary                                                           func(string) (bool, error)
}

type homeActionFacts struct {
	kind, library string
	available     bool
	playable      bool
	state         HomeActionState
}

// homeActionAvailableSQL tests the current canonical membership, availability
// and viewer policy in the caller's snapshot. The membership primary key starts
// with entity_id; CROSS JOIN keeps each returned target's range as the driver,
// followed by primary-key probes of its members and availability. EXISTS stops
// on the first permitted available member, with no correctness/candidate cap.
// Global browse availability and restricted-class sort facts cannot prove this:
// the class rows currently copy the global member-link availability state.
// homeActionAvailableSQLFor is the one viewer-bound availability predicate. The
// entity-id expression lets the same rule serve the Home/browse projection
// (catalog_entities e.id) and the whole-view aggregate over catalog_browse_rows
// (e.entity_id); a container is available only through a member the viewer may
// actually see.
func homeActionAvailableSQLFor(v Viewer, entityID string) (string, []any) {
	visible, args := v.itemVisibilitySQL("home_member.item_id")
	return `CASE WHEN k.playable=1 THEN COALESCE(a.available,0) ELSE EXISTS(
	 SELECT 1 FROM catalog_browse_memberships home_member
	 CROSS JOIN catalog_item_availability available ON available.entity_id=home_member.item_id
	 WHERE home_member.entity_id=` + entityID + ` AND available.available=1 AND available.retired=0
	 AND ` + visible + recordingsClause("home_member.item_id", v.EffectiveRestrictions()) + `
	 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=home_member.item_id)) END`, args
}

func homeActionAvailableSQL(v Viewer) (string, []any) { return homeActionAvailableSQLFor(v, "e.id") }

// ProjectHomeActions decorates only the already-authorized returned Home items.
// It requires the composition snapshot and batches exact returned-entity keys.
// Availability may visit that target's full indexed membership range, but no
// descendant list is retained or returned and no library is fetched. The Home
// caller retains its normal post-read revocation/fence proof for these offers.
func (s *Service) ProjectHomeActions(v Viewer, authority HomeActionAuthority, rows []HomeRow, spotlight []HomeSpotlight) error {
	if dbwork.Snapshot(s.Context()) == nil {
		return sql.ErrTxDone
	}
	entries := []*ContentEntry{}
	collections := map[*HomeCollection]*ContentEntry{}
	var collect func([]HomeRow)
	collect = func(rows []HomeRow) {
		for i := range rows {
			row := &rows[i]
			for j := range row.Entries {
				entries = append(entries, &row.Entries[j])
			}
			for j := range row.Resumes {
				entries = append(entries, &row.Resumes[j].Target)
			}
			if row.Collection != nil {
				c := row.Collection
				entry := &ContentEntry{ID: c.Collection.ID, LibraryID: c.Collection.LibraryID, Kind: "collection"}
				collections[c] = entry
				entries = append(entries, entry)
				for j := range row.Collection.Members {
					entries = append(entries, &row.Collection.Members[j])
				}
			}
			collect(row.Children)
		}
	}
	collect(rows)
	for i := range spotlight {
		entries = append(entries, &spotlight[i].Entry)
		if spotlight[i].ResumeTarget != nil {
			entries = append(entries, spotlight[i].ResumeTarget)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	ids, seen := []string{}, map[string]bool{}
	for _, entry := range entries {
		entry.HomeActions = nil
		if !seen[entry.ID] {
			ids = append(ids, entry.ID)
			seen[entry.ID] = true
		}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	facts := map[string]homeActionFacts{}
	availability, bound := homeActionAvailableSQL(v)
	bound = append(bound, v.Profile, string(raw), v.Profile, v.Profile)
	leafVisible, leafArgs := v.itemVisibilitySQL("e.id")
	containerVisible, containerArgs := v.entityVisibilitySQL("e.id", "l.library_id")
	bound = append(bound, leafArgs...)
	bound = append(bound, containerArgs...)
	// The requested primary keys drive every join. Personal state is the
	// displayed title's row; a show's first episode cannot supply its flags.
	found, err := s.read().Query(`SELECT pid(e.public_id),k.name,l.library_id,k.playable,
	 `+availability+`,COALESCE(p.watchlisted,0),COALESCE(p.favorite,0),p.rating,
	 COALESCE(p.revision,0),COALESCE(c.watched,`+personalstate.SQL("?", "e.id")+`,0),COALESCE(c.revision,0)
	 FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)
	 JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries l ON l.id=e.library_id
	 LEFT JOIN catalog_item_availability a ON a.entity_id=e.id AND a.retired=0
	 LEFT JOIN personal_items p ON p.item_id=e.id AND p.profile_id=?
	 LEFT JOIN container_personal_state c ON c.container_id=e.id AND c.kind=k.name AND c.profile_id=?
	 WHERE e.retired=0 AND l.retired=0
	 AND (e.kind NOT IN(20,21) OR EXISTS(SELECT 1 FROM libraries reading_library WHERE reading_library.id=l.library_id AND ((e.kind=20 AND reading_library.kind='book') OR (e.kind=21 AND reading_library.kind='comic'))))
	 AND CASE WHEN k.playable=1 OR e.kind IN(20,21) THEN
	 (`+leafVisible+recordingsClause("e.id", v.EffectiveRestrictions())+` AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id))
	 ELSE `+containerVisible+` END`, bound...)
	if err != nil {
		return err
	}
	for found.Next() {
		var id string
		var f homeActionFacts
		if err = found.Scan(&id, &f.kind, &f.library, &f.playable, &f.available, &f.state.Watchlisted, &f.state.Favorite, &f.state.Rating, &f.state.PersonalRevision, &f.state.Watched, &f.state.ContainerRevision); err != nil {
			break
		}
		facts[id] = f
	}
	if err == nil {
		err = found.Err()
	}
	found.Close()
	if err != nil {
		return err
	}
	deletion := map[string]bool{}
	for _, entry := range entries {
		if entry.Removed {
			removed, err := s.RemovedTitle(v, entry.ID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if removed.Kind == entry.Kind {
				entry.HomeActions = &HomeActions{Details: true, RemoveFromContinue: authority.Personal && removed.CanRemoveFromContinue}
			}
			continue
		}
		f, ok := facts[entry.ID]
		if !ok || f.kind != entry.Kind || f.library != entry.LibraryID || !v.AllowsLibrary(f.library) {
			continue
		}
		if f.kind == "ebook" || f.kind == "comic" {
			// Exact Reading leaves use the bounded personal Saved entries
			// command. They do not acquire any timed-media/job selector grants.
			collection := authority.ReadingCollections
			entry.HomeActions = &HomeActions{State: f.state, Details: true, Collection: collection, Selection: HomeSelectionActions{Collection: collection}}
			continue
		}
		leaf := f.kind == "movie" || f.kind == "episode" || f.kind == "song" || f.kind == "audiobook_file"
		container := f.kind == "show" || f.kind == "season" || f.kind == "album" || f.kind == "artist" || f.kind == "book" || f.kind == "collection"
		if !leaf && !container {
			continue
		}
		if entry.Watched != nil {
			f.state.Watched = *entry.Watched
		}
		available := f.available
		a := &HomeActions{State: f.state, Details: true}
		// Same-snapshot visible displayed identity, using exactly the batch writer's
		// target support. Row classification only affects client presentation.
		targetMatches := true
		if (f.kind == "show" || f.kind == "album" || f.kind == "book") && entry.Navigation != nil && entry.Navigation.EntityID != "" {
			targetMatches = entry.Navigation.EntityID == entry.ID
		}
		a.NotInterested = authority.Personal && targetMatches && personalTargetSupported(f.kind, f.playable, "notInterested")
		a.Play = authority.Playback && available
		a.Queue = authority.Queue && available
		// Playlist jobs expand a real container selector server-side. Explicit
		// ids must remain leaf ids; callers must not send an aggregate as one.
		a.Playlist = authority.Organize && (leaf || container && authority.Bulk)
		a.Collection = authority.Organize && (leaf || container && authority.Bulk)
		a.MyList = authority.Personal && (leaf || containerSaves(f.kind, "watchlisted"))
		a.Favorite = authority.Personal && (leaf || containerSaves(f.kind, "favorite"))
		a.Watched = authority.Personal && (f.kind == "movie" || f.kind == "episode" || f.kind == "show" || f.kind == "season")
		a.Played = authority.Personal && (f.kind == "song" || f.kind == "audiobook_file" || f.kind == "album" || f.kind == "book")
		a.Rating = authority.Personal && leaf
		a.EditMetadata = authority.Metadata && (leaf || f.kind == "show" || f.kind == "season" || f.kind == "album" || f.kind == "artist" || f.kind == "book")
		a.RefreshMetadata = authority.Refresh && (leaf || f.kind == "show")
		a.Download = authority.Downloads && available && (leaf || f.kind == "show" || f.kind == "season" || f.kind == "album" || f.kind == "book")
		// The real delete preview accepts these displayed aggregate targets and
		// resolves TitleMembersTx server-side. Its 200 displayed-target limit
		// and separate 5000 member limit are enforced by that preview, never by
		// substituting a child or walking descendants on the client.
		deletable := leaf || f.kind == "show" || f.kind == "season" || f.kind == "album" || f.kind == "artist" || f.kind == "book"
		if authority.DeleteLibrary != nil && deletable {
			allowed, known := deletion[f.library]
			if !known {
				allowed, err = authority.DeleteLibrary(f.library)
				if err != nil {
					return err
				}
				deletion[f.library] = allowed
			}
			a.Delete = allowed
		}
		// The one offer rule, shared with the whole-view aggregate.
		a.Selection = selectionOffers(authority, f.kind, available, a.Delete)
		entry.HomeActions = a
	}
	for collection, entry := range collections {
		collection.Actions = entry.HomeActions
	}
	return nil
}
