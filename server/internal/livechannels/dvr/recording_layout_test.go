package dvr

import "testing"

func TestRecordingTemplateDrawnPresetsAndVocabulary(t *testing.T) {
	for _, value := range []string{
		"",
		"{series}/Season {season:02}/{series} S{season:02}E{episode:02}",
		"{title} ({year})/{title} ({year})",
		"{series} - S{season:02}E{episode:02}",
		"{title} ({year})",
		"{source}", "fixed filename",
		"{source}/{group}/{channel} {channelNumber:02}/{series} {season} {episode} {date} {time}",
	} {
		if !ValidRecordingTemplate(value) {
			t.Errorf("rejects drawn/documented template %q", value)
		}
	}
}
func TestRecordingTemplateRejectsTraversalUnknownTokensAndPadding(t *testing.T) {
	for _, value := range []string{
		"/{title}", "../{title}", "{title}/..", "{title}/./file", "{title}//file", "{title}/",
		"{title}\\file", "{title}/x:y", "{title}/x*", "{title}\x00", "{title}\n",
		"{title}/{unknown}", "{title:02}", "{series:02}/{episode}", "{title}/{season:2}",
		"{title}/{episode:03}", "{title}/{year:02}", "{title}/{episode:02:02}", "{title}/{episode", "{title}/episode}",
		"{title}/{time:02}",
	} {
		if ValidRecordingTemplate(value) {
			t.Errorf("accepts unsafe/unsupported template %q", value)
		}
	}
}
