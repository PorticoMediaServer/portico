package settings

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// GeneralSettings is Settings › General.
type GeneralSettings struct {
	// Name is the server's name, shown in every app's server list and to people signing in.
	Name            string `json:"name"`
	BackgroundSpeed string `json:"backgroundSpeed"`
	// UpdateCheck is how the server looks for a new version, once a day and on Check now:
	// "automatic" (Portico's releases), "never", or "address" (UpdateAddress, the owner's own).
	// It only ever tells the owner; nothing is installed.
	UpdateCheck   string `json:"updateCheck"`
	UpdateAddress string `json:"updateAddress,omitempty"`
	// HeavyWork is when heavy background work runs: see HeavyWorkSettings.
	HeavyWork HeavyWorkSettings `json:"heavyWork"`
}

// The values of UpdateCheck.
const (
	UpdatesAutomatic  = "automatic"
	UpdatesNever      = "never"
	UpdatesOwnAddress = "address"
)

// General is the general settings document.
var General = Define("general", GeneralSettings{Name: "Portico", BackgroundSpeed: "normal", UpdateCheck: UpdatesAutomatic, HeavyWork: HeavyWorkSettings{Hours: HeavyAnyTime}}, func(g *GeneralSettings) error {
	g.Name = strings.TrimSpace(g.Name)
	switch g.BackgroundSpeed {
	case "":
		g.BackgroundSpeed = "normal"
	case "gentle", "normal", "fast":
	default:
		return &Invalid{Field: "backgroundSpeed", Message: "Choose Gentle, Normal or Fast."}
	}
	if g.Name == "" || utf8.RuneCountInString(g.Name) > 50 {
		return &Invalid{Field: "name", Message: "Give the server a name of up to 50 characters."}
	}
	switch g.UpdateCheck {
	case UpdatesAutomatic, UpdatesNever:
	case UpdatesOwnAddress:
		u, err := url.Parse(strings.TrimSpace(g.UpdateAddress))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return &Invalid{Field: "updateAddress", Message: "Give the address as http:// or https://."}
		}
		g.UpdateAddress = u.String()
	default:
		return &Invalid{Field: "updateCheck", Message: "Choose Automatically, Never, or your own address."}
	}
	return g.HeavyWork.check()
})
