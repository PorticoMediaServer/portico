package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/musicanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
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
	Handle(s, "POST /v1/music/mixes/{id}/playlist", WithProfile, func(r *http.Request, req MusicMixSave) (ListCreated, error) {
		return s.saveMix(r, music, req)
	})
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

// MusicMixSave saves a mix as a new playlist of the profile: Save as playlist on the Mixes row,
// without making the mix the device's queue.
type MusicMixSave struct {
	// Name is the playlist's name, as the app names the mix.
	Name string `json:"name"`
}

// saveMix saves the mix's songs, as the asking profile sees them now and in the mix's order,
// as a new playlist of that profile. No queue is touched. A repeat with the same
// Idempotency-Key answers the playlist the first one made, even once the mix has moved on.
func (s *Server) saveMix(r *http.Request, music *musicanalysis.Mixer, req MusicMixSave) (ListCreated, error) {
	if err := checkName(&req.Name, new("")); err != nil {
		return ListCreated{}, err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return ListCreated{}, err
	}
	ctx, v := r.Context(), viewer(r)
	hash := requestHash(struct {
		Mix string
		MusicMixSave
	}{r.PathValue("id"), req})
	if key != "" {
		if made, err := s.db.CreatedBefore(ctx, v.UserID, key, hash); err != nil {
			return ListCreated{}, listError(err)
		} else if made != "" {
			l, err := s.db.List(ctx, v, made)
			return ListCreated{listShape(l)}, listError(err)
		}
	}
	songs, err := music.MixSongs(ctx, v, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return ListCreated{}, ErrNotFound()
	}
	if err != nil {
		return ListCreated{}, err
	}
	l, err := s.db.CreateList(ctx, v, db.NewList{Kind: db.ListPlaylist, Name: req.Name, SortName: sorttext.Key(req.Name, "", ""), Items: songs},
		key, hash, s.now())
	return ListCreated{listShape(l)}, listError(err)
}
