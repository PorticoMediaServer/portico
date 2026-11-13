package settings

import "github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect"

// SegmentSettings chooses how intros and credits are found (area 4 rule 65).
type SegmentSettings struct {
	Mode                       string `json:"mode"`
	ViewerConfidencePercent    int    `json:"viewerConfidencePercent"`
	AutomaticConfidencePercent int    `json:"automaticConfidencePercent"`
	UseChapterNames            bool   `json:"useChapterNames"`
	IntroWindow                string `json:"introWindow"`
	CreditsWindow              string `json:"creditsWindow"`
	FilmCreditsWindow          string `json:"filmCreditsWindow"`
	ShortestEpisodeMinutes     int    `json:"shortestEpisodeMinutes"`
	ShortestFilmMinutes        int    `json:"shortestFilmMinutes"`
}

func (s SegmentSettings) MarkerPolicy() segmentdetect.ConfidencePolicy {
	return segmentdetect.ConfidencePolicy{ViewerPercent: s.ViewerConfidencePercent, AutomaticPercent: s.AutomaticConfidencePercent, UseChapterNames: s.UseChapterNames}
}

var Segments = Define("segments", SegmentSettings{Mode: "standard", ViewerConfidencePercent: 50, AutomaticConfidencePercent: 85,
	UseChapterNames: true, IntroWindow: "usual", CreditsWindow: "usual", FilmCreditsWindow: "usual", ShortestEpisodeMinutes: 2, ShortestFilmMinutes: 10}, func(s *SegmentSettings) error {
	switch s.Mode {
	case "off", "standard", "thorough":
	default:
		return &Invalid{Field: "mode", Message: "Choose Off, Listening, or Listening and picture."}
	}
	if s.ViewerConfidencePercent != 30 && s.ViewerConfidencePercent != 50 && s.ViewerConfidencePercent != 70 {
		return &Invalid{Field: "viewerConfidencePercent", Message: "Choose 30, 50 or 70 percent."}
	}
	if s.AutomaticConfidencePercent != 70 && s.AutomaticConfidencePercent != 85 && s.AutomaticConfidencePercent != 95 {
		return &Invalid{Field: "automaticConfidencePercent", Message: "Choose 70, 85 or 95 percent."}
	}
	for _, f := range []struct{ name, value string }{{"introWindow", s.IntroWindow}, {"creditsWindow", s.CreditsWindow}, {"filmCreditsWindow", s.FilmCreditsWindow}} {
		if f.value != "usual" && f.value != "long" {
			return &Invalid{Field: f.name, Message: "Choose the usual or longer search."}
		}
	}
	if s.ShortestEpisodeMinutes < 1 || s.ShortestEpisodeMinutes > 30 {
		return &Invalid{Field: "shortestEpisodeMinutes", Message: "Choose from 1 to 30 minutes."}
	}
	if s.ShortestFilmMinutes < 5 || s.ShortestFilmMinutes > 60 || s.ShortestFilmMinutes%5 != 0 {
		return &Invalid{Field: "shortestFilmMinutes", Message: "Choose from 5 to 60 minutes, in steps of 5."}
	}
	return nil
})
