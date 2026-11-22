package api

import (
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// datedEpisode says an episode was placed by its air date: a daily show or a recording named
// by date, which the scan files under season = the year and episode = the day of the year, so a
// show's years group as seasons. Those are not a real season and episode, so its card says
// dateBased and apps label it by its date instead of "S2026 E326". It is read from the stored
// numbers and air date, so an air date changed later (an NFO's, an owner's edit) makes it an
// ordinary numbered episode again.
func datedEpisode(it db.Item) bool {
	if it.Kind != db.KindEpisode || it.IndexNumber == nil || it.ParentIndex == nil {
		return false
	}
	aired, err := time.Parse("2006-01-02", it.ReleaseDate)
	return err == nil && *it.ParentIndex == aired.Year() && *it.IndexNumber == aired.YearDay()
}
