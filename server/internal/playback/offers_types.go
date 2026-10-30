package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type OffersScope struct {
	ServerID    string `json:"serverId"`
	LibraryID   string `json:"libraryId"`
	ItemID      string `json:"itemId"`
	ViewerFence string `json:"viewerFence"`
}
type OfferControl struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}
type OfferedStream struct {
	assets.Stream
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}
type SourceOffer struct {
	ID            string          `json:"id"`
	Available     bool            `json:"available"`
	Container     string          `json:"container"`
	VideoCodec    string          `json:"videoCodec"`
	AudioCodec    string          `json:"audioCodec"`
	Width         int             `json:"width"`
	Height        int             `json:"height"`
	Duration      float64         `json:"duration"`
	FactsRevision int64           `json:"factsRevision"`
	FactsStatus   string          `json:"factsStatus"`
	Relation      string          `json:"relation"`
	Boundary      string          `json:"boundary"`
	Enabled       bool            `json:"enabled"`
	Reason        string          `json:"reason"`
	Streams       []OfferedStream `json:"streams"`
	Qualities     []QualityRung   `json:"qualities"`
}
type CurrentOffer struct {
	PreparedVersionID string `json:"preparedVersionId,omitempty"`
	SessionID         string `json:"sessionId"`
	Generation        int    `json:"generation"`
	State             string `json:"state"`
	SourceID          string `json:"sourceId"`
	Delivery          string `json:"delivery"`
	AudioSelection    string `json:"audioSelection"`
	SubtitleSelection string `json:"subtitleSelection"`
	QualityID         string `json:"qualityId"`
}
type Offers struct {
	PreparedVersions              []preparedmedia.Version `json:"preparedVersions"`
	PreparedOffersRevision        string                  `json:"preparedOffersRevision"`
	DeliveryPlan                  *DeliveryPlan           `json:"deliveryPlan"`
	SubtitlePlan                  *subtitles.PlaybackPlan `json:"subtitlePlan"`
	SubtitlePlanUnavailableReason *string                 `json:"subtitlePlanUnavailableReason"`
	Scope                         OffersScope             `json:"scope"`
	Revision                      string                  `json:"revision"`
	Sources                       []SourceOffer           `json:"sources"`
	Current                       *CurrentOffer           `json:"current"`
	Controls                      []OfferControl          `json:"controls"`
	// DeliveryPolicy is what this viewer's preferences and the owner's clamps
	// resolved to on this network. Clients render the quality menu and explain
	// an automatic decision from it; they never infer it.
	DeliveryPolicy  *ResolvedDeliveryPolicy `json:"deliveryPolicy"`
	OffersRevision  string                  `json:"offersRevision"`
	TranscodingOpen bool                    `json:"transcodingEnabled"`
	// Markers belong to the source actually being played and carry their own
	// source/revision fence, exactly like chapters. They are null without a
	// current session, and for a prepared derivative whose clock is not the
	// original's.
	Markers *segmentmarkers.Set `json:"markers"`
}
