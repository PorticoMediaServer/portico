package httpapi

import (
	"net/http"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func musicMixesView(cards []catalog.MusicMixCard) MusicMixesView {
	out := MusicMixesView{Mixes: []MusicMixView{}}
	for _, card := range cards {
		if len(card.Covers) != 3 || len(card.Artists) == 0 || len(card.Artists) > 2 || card.SongCount < 3 {
			continue
		}
		name := MusicMixNameSourceView{}
		switch {
		case card.NameSource.Style != nil && card.NameSource.Artist == nil:
			style := card.NameSource.Style
			if style.Model != sonic.ModelID || style.Version != sonic.ModelVersion || !slices.Contains(sonic.MixStyleTags(), style.Tag) {
				continue
			}
			name = MusicMixNameSourceView{Kind: "style", Tag: style.Tag, Model: style.Model, Version: style.Version}
		case card.NameSource.Artist != nil && card.NameSource.Style == nil:
			artist := card.NameSource.Artist
			if !slices.ContainsFunc(card.Artists, func(ref catalog.ContentEntityRef) bool { return ref.ID == artist.ID }) {
				continue
			}
			name = MusicMixNameSourceView{Kind: "artist", ArtistID: artist.ID}
		default:
			continue
		}
		mix := MusicMixView{ID: card.ID, NameSource: name, Tint: card.Tint, Covers: []MusicMixCoverView{}, Artists: append([]catalog.ContentEntityRef(nil), card.Artists...), More: card.More, SongCount: card.SongCount}
		for _, cover := range card.Covers {
			mix.Covers = append(mix.Covers, MusicMixCoverView{Album: cover.Album, PosterURL: cover.PosterURL})
		}
		out.Mixes = append(out.Mixes, mix)
	}
	return out
}

func (d Dependencies) musicMixesRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/music/mixes", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) != 0 {
			failure(w, errStrictJSON)
			return
		}
		cards, scope, err := d.musicMixCardsRequest(r)
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, http.StatusOK, projectedViewerEnvelope[MusicMixesView]{Scope: scope, Data: musicMixesView(cards)})
	})
	d.httpHandle(mux, "POST /v1/music/mixes/{id}/playlist", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) != 0 {
			failure(w, errStrictJSON)
			return
		}
		out, err := d.saveMusicMixRequest(w, r)
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, http.StatusOK, out)
	})
}
