package api

import "time"

// HomeLive is the guide-shaped On now row consumed by all apps. Continuations keep
// the same observation; it expires within a minute or when a returned programme ends.
type HomeLive struct {
	Revision   string             `json:"revision"`
	ObservedAt time.Time          `json:"observedAt"`
	ExpiresAt  time.Time          `json:"expiresAt"`
	Sort       string             `json:"sort"`
	Group      string             `json:"group"`
	Groups     []LiveChannelGroup `json:"groups"`
	Channels   []HomeChannel      `json:"channels"`
}

type HomeChannel struct {
	Source  HomeLiveSource  `json:"source"`
	Channel HomeLiveChannel `json:"channel"`
	Actions HomeLiveActions `json:"actions"`
}

type HomeLiveSource struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Generation     string    `json:"generation"`
	PublishedAt    time.Time `json:"publishedAt"`
	AvailableStart string    `json:"availableStart"`
	AvailableEnd   string    `json:"availableEnd"`
	Provenance     string    `json:"provenance"`
	RefreshState   string    `json:"refreshState"`
}

type HomeLiveChannel struct {
	ID                    string              `json:"id"`
	SourceID              string              `json:"sourceId"`
	Provenance            string              `json:"provenance"`
	Name                  string              `json:"name"`
	Number                string              `json:"number"`
	Group                 string              `json:"group"`
	Generation            string              `json:"generation"`
	LogoPath              string              `json:"logoPath,omitempty"`
	Mark                  HomeLiveMark        `json:"mark"`
	Programmes            []HomeLiveProgramme `json:"programmes"`
	TuneAvailable         bool                `json:"tuneAvailable"`
	RecordAvailable       bool                `json:"recordAvailable"`
	TuneUnavailableReason string              `json:"tuneUnavailableReason"`
	Guide                 string              `json:"guide"`
	Favorite              bool                `json:"favorite"`
	Hidden                bool                `json:"hidden"`
	PreferenceRevision    int                 `json:"preferenceRevision"`
}

type HomeLiveMark struct {
	Text string `json:"text"`
	Seed string `json:"seed"`
}

type HomeLiveActions struct {
	Play     bool `json:"play"`
	Favorite bool `json:"favorite"`
}

type HomeLiveProgramme struct {
	ID          string           `json:"id"`
	ChannelID   string           `json:"channelId"`
	Title       string           `json:"title"`
	Start       time.Time        `json:"start"`
	End         time.Time        `json:"end"`
	Lineage     string           `json:"lineage"`
	Description string           `json:"description"`
	Subtitle    string           `json:"subtitle,omitempty"`
	Episode     *HomeLiveEpisode `json:"episode,omitempty"`
	Rating      *HomeLiveRating  `json:"rating,omitempty"`
}

type HomeLiveEpisode struct {
	Season *int `json:"season,omitempty"`
	Number *int `json:"number,omitempty"`
}

type HomeLiveRating struct {
	System string `json:"system"`
	Value  string `json:"value"`
}
