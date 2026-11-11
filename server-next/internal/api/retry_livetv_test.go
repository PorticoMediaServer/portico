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
			{route: "POST /v1/admin/library-channels/presets", body: BlockPresetSave{Name: new("Evenings"), Library: new(films),
				Recipe: &librarychannels.Recipe{Order: "shuffle", Minutes: 120}}, other: BlockPresetSave{Name: new("Mornings"),
				Library: new(films), Recipe: &librarychannels.Recipe{Order: "shuffle", Minutes: 60}},
				count: func() int { return th.named("/v1/admin/library-channels/presets", "name", "Evenings") }},
		}
	})
	safeToRepeat(map[string]string{
		"POST /v1/admin/library-channels/preview":         "changes nothing",
		"POST /v1/admin/library-channels/{id}/regenerate": "sets the channel to be built again",
	})
}

func (th *theatre) filmChannel() *librarychannels.Builder {
	return &librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{th.libs[db.LibraryMovies].PublicID},
		Kinds: []string{"movie"}}}
}
