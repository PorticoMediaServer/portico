package metadata

import "github.com/PorticoMediaServer/portico/server-next/internal/settings"

// Setting is the owner's metadata switches, the settings document "metadata". The owner sees
// and changes it on Server › Metadata (/v1/admin/metadata/settings) with the provider keys,
// which are not in it: a key is sealed in a row of its own and never read back.
type Setting struct {
	// IncludeAdult is "Include adult titles" in lookups and searches. Off by default
	// (definition area 4, rule 8).
	IncludeAdult bool `json:"includeAdult"`
	// CastPhotos is "Cast and crew photos", on by default: people's photos come from the
	// provider. Off, none is fetched or kept (definition area 4, rule 47).
	CastPhotos bool `json:"castPhotos"`
}

// Document is the owner's metadata switches.
var Document = settings.Define("metadata", Setting{CastPhotos: true}, nil)

// setting is the switches in force; with no Settings (a package's own tests), the defaults.
func (s *Service) setting() Setting { return Document.Get(s.Settings) }
