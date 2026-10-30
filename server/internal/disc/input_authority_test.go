package disc

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestFFmpegInputRequiresExactObservedTitle(t *testing.T) {
	root, fixtures := discFixtures(t)
	for _, f := range fixtures {
		t.Run(f.ID, func(t *testing.T) {
			probe, err := DecodeProbe(f.Expected)
			if err != nil {
				t.Fatal(err)
			}
			title := probe.OrderedTitles()[0]
			path := filepath.Join(root, f.Path)
			if _, err = probe.FFmpegInput(path, title, 0, nil); err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				name   string
				mutate func(*Probe, *Title)
			}{
				{"unpublished-title", func(p *Probe, title *Title) {
					if p.Kind == "bluray" {
						playlist := 99999
						title.Playlist = &playlist
					} else {
						title.Title = 99
					}
				}},
				{"changed-duration", func(_ *Probe, title *Title) { title.DurationMs++ }},
				{"changed-chapters", func(_ *Probe, title *Title) { title.Chapters = nil }},
				{"changed-tracks", func(_ *Probe, title *Title) { title.Streams = nil }},
				{"unknown-schema", func(p *Probe, _ *Title) { p.Schema = 2 }},
				{"duplicate-key", func(p *Probe, title *Title) { p.Titles = append(p.Titles, *title) }},
			} {
				t.Run(test.name, func(t *testing.T) {
					// Give each mutation separate slices and pointer values.
					raw, err := json.Marshal(probe)
					if err != nil {
						t.Fatal(err)
					}
					var changed Probe
					if err = json.Unmarshal(raw, &changed); err != nil {
						t.Fatal(err)
					}
					selected := changed.OrderedTitles()[0]
					test.mutate(&changed, &selected)
					if _, err = changed.FFmpegInput(path, selected, 0, nil); !errors.Is(err, ErrProtocol) {
						t.Fatalf("unobserved selection admitted: %v", err)
					}
				})
			}
		})
	}
}
