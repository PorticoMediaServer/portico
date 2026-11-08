package read

import (
	"sort"
	"time"
)

func homeManifestArtwork(kind string) string {
	if kind == "music" || kind == "audiobook" {
		return "square"
	}
	return "poster"
}
func homeManifestFixed() []HomeManifestDescriptor {
	// These are the public row definitions, not fabricated successful previews.
	// No entries/total/hasMore fields exist in this descriptor-only result.
	makeRow := func(id, title, code, kind, shape string, priority, ttl int, required, cursors bool, reason string) HomeManifestDescriptor {
		return HomeManifestDescriptor{ID: id, Title: title, TitleCode: code, Kind: kind, ArtworkShape: shape, Priority: priority, CacheTTLMs: ttl * 1000, Required: required, Hideable: !required, Reorderable: true, CursorCapable: cursors, PolicyState: "available", ReadState: "unsupported", Reason: reason}
	}
	rows := []HomeManifestDescriptor{
		makeRow("continue", "Continue Watching", "next.home.continueWatching", "continue", "landscape", 10, 45, true, true, "home_continue_composition_not_attached"),
		makeRow("new_on_server", "New on your server", "next.home.newOnServer", "recent", "poster", 20, 120, false, true, "home_chronology_composition_not_attached"),
		makeRow("because_you_watched", "Because you watched", "next.home.becauseHeading", "recommendation", "poster", 30, 60, false, true, "recommendation_source_not_attached"),
		makeRow("featured_collection", "Collection", "next.home.collection", "collection_banner", "poster", 40, 120, false, false, "collection_source_not_attached"),
		makeRow("on_now", "On now", "next.home.onNow", "live", "landscape", 50, 0, false, true, "live_authority_and_guide_not_attached"),
		makeRow("community_watching", "Popular on this server", "next.home.popularOnServer", "community", "poster", 60, 60, false, true, "community_source_not_attached"),
		makeRow("listen_next", "Continue Listening", "next.home.continueListening", "listening", "square", 70, 45, true, true, "home_listening_composition_not_attached"),
		makeRow("continue_books", "Continue your books", "next.home.continueBooks", "continue_books", "square", 80, 45, true, true, "home_reading_policy_not_attached"),
		makeRow("recommended", "Recommended for you", "home.row.recommended", "recommendation", "poster", 90, 300, false, true, "recommendation_source_not_attached"),
		makeRow("for_you", "More recommendations", "home.row.moreRecommendations", "family", "poster", 91, 300, false, false, "recommendation_family_not_attached"),
		makeRow("trending_now", "Trending now", "home.row.trending", "recommendation", "poster", 92, 300, false, true, "recommendation_source_not_attached"),
	}
	privacy := []string{"personal", "catalog", "personal", "catalog", "personal", "aggregated", "personal", "personal", "personal", "personal", "catalog"}
	for n := range rows {
		rows[n].PrivacySensitivity = privacy[n]
		rows[n].TargetKind = "row"
	}
	rows[4].TargetKind = "guide"
	rows[7].Platforms = []string{"web", "phone"}
	rows[9].Family = "for_you"
	rows[0].Critical = true
	rows[5].defaultHidden = true
	// The catalogue explicitly retains this unsupported family, but cannot claim
	// current live availability or put it on the active layout without MG facts.
	rows[4].PolicyState = "unresolved"
	return rows
}
func homeManifestProject(frame homeManifestFrame, at time.Time) HomeManifestDocument {
	doc := HomeManifestDocument{Layout: frame.Layout, Libraries: frame.Libraries, Pending: frame.Pending, SourceExhausted: !frame.Pending, RawLibraryPositions: len(frame.LibraryIDs), SpotlightState: "unsupported_recommendation_source", GeneratedAt: at.UTC().Format(time.RFC3339Nano), context: &frame}
	if frame.Pending {
		doc.PendingReason = "library_projection_requires_preparation"
		return doc
	}
	rows := append(homeManifestFixed(), frame.ViewDescriptors...)
	// The registry default is off, but an effective owner policy is claimed
	// only for an actual native server source. Missing/file sources stay unresolved.
	communityPolicy := "unresolved"
	if len(frame.CommunitySource) == 1 && frame.CommunitySource[0][0] == "server" {
		communityPolicy = "disabled_by_owner"
	}
	if len(frame.CommunitySource) == 1 && frame.CommunitySource[0][0] == "server" && len(frame.Community) == 1 && frame.Community[0][1] != nil {
		switch frame.Community[0][1] {
		case int64(0):
		case int64(1):
			communityPolicy = "available"
		default:
			communityPolicy = "unresolved"
		}
	}
	if len(frame.CommunitySource) == 1 && frame.CommunitySource[0][0] == "file" {
		communityPolicy = "unresolved"
	}
	rank := map[string]int{}
	hidden := map[string]bool{}
	for n, id := range frame.Layout.RowOrder {
		if _, exists := rank[id]; !exists {
			rank[id] = n
		}
	}
	for _, id := range frame.Layout.HiddenRowIDs {
		hidden[id] = true
	}
	for n := range rows {
		row := &rows[n]
		if row.ID == "community_watching" {
			row.PolicyState = communityPolicy
			if communityPolicy == "unresolved" {
				row.Reason = "effective_owner_policy_not_in_native_store"
			}
		}
		if row.Required || !row.Hideable {
			row.Hidden = false
		} else {
			_, placed := rank[row.ID]
			row.Hidden = hidden[row.ID] || row.defaultHidden && !placed
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		x, xok := rank[rows[a].ID]
		y, yok := rank[rows[b].ID]
		if xok != yok {
			return xok
		}
		if xok && x != y {
			return x < y
		}
		if rows[a].Priority != rows[b].Priority {
			return rows[a].Priority < rows[b].Priority
		}
		return rows[a].ID < rows[b].ID
	})
	doc.Catalogue = rows
	for _, row := range rows {
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
	return doc
}
