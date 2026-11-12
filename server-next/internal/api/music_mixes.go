package api

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/musicanalysis"
)

// MusicMix is one of the profile's Mixes for you: a row on Home and in Music Discover. It
// plays like a playlist (POST /v1/queues with source mix) and is saved as one with the queue's
// Save as playlist.
type MusicMix struct {
	ID string `json:"id"`
	// Style is the mix's strongest style as a word the app translates (one of classical,
	// techno, electronic, rock, ambient, opera, pop, soft, new age, dance, country, metal);
	// absent, the mix is named after its first artist.
	Style       string           `json:"style,omitempty"`
	Artists     []MusicMixArtist `json:"artists"`
	MoreArtists bool             `json:"moreArtists,omitempty"`
	// Covers are up to three albums of the mix, for its drawn cover: a tint from the first,
	// the three fanned. Image is absent for an album without a cover.
	Covers []MusicMixCover `json:"covers"`
	Songs  int             `json:"songs"`
}

// MusicMixArtist is an artist a mix is shown with.
type MusicMixArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MusicMixCover is an album a mix's cover is drawn from.
type MusicMixCover struct {
	AlbumID string `json:"albumId"`
	Image   string `json:"image,omitempty"`
}

// musicMixRoutes serves the mixes, lets a queue be made from one, and gives radio its picks by
// sound.
func (s *Server) musicMixRoutes(music *musicanalysis.Mixer) {
	s.queues.Mixes = func(ctx context.Context, v db.Viewer, id string) ([]string, error) { return music.MixSongs(ctx, v, id) }
	s.queues.SoundRadio = music.Radio
	Handle(s, "GET /v1/music/mixes", WithProfile, func(r *http.Request, _ None) (Page[MusicMix], error) {
		mixes, err := music.Mixes(r.Context(), viewer(r))
		if err != nil {
			return Page[MusicMix]{}, err
		}
		out := Page[MusicMix]{Items: make([]MusicMix, 0, len(mixes))}
		for _, m := range mixes {
			mix := MusicMix{ID: m.ID, Style: m.Style, MoreArtists: m.MoreArtists, Songs: len(m.Songs), Artists: []MusicMixArtist{}, Covers: []MusicMixCover{}}
			for _, a := range m.Artists {
				mix.Artists = append(mix.Artists, MusicMixArtist{ID: a.ID, Name: a.Name})
			}
			for _, c := range m.Covers {
				cover := MusicMixCover{AlbumID: c.AlbumID}
				if c.Hash != "" {
					cover.Image = "/v1/images/" + c.Hash
				}
				mix.Covers = append(mix.Covers, cover)
			}
			out.Items = append(out.Items, mix)
		}
		return out, nil
	})
}
