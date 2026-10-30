package catalog

// ShowWorkspace resolves hard links into one bounded server-owned show view.
// Cursor fields are opaque and valid only with the same principal/query/revision.
type ShowWorkspaceRequest struct {
	PlayState, EpisodeDirection                                                string
	Viewer                                                                     Viewer
	ServerID, Library, Profile, ViewerFence                                    string
	ShowID, SeasonID, EpisodeID, SelectedSeasonID, Group, Cursor, SeasonCursor string
	Limit, SeasonLimit                                                         int
}
type ShowWorkspaceGroup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
}
type ShowWorkspaceSelection struct {
	SeasonID  *string `json:"seasonId"`
	Group     string  `json:"group,omitempty"`
	EpisodeID string  `json:"episodeId,omitempty"`
}
type ShowWorkspaceScope struct {
	ServerID    string `json:"serverId"`
	LibraryID   string `json:"libraryId"`
	ViewerFence string `json:"viewerFence"`
}

// ShowCredit is one credited person. ID is the /v1/people id (the same person
// as in movies), absent only while that person has no people row.
type ShowCredit struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Role        string `json:"role,omitempty"`
	Department  string `json:"department,omitempty"`
	Ordinal     int    `json:"ordinal"`
	PortraitURL string `json:"portraitUrl,omitempty"`
}
type ShowWorkspace struct {
	Scope            ShowWorkspaceScope     `json:"scope"`
	Revision         ContentRevision        `json:"revision"`
	Show             Show                   `json:"show"`
	ShowCredits      []ShowCredit           `json:"showCredits,omitempty"`
	EpisodeCredits   []ShowCredit           `json:"episodeCredits,omitempty"`
	NextUp           *ContentEntry          `json:"nextUp,omitempty"`
	Seasons          []Season               `json:"seasons"`
	SeasonTotalCount int                    `json:"seasonTotalCount"`
	NextSeasonCursor string                 `json:"nextSeasonCursor"`
	Groups           []ShowWorkspaceGroup   `json:"groups"`
	Selected         ShowWorkspaceSelection `json:"selected"`
	Episodes         ContentEnvelope        `json:"episodes"`
	// Settings is how the owner asked this show to be presented. Flat shows
	// (seasons hidden, or ranges) list their episodes under Groups, not Seasons.
	Settings ShowSettings `json:"settings"`
}
