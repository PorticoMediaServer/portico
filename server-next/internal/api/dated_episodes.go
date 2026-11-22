package api

import (
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// episodeNumbers is what an episode's card says about its place in its show. An episode placed
// by its air date (a daily show, a recording named by date: the scan files it under season =
// the year, episode = the day of the year, so a show's years group as seasons) has no real
// season or episode number, so none is sent and apps label it by its date instead of
// "S2026 E326". Anything else is sent as stored.
func episodeNumbers(it db.Item) (index, parentIndex *int) {
	if it.Kind == db.KindEpisode && it.IndexNumber != nil && it.ParentIndex != nil {
		if aired, err := time.Parse("2006-01-02", it.ReleaseDate); err == nil &&
			*it.ParentIndex == aired.Year() && *it.IndexNumber == aired.YearDay() {
			return nil, nil
		}
	}
	return it.IndexNumber, it.ParentIndex
}
