package catalog

type ResourceActor struct {
	Authority string `json:"authority"`
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId"`
}
type PlaylistLimits struct {
	MaxEntries        int `json:"maxEntries,omitempty"`
	MaxShares         int `json:"maxShares"`
	MaxReorderEntries int `json:"maxReorderEntries"`
}
type PlaylistShare struct {
	ResourceActor
	Role        string `json:"role"`
	DisplayName string `json:"displayName"`
}
type Playlist struct {
	Pinned      bool            `json:"pinned"`
	PinRevision int64           `json:"pinRevision"`
	ServerID    string          `json:"serverId"`
	ViewerFence string          `json:"viewerFence"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Summary     string          `json:"summary"`
	Revision    int64           `json:"revision"`
	Role        string          `json:"role"`
	EntryCount  int             `json:"entryCount"`
	Actions     []string        `json:"actions"`
	Limits      PlaylistLimits  `json:"limits"`
	Shares      []PlaylistShare `json:"shares,omitempty"`
}
type PlaylistMutation struct {
	OperationID      string   `json:"operationId"`
	ExpectedRevision int64    `json:"expectedRevision,omitempty"`
	Name             *string  `json:"name,omitempty"`
	Summary          *string  `json:"summary,omitempty"`
	ItemID           string   `json:"itemId,omitempty"`
	ItemIDs          []string `json:"itemIds,omitempty"`
	// UndoToken, on an add, puts back the occurrence a removal just took out
	// (the removal's receipt carries it) in place of adding an item.
	UndoToken    string   `json:"undoToken,omitempty"`
	EntryIDs     []string `json:"entryIds,omitempty"`
	AfterEntryID *string  `json:"afterEntryId,omitempty"`
	Authority    string   `json:"authority,omitempty"`
	AccountID    string   `json:"accountId,omitempty"`
	ProfileID    string   `json:"profileId,omitempty"`
	Role         string   `json:"role,omitempty"`
}
type PlaylistReceipt struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
	OperationID string `json:"operationId"`
	PlaylistID  string `json:"playlistId"`
	Revision    int64  `json:"revision"`
	EntryID     string `json:"entryId,omitempty"`
	Deleted     bool   `json:"deleted"`
	// A removal can be undone until UndoUntil by adding with UndoToken.
	UndoToken string `json:"undoToken,omitempty"`
	UndoUntil string `json:"undoUntil,omitempty"`
}
