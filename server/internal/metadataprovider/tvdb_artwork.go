package metadataprovider

import (
	"context"
	"errors"
	"golang.org/x/text/language"
	"math"
	"sort"
	"strconv"
	"strings"
)

type ArtworkImage struct {
	ID                  int64
	Role, URL, Language string
	Score               float64
}

// Artwork reuses the existing private token/rate-budget transport. Type IDs are
// resolved from the provider's own type registry, never guessed from magic IDs.
func (s *TVDB) Artwork(ctx context.Context, id int64) ([]ArtworkImage, error) {
	return s.ArtworkForKind(ctx, "show", id, "")
}
func (s *TVDB) ArtworkForKind(ctx context.Context, kind string, id int64, locale string) ([]ArtworkImage, error) {
	if id <= 0 {
		return nil, errors.New("invalid artwork entity")
	}
	var types struct {
		Status string `json:"status"`
		Data   []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
	}
	if err := s.get(ctx, "/artwork/types", &types); err != nil {
		return nil, err
	}
	if types.Status != "success" || len(types.Data) > 128 {
		return nil, &Error{Provider: "tvdb", Code: "invalid_artwork_types"}
	}
	roles := map[int64]string{}
	for _, t := range types.Data {
		name := strings.ToLower(t.Name)
		role := ""
		switch {
		case strings.Contains(name, "poster"):
			role = "poster"
		case strings.Contains(name, "background"):
			role = "backdrop"
		case strings.Contains(name, "banner"):
			role = "banner"
		case strings.Contains(name, "icon"):
			role = "square"
		case strings.Contains(name, "logo"):
			role = "logo"
		case strings.Contains(name, "thumbnail"):
			role = "thumbnail"
		}
		if role != "" {
			roles[t.ID] = role
		}
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ID       int64 `json:"id"`
			Artworks []struct {
				ID       int64   `json:"id"`
				Type     int64   `json:"type"`
				Image    string  `json:"image"`
				Language string  `json:"language"`
				Score    float64 `json:"score"`
			}
		}
	}
	route := "/series/" + strconv.FormatInt(id, 10) + "/artworks"
	if kind == "movie" {
		route = "/movies/" + strconv.FormatInt(id, 10) + "/extended"
	} else if kind != "show" {
		return nil, errors.New("invalid artwork kind")
	}
	if err := s.get(ctx, route, &result); err != nil {
		return nil, err
	}
	if result.Status != "success" || result.Data.ID != id || len(result.Data.Artworks) > 4000 {
		return nil, &Error{Provider: "tvdb", Code: "invalid_artwork"}
	}
	out := []ArtworkImage{}
	for _, a := range result.Data.Artworks {
		role := roles[a.Type]
		if role == "" || a.ID <= 0 || len(a.Image) > 4096 || len(a.Language) > 64 || math.IsNaN(a.Score) || math.IsInf(a.Score, 0) {
			continue
		}
		out = append(out, ArtworkImage{a.ID, role, a.Image, a.Language, a.Score})

	}
	base, _ := language.Make(locale).Base()
	preferred := base.String()
	iso3 := base.ISO3()
	rank := func(l string) int {
		if strings.EqualFold(l, preferred) || strings.EqualFold(l, iso3) {
			return 0
		}
		if l == "" {
			return 1
		}
		if l == "en" || l == "eng" {
			return 2
		}
		return 3
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := rank(out[i].Language), rank(out[j].Language); a != b {
			return a < b
		}
		return out[i].Score > out[j].Score
	})
	kept := []ArtworkImage{}
	counts := map[string]int{}
	for _, im := range out {
		if counts[im.Role] < 20 && len(kept) < 80 {
			kept = append(kept, im)
			counts[im.Role]++
		}
	}
	return kept, nil
}
