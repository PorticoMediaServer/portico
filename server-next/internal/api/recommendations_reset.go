package api

import (
	"net/http"
	"strings"
)

// Reset recommendations, in Saved › History and Privacy (area 6 rule 22): the profile's
// recommendations forget its taste and learn again from what it plays and likes next.
// History, ratings, lists, watched marks, resume points and favourites all stay.

// RecommendationsReset is the request. OperationID is the app's own id for this press; it is
// sent back so the app knows the answer is to it.
type RecommendationsReset struct {
	OperationID string `json:"operationId,omitempty"`
}

// RecommendationsResetDone is the answer: this server's id and the press's operation id.
type RecommendationsResetDone struct {
	ServerID    string `json:"serverId"`
	OperationID string `json:"operationId,omitempty"`
}

func (s *Server) resetRecommendations(r *http.Request, req RecommendationsReset) (RecommendationsResetDone, error) {
	if len(req.OperationID) > 128 || strings.ContainsFunc(req.OperationID, func(c rune) bool {
		return !(c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z')
	}) {
		return RecommendationsResetDone{}, ErrField("operationId", "Use up to 128 letters, digits, - or _.")
	}
	return RecommendationsResetDone{ServerID: s.id, OperationID: req.OperationID}, s.db.ResetTaste(r.Context(), viewer(r), s.now())
}
