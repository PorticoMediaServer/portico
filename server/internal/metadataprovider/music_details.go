package metadataprovider

// Typed optional supplements to immutable MusicBrainz evidence. These fields
// are never substituted for the recording/release/track IDs used by playback.
type Work struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Type           string `json:"type,omitempty"`
	Disambiguation string `json:"disambiguation,omitempty"`
}
type MusicRelation struct {
	Type       string   `json:"type"`
	TypeID     string   `json:"type-id,omitempty"`
	TargetType string   `json:"target-type"`
	Direction  string   `json:"direction,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
	Artist     *Artist  `json:"artist,omitempty"`
	Work       *Work    `json:"work,omitempty"`
}
type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ReleaseLabel struct {
	CatalogNumber string `json:"catalog-number"`
	Label         *Label `json:"label,omitempty"`
}
type ReleaseText struct {
	Language string `json:"language,omitempty"`
	Script   string `json:"script,omitempty"`
}
