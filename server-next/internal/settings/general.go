package settings

import (
	"strings"
	"unicode/utf8"
)

// GeneralSettings is Settings › General.
type GeneralSettings struct {
	// Name is the server's name, shown in every app's server list and to people signing in.
	Name string `json:"name"`
}

// General is the general settings document.
var General = Define("general", GeneralSettings{Name: "Portico"}, func(g *GeneralSettings) error {
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" || utf8.RuneCountInString(g.Name) > 50 {
		return &Invalid{Field: "name", Message: "Give the server a name of up to 50 characters."}
	}
	return nil
})
