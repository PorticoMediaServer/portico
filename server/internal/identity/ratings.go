package identity

import "github.com/PorticoMediaServer/portico/server/internal/securitypolicy"

// RatingValue is one entry of one rating system.
type RatingValue struct {
	Code       string `json:"code"`
	Label      string `json:"label"`
	MinimumAge int    `json:"minimumAge"`
}

// RatingSystem is an ordered table of the values one body issues, ascending.
type RatingSystem struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Region  string        `json:"region"`
	Values  []RatingValue `json:"values"`
	Screens []string      `json:"screens"`
}

// RatingSystems preserves the identity API types while sharing one policy table
// with the new store layer. Returned slices own their values.
func RatingSystems() []RatingSystem {
	source := securitypolicy.RatingSystems()
	out := make([]RatingSystem, len(source))
	for i, system := range source {
		out[i] = RatingSystem{ID: system.ID, Name: system.Name, Region: system.Region, Screens: append([]string(nil), system.Screens...), Values: make([]RatingValue, len(system.Values))}
		for j, value := range system.Values {
			out[i].Values[j] = RatingValue{Code: value.Code, Label: value.Label, MinimumAge: value.MinimumAge}
		}
	}
	return out
}

const RatingTableVersion = securitypolicy.RatingTableVersion

func RatingAge(raw string) (int, bool) { return securitypolicy.RatingAge(raw) }
func RatingCeiling(system, code string) (int, bool) {
	return securitypolicy.RatingCeiling(system, code)
}
