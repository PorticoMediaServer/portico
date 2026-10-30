package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"time"
)

type EpisodeNaming struct {
	Reading     naming.Reading
	Part        int
	Edition     string
	ShowEdition string
	Provider    []compactcatalog.ProviderIdentity
	AirDate     string
	ShowTitle   string
	Year        int
	Season      int
	Numbers     []int
	Numbering   string
	Issue       string
}

// ParseEpisode reads the same source-relative evidence used by title attachment
// and matching. File evidence wins folder conflicts.
func ParseEpisode(relative, kind string) EpisodeNaming {
	return EpisodeFromReading(naming.Read(relative, kind))
}

func EpisodeFromReading(r naming.Reading) EpisodeNaming {
	out := EpisodeNaming{Reading: r, Season: -1, Issue: "unrecognized_episode_name", Provider: nameProviderIDs(r, "show")}
	if len(r.Parts) > 0 {
		out.Part = r.Parts[0].Value
	}
	if len(r.Editions) > 0 {
		out.Edition = r.Editions[0].Value
	}
	if edition, known := r.ShowFolderEdition(); known {
		out.ShowEdition = edition.Value
	}
	out.ShowTitle, out.Year = r.PreferredTitle()
	if len(r.Diagnostics) > 0 {
		out.Issue = r.Diagnostics[0]
		return out
	}
	if len(r.AirDates) > 0 {
		out.AirDate = r.AirDates[0].Value
		date, _ := time.Parse("2006-01-02", out.AirDate)
		out.Numbering, out.Season, out.Numbers = "date", date.Year(), []int{date.YearDay()}
	} else if len(r.Episodes) > 0 && len(r.Seasons) > 0 {
		out.Numbering, out.Season, out.Numbers = "seasonal", r.Seasons[0].Value, r.Episodes[0].Value
	} else if len(r.AbsoluteNumbers) > 0 {
		out.Numbering, out.Numbers = "absolute", []int{r.AbsoluteNumbers[0].Value}
	} else {
		return out
	}
	for _, n := range out.Numbers {
		if n < 0 || n > 9999 {
			out.Issue = "episode_number_out_of_range"
			return out
		}
	}
	if out.ShowTitle == "" {
		out.Issue = "series_identity_missing"
		return out
	}
	out.Issue = ""
	return out
}
