package dvr

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestRecordingLayoutSection22ExactPathsAndMissingValueFallback(t *testing.T) {
	values := map[string]string{"series": "Nova", "title": "The Planets", "season": "3", "episode": "7", "year": "2024"}
	for _, v := range []struct{ template, want string }{
		{"{series}/Season {season:02}/{series} S{season:02}E{episode:02}", "Nova/Season 03/Nova S03E07.ts"},
		{"{title} ({year})/{title} ({year})", "The Planets (2024)/The Planets (2024).ts"},
		{"{series} - S{season:02}E{episode:02}", "Nova - S03E07.ts"},
		{"{title} ({year})", "The Planets (2024).ts"},
	} {
		path, fallback, err := renderRecordingTemplate(v.template, values, ".ts")
		if err != nil || fallback || path != v.want {
			t.Fatalf("%q => %q fallback%v %v", v.template, path, fallback, err)
		}
	}
	for _, token := range []string{"season", "episode", "year", "source", "group", "channel"} {
		v := map[string]string{"title": "Known"}
		if path, fallback, err := renderRecordingTemplate("{title}/{"+token+"}", v, ".ts"); err != nil || !fallback || path != "" {
			t.Fatalf("missing%s failed rather than own fallback: %q %v %v", token, path, fallback, err)
		}
	}
	if path, fallback, err := renderRecordingTemplate("{title}", map[string]string{"title": "Known"}, ".mkv"); err != nil || fallback || path != "Known.mkv" {
		t.Fatal("invented/missing container extension", path, fallback, err)
	}
}
func TestRecordingLayoutSanitizesTokenSegmentsAndKeepsByteBound(t *testing.T) {
	values := map[string]string{"title": " . A/B\\C:D*E?F\"G<H>I|J\n . "}
	path, fallback, err := renderRecordingTemplate("{title}", values, ".ts")
	if err != nil || fallback || path != "A-B-C-D-E-F-G-H-I-J-.ts" {
		t.Fatal(path, fallback, err)
	}
	values["title"] = strings.Repeat("界", 300)
	path, fallback, err = renderRecordingTemplate("{title}/{title}", values, ".ts")
	if err != nil || fallback || !utf8.ValidString(path) {
		t.Fatal(path, fallback, err)
	}
	for _, segment := range strings.Split(path, "/") {
		if len(segment) > 200 {
			t.Fatalf("unbounded segment%d", len(segment))
		}
	}
	candidate, err := layoutCollisionCandidate(path, 2)
	if err != nil || !strings.HasSuffix(candidate, " (2).ts") || !utf8.ValidString(candidate) {
		t.Fatal(candidate, err)
	}
	for _, segment := range strings.Split(candidate, "/") {
		if len(segment) > 200 {
			t.Fatalf("unbounded collision segment%d", len(segment))
		}
	}
}
func TestRecordingLayoutDatesUseScheduledStartInHostZone(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("Host", -3*3600)
	defer func() { time.Local = old }()
	r := Recording{Start: "2026-10-23T01:25:00Z", Programme: livechannels.Programme{Start: "2026-10-23T02:00:00Z", Title: "Program"}}
	values := recordingTemplateValues(r, "", "")
	path, fallback, err := renderRecordingTemplate("{title} {date} {time}", values, ".ts")
	if err != nil || fallback || path != "Program 2026-10-22 22-25.ts" {
		t.Fatal("wrong scheduled start/zone", path, fallback, err)
	}
}

func TestRecordingLayoutCollisionOrdinalSurvivesRestartAndTitleSuffix(t *testing.T) {
	for _, v := range []struct {
		base string
		n    int
		want string
	}{
		{"Program.ts", 1, "Program.ts"}, {"Program.ts", 2, "Program (2).ts"}, {"Program.ts", 4, "Program (4).ts"},
		{"Program (2).ts", 3, "Program (2) (3).ts"},
	} {
		got, err := layoutCollisionCandidate(v.base, v.n)
		if err != nil || got != v.want {
			t.Fatal(got, err)
		}
	}
}
