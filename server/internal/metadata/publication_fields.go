package metadata

import (
	"encoding/json"
	"strings"
)

// The provider payload carries descriptive facts the editor also publishes. They
// are written through publishItemFields so a locked owner decision survives the
// publication and the browse projection is refreshed in the same transaction.
type tmdbDescriptive struct {
	ReleaseDate   string `json:"release_date"`
	Tagline       string `json:"tagline"`
	OriginalTitle string `json:"original_title"`
	Companies     []struct {
		Name string `json:"name"`
	} `json:"production_companies"`
	Countries []struct {
		Code string `json:"iso_3166_1"`
		Name string `json:"name"`
	} `json:"production_countries"`
	Releases struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
}

func tmdbDescriptiveFields(raw, region string) map[string]string {
	var parsed tmdbDescriptive
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return nil
	}
	out := map[string]string{}
	if validISODate(parsed.ReleaseDate) {
		out["releaseDate"] = parsed.ReleaseDate
	}
	out["tagline"] = strings.TrimSpace(parsed.Tagline)
	out["originalTitle"] = strings.TrimSpace(parsed.OriginalTitle)
	if len(parsed.Companies) > 0 {
		out["studio"] = strings.TrimSpace(parsed.Companies[0].Name)
	}
	if len(parsed.Countries) > 0 {
		name := strings.TrimSpace(parsed.Countries[0].Name)
		if name == "" {
			name = strings.TrimSpace(parsed.Countries[0].Code)
		}
		out["country"] = name
	}
	// A certification is only meaningful with its region. Prefer the library's
	// configured region and fall back to the release's own first certification.
	if region == "" {
		region = "US"
	}
	fallback := ""
	for _, r := range parsed.Releases.Results {
		for _, d := range r.Dates {
			value := strings.TrimSpace(d.Certification)
			if value == "" {
				continue
			}
			if strings.EqualFold(r.Country, region) {
				out["contentRating"] = value
				fallback = ""
				break
			}
			if fallback == "" {
				fallback = value
			}
		}
		if _, ok := out["contentRating"]; ok {
			break
		}
	}
	if _, ok := out["contentRating"]; !ok && fallback != "" {
		out["contentRating"] = fallback
	}
	return out
}
