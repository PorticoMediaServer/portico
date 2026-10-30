package metadataprovider

// Descriptor is the curated provider registry, shared by per-library policies
// and source choices. Providers are application adapters, never plugins.
type Descriptor struct {
	ID                 string   `json:"id"`
	Kinds              []string `json:"kinds"`
	CredentialOverride bool     `json:"credentialOverride"`
}

func Registry() []Descriptor {
	return []Descriptor{
		{"tmdb", []string{"movie", "tv", "anime"}, true},
		{"tvdb", []string{"movie", "tv", "anime"}, true},
		{"anilist", []string{"anime"}, false},
		{"musicbrainz", []string{"music"}, false},
		{"local", []string{"movie", "tv", "anime", "music", "audiobook"}, false},
	}
}
func ProvidersFor(kind string) []string {
	out := []string{}
	for _, provider := range Registry() {
		if provider.ID == "local" {
			continue
		}
		for _, k := range provider.Kinds {
			if k == kind {
				out = append(out, provider.ID)
			}
		}
	}
	return out
}
