package segmentdetect

import "strings"

// ConfidencePolicy applies to measured suggestions; a person's decision always
// wins. A chapter name or picture alone never authorizes an automatic skip.
type ConfidencePolicy struct {
	ViewerPercent    int  `json:"viewerConfidencePercent"`
	AutomaticPercent int  `json:"automaticConfidencePercent"`
	UseChapterNames  bool `json:"useChapterNames"`
}

func DefaultConfidencePolicy() ConfidencePolicy {
	return ConfidencePolicy{50, 85, true}
}

func (p ConfidencePolicy) Visible(approved bool, confidence float64, provenance string) bool {
	return approved || (p.UseChapterNames || provenance != ChapterProvenance) &&
		confidence >= float64(p.ViewerPercent)/100 && !strings.Contains(provenance, "semantic_ambiguous")
}

func (p ConfidencePolicy) AutomaticSafe(approved bool, confidence float64, provenance string) bool {
	return approved || confidence >= float64(p.AutomaticPercent)/100 && strings.HasPrefix(provenance, SeasonProvenance+":") &&
		!strings.Contains(provenance, "semantic_ambiguous")
}
