package metadataprovider

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// SeriesPageArtwork is what a show page needs from TVDB beyond the series
// record: each season's poster (by episode order) and the cast's photos. It
// reads the same extended record ScreenDetails reads, through the same cache,
// so a refresh that fetched the record costs no second request.
type SeriesPageArtwork struct {
	Seasons []SeasonImage
	People  []PersonImage
}

// SeasonImage is one season's poster in one TVDB episode order ("official",
// "dvd", "absolute", "alternate", "regional").
type SeasonImage struct {
	Number int
	Order  string
	URL    string
}

// PersonImage is a credited person's photo (the character image when TVDB has
// no photo of the person).
type PersonImage struct {
	PersonID int64
	URL      string
}

func (s *TVDB) SeriesPageArtwork(ctx context.Context, id int64) (SeriesPageArtwork, error) {
	var out SeriesPageArtwork
	if id <= 0 {
		return out, errors.New("invalid series")
	}
	var page struct {
		Status string `json:"status"`
		Data   struct {
			ID      int64 `json:"id"`
			Seasons []struct {
				Number int    `json:"number"`
				Image  string `json:"image"`
				Type   struct {
					Type string `json:"type"`
				} `json:"type"`
			} `json:"seasons"`
			Characters []struct {
				PersonID    int64  `json:"peopleId"`
				PersonImage string `json:"personImgURL"`
				Image       string `json:"image"`
				Sort        int    `json:"sort"`
			} `json:"characters"`
		} `json:"data"`
	}
	route := "/series/" + strconv.FormatInt(id, 10) + "/extended?meta=translations"
	if err := s.screenGet(ctx, route, &page); err != nil {
		return out, err
	}
	if page.Status != "success" || page.Data.ID != id || len(page.Data.Seasons) > 4000 || len(page.Data.Characters) > 4000 {
		return out, &Error{Provider: "tvdb", Code: "invalid_series"}
	}
	seenSeason := map[string]bool{}
	for _, v := range page.Data.Seasons {
		order := strings.ToLower(v.Type.Type)
		key := order + ":" + strconv.Itoa(v.Number)
		image := tvdbImageURL(v.Image)
		if v.Number < 0 || v.Number > 9999 || order == "" || len(order) > 32 || image == "" || len(image) > 4096 || seenSeason[key] {
			continue
		}
		seenSeason[key] = true
		out.Seasons = append(out.Seasons, SeasonImage{v.Number, order, image})
		if len(out.Seasons) == 400 {
			break
		}
	}
	seenPerson := map[int64]bool{}
	for _, c := range page.Data.Characters {
		url := tvdbImageURL(c.PersonImage)
		if url == "" {
			url = tvdbImageURL(c.Image)
		}
		if c.PersonID <= 0 || url == "" || len(url) > 4096 || seenPerson[c.PersonID] {
			continue
		}
		seenPerson[c.PersonID] = true
		out.People = append(out.People, PersonImage{c.PersonID, url})
		// The page shows the main cast; 80 matches the movie portrait bound.
		if len(out.People) == 80 {
			break
		}
	}
	return out, nil
}

// tvdbImageURL makes TVDB's occasional host-relative image paths absolute.
func tvdbImageURL(v string) string {
	if strings.HasPrefix(v, "/banners/") {
		return "https://artworks.thetvdb.com" + v
	}
	return v
}
