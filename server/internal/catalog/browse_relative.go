package catalog

import (
	"encoding/json"
	"math"
	"time"
)

func validateBrowseDays(value any, path string) (int, error) {
	var number float64
	switch v := value.(type) {
	case json.Number:
		var err error
		number, err = v.Float64()
		if err != nil {
			return 0, browseIssue(path, "must be an integer from 1 to 36500 days")
		}
	case float64:
		number = v
	case int:
		number = float64(v)
	default:
		return 0, browseIssue(path, "must be an integer from 1 to 36500 days")
	}
	if math.IsNaN(number) || number < 1 || number > 36500 || math.Trunc(number) != number {
		return 0, browseIssue(path, "must be an integer from 1 to 36500 days")
	}
	return int(number), nil
}

func browseRelative(node *BrowseNode) bool {
	if node == nil {
		return false
	}
	if node.Operator == "within-last-days" {
		return true
	}
	if browseRelative(node.Not) {
		return true
	}
	for i := range node.All {
		if browseRelative(&node.All[i]) {
			return true
		}
	}
	for i := range node.Any {
		if browseRelative(&node.Any[i]) {
			return true
		}
	}
	return false
}

// Relative reads use their exact instant in every revision/cache key. No
// revision-only cache can reuse a result from an older instant. Continuations
// recover the authenticated instant before compilation; normal scope and
// content revision checks still run in the paging engine.
func (s *Service) withBrowseInstant(node *BrowseNode, cursor string) (*Service, error) {
	if !browseRelative(node) {
		return s, nil
	}
	at := s.recommendationNow(s.browseInstant).UTC()
	if cursor != "" {
		value, err := s.readSignedBrowseCursor(cursor)
		if err != nil {
			return s, err
		}
		at, err = time.Parse(time.RFC3339Nano, value.EvaluationAt)
		if err != nil {
			return s, ErrCursor
		}
	}
	copy := *s
	copy.browseInstant = at
	return &copy, nil
}

func (s *Service) relativeBrowseTime(node *BrowseNode) string {
	if !browseRelative(node) {
		return ""
	}
	return s.browseInstant.UTC().Format(time.RFC3339Nano)
}
func (s *Service) relativeBrowseFence(node *BrowseNode) string {
	if !browseRelative(node) {
		return ""
	}
	return "\x00at:" + s.relativeBrowseTime(node)
}

// Standalone compilers have no Service. Callers such as Library Channels must
// supply their composition instant for a relative expression; a missing clock
// stays unknown and the compiler refuses it. Absolute expressions need none.
func browseSelectionInstant(evaluation []time.Time) time.Time {
	if len(evaluation) > 0 && !evaluation[0].IsZero() {
		return evaluation[0]
	}
	return time.Time{}
}
