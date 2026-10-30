package assets

import "github.com/PorticoMediaServer/portico/server/internal/metadataprovider"

// VideoNFO is bounded descriptive evidence, never probe facts or source identity.
// Locator is an opaque sidecar digest, not a client-visible filesystem path.
type NFOActorThumbnail struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type VideoNFO struct {
	Issues                 []string                        `json:"issues,omitempty"`
	Tags                   []string                        `json:"tags,omitempty"`
	Countries              []string                        `json:"countries,omitempty"`
	Status                 string                          `json:"status,omitempty"`
	Trailer                string                          `json:"trailer,omitempty"`
	Rating                 *metadataprovider.ScreenRating  `json:"rating,omitempty"`
	ActorThumbnails        []NFOActorThumbnail             `json:"actorThumbnails,omitempty"`
	DisplaySeason          *int                            `json:"displaySeason,omitempty"`
	DisplayEpisode         *int                            `json:"displayEpisode,omitempty"`
	DateAdded              string                          `json:"dateAdded,omitempty"`
	CollectionOverview     string                          `json:"collectionOverview,omitempty"`
	LockData               bool                            `json:"lockData,omitempty"`
	Kind                   string                          `json:"kind"`
	Locator                string                          `json:"locator"`
	Digest                 string                          `json:"digest"`
	Size                   int64                           `json:"size"`
	ModifiedNS             int64                           `json:"modifiedNs"`
	Revision               string                          `json:"revision,omitempty"`
	Title                  string                          `json:"title,omitempty"`
	OriginalTitle          string                          `json:"originalTitle,omitempty"`
	SortTitle              string                          `json:"sortTitle,omitempty"`
	Year                   int                             `json:"year,omitempty"`
	Overview               string                          `json:"overview,omitempty"`
	Tagline                string                          `json:"tagline,omitempty"`
	Date                   string                          `json:"date,omitempty"`
	Edition                string                          `json:"edition,omitempty"`
	Collection             string                          `json:"collection,omitempty"`
	Certification          string                          `json:"certification,omitempty"`
	Genres                 []string                        `json:"genres,omitempty"`
	Studios                []string                        `json:"studios,omitempty"`
	IDs                    []metadataprovider.ScreenID     `json:"ids,omitempty"`
	Credits                []metadataprovider.ScreenCredit `json:"credits,omitempty"`
	Season                 *int                            `json:"season,omitempty"`
	Episode                *int                            `json:"episode,omitempty"`
	Absolute               *int                            `json:"absolute,omitempty"`
	AdvisoryRuntimeMinutes *int                            `json:"advisoryRuntimeMinutes,omitempty"`
}
