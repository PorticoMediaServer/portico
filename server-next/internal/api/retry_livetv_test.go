package api

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// Live TV and Library Channels.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		films := th.libs[db.LibraryMovies].PublicID
		return []retryCase{
			{route: "POST /v1/admin/library-channels", body: LibraryChannelSave{Name: new("Films"), Builder: th.filmChannel()},
				other: LibraryChannelSave{Name: new("Other"), Builder: th.filmChannel()},
				count: func() int { return th.named("/v1/admin/library-channels", "name", "Films") }},
			{route: "POST /v1/admin/block-presets", body: LibraryChannelBlockPresetSave{Name: new("Evenings"), Library: new(films),
				Recipe: &librarychannels.LibraryChannelRecipe{Order: "shuffle", Minutes: 120}}, other: LibraryChannelBlockPresetSave{Name: new("Mornings"),
				Library: new(films), Recipe: &librarychannels.LibraryChannelRecipe{Order: "shuffle", Minutes: 60}},
				count: func() int { return th.named("/v1/admin/block-presets", "name", "Evenings") }},
			{route: "POST /v1/admin/live/sources", body: LiveSourceSave{Kind: "guide", Name: new("Listings"), Guide: "<tv></tv>"},
				other: LiveSourceSave{Kind: "guide", Name: new("Other"), Guide: "<tv></tv>"},
				count: func() int { return th.named("/v1/admin/live/sources", "name", "Listings") }},
		}
	})
	safeToRepeat(map[string]string{
		"POST /v1/admin/live-sources/discover":            "only reads network tuner facts; no source or saved setting is created",
		"POST /v1/admin/library-channels/preview":         "changes nothing",
		"POST /v1/admin/library-channels/{id}/regenerate": "sets the channel to be built again",
		"POST /v1/admin/live/sources/preview":             "changes nothing",
		"POST /v1/admin/live/sources/{id}/refresh":        "a refresh already running is waited for, not repeated",
		"POST /v1/admin/live/sources/{id}/logos":          "artwork is held by content hash; repeated imports update the same channel logos and create no duplicate resource",
		"POST /v1/dvr/recordings":                         "an airing is booked once however often it is asked",
		"POST /v1/dvr/rules":                              "a profile's rule for the same series and channel is answered, not made again",
	})
}

func (th *theatre) filmChannel() *librarychannels.LibraryChannelBuilder {
	return &librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{th.libs[db.LibraryMovies].PublicID},
		Kinds: []string{"movie"}}}
}
