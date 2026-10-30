package playbackv1

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func TestQualityValidation(t *testing.T) {
	for name, q := range map[string]Quality{
		"original":             {Mode: QualityOriginal},
		"fixed":                {Mode: QualityFixed, ID: "1080p-low"},
		"automatic":            {Mode: QualityAutomatic},
		"automatic on a step":  {Mode: QualityAutomatic, ID: "720p-high"},
		"automatic step down":  {Mode: QualityAutomatic, Step: "down"},
		"limit":                {Mode: QualityLimit, MaxVideoBitrateKbps: 3000, MaxHeight: 720},
		"limit with no number": {Mode: QualityLimit},
	} {
		if err := q.Validate("quality"); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	for name, q := range map[string]Quality{
		"no mode":                  {},
		"unknown mode":             {Mode: "best"},
		"original with a number":   {Mode: QualityOriginal, MaxHeight: 720},
		"original with an id":      {Mode: QualityOriginal, ID: "720p-high"},
		"fixed without an id":      {Mode: QualityFixed},
		"fixed with an unknown id": {Mode: QualityFixed, ID: "900p"},
		"fixed with a step":        {Mode: QualityFixed, ID: "720p-high", Step: "down"},
		"automatic sideways":       {Mode: QualityAutomatic, Step: "sideways"},
		"automatic with numbers":   {Mode: QualityAutomatic, MaxHeight: 720},
		"limit with an id":         {Mode: QualityLimit, ID: "720p-high"},
		"limit out of range":       {Mode: QualityLimit, MaxHeight: 100000},
	} {
		if err := q.Validate("quality"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// A quality is ceilings, never a forced conversion, and the plan keeps its name.
func TestQualityAppliesCeilings(t *testing.T) {
	var c playback.V1Choice
	Quality{Mode: QualityFixed, ID: "720p-high"}.apply(&c)
	if !c.Limit || c.MaxVideoHeight != 720 || c.MaxVideoBitrateBPS != 4_000_000 || c.MaxAudioBitrateBPS != 128_000 || c.QualityID != "720p-high" {
		t.Fatalf("fixed: %+v", c)
	}
	Quality{Mode: QualityOriginal}.apply(&c)
	if c.Limit || c.MaxVideoHeight != 0 || c.MaxVideoBitrateBPS != 0 || c.QualityID != "original" {
		t.Fatalf("original after fixed kept ceilings: %+v", c)
	}
	Quality{Mode: QualityAutomatic}.apply(&c)
	if c.Limit || c.QualityID != "automatic" {
		t.Fatalf("automatic starts at the original: %+v", c)
	}
	Quality{Mode: QualityAutomatic, ID: "480p"}.apply(&c)
	if !c.Limit || c.MaxVideoHeight != 480 || c.QualityID != "automatic:480p" {
		t.Fatalf("automatic on a step: %+v", c)
	}
	Quality{Mode: QualityLimit, MaxVideoBitrateKbps: 1500, MaxAudioBitrateKbps: 96, MaxHeight: 540}.apply(&c)
	if !c.Limit || c.MaxVideoBitrateBPS != 1_500_000 || c.MaxAudioBitrateBPS != 96_000 || c.MaxVideoHeight != 540 || c.QualityID != "limit" {
		t.Fatalf("limit: %+v", c)
	}
	for name, want := range map[string]Quality{"": {Mode: QualityOriginal}, "original": {Mode: QualityOriginal}, "automatic": {Mode: QualityAutomatic}, "1080p-low": {Mode: QualityFixed, ID: "1080p-low"}, "data-saver": {Mode: QualityOriginal}} {
		if got := savedQuality(name); got != want {
			t.Errorf("saved %q is %+v, want %+v", name, got, want)
		}
	}
}

// Automatic starts at the original and moves along the ladder one step at a
// time, skipping steps that would not make the stream smaller.
func TestAutomaticQualitySteps(t *testing.T) {
	film := SourceFacts{Height: 1080, BitrateKbps: 12_000}
	at := Quality{Mode: QualityAutomatic}
	for _, want := range []string{"1080p-low", "720p-high", "720p-low", "480p", "240p", "240p"} {
		at = stepAutomatic(at, "down", film)
		if at.Mode != QualityAutomatic || at.ID != want || at.Step != "" {
			t.Fatalf("down: %+v, want %s", at, want)
		}
	}
	for _, want := range []string{"480p", "720p-low", "720p-high", "1080p-low", "", ""} {
		at = stepAutomatic(at, "up", film)
		if at.ID != want {
			t.Fatalf("up: %+v, want %q", at, want)
		}
	}
	// A 1080p file at 5 Mb/s: the 1080p step (8 Mb/s) is no step down at all.
	small := SourceFacts{Height: 1080, BitrateKbps: 5000}
	if got := stepAutomatic(Quality{Mode: QualityAutomatic}, "down", small); got.ID != "720p-high" {
		t.Fatalf("first useful step: %+v", got)
	}
	if got := stepAutomatic(Quality{Mode: QualityAutomatic, ID: "720p-high"}, "up", small); got.ID != "" {
		t.Fatalf("up from the widest useful step is the original: %+v", got)
	}
	// A step that does not apply to this file (carried over from another title).
	if got := stepAutomatic(Quality{Mode: QualityAutomatic, ID: "1080p-high"}, "down", small); got.ID != "720p-high" {
		t.Fatalf("down from a step above the file: %+v", got)
	}
	// Nothing known about the file: the whole ladder, widest first.
	if got := stepAutomatic(Quality{Mode: QualityAutomatic}, "down", SourceFacts{}); got.ID != "" {
		t.Fatalf("unknown file: %+v", got)
	}
	// A picture smaller than every step keeps the smallest one.
	if got := stepAutomatic(Quality{Mode: QualityAutomatic}, "down", SourceFacts{Height: 240, BitrateKbps: 3000}); got.ID != "240p" {
		t.Fatalf("tiny picture: %+v", got)
	}
}

// A measured connection lands automatic on the widest quality that fits four
// fifths of it, and names the quality a refused start could use instead.
func TestAutomaticQualityFitsTheConnection(t *testing.T) {
	film := SourceFacts{Height: 2160, BitrateKbps: 40_000}
	for throughput, want := range map[int]string{0: "", 60_000: "", 30_000: "1080p-high", 12_000: "1080p-low", 6000: "720p-high", 3000: "720p-low", 1500: "240p", 300: "240p"} {
		if got := fitAutomatic(throughput, film); got.Mode != QualityAutomatic || got.ID != want {
			t.Errorf("%d kbps: %+v, want %q", throughput, got, want)
		}
	}
	// A file whose bitrate is unknown is taken to fit.
	if got := fitAutomatic(2000, SourceFacts{Height: 1080}); got.ID != "" {
		t.Fatalf("unknown bitrate: %+v", got)
	}
	if got := suggestedQuality(6000, film); got != "720p-high" {
		t.Fatalf("suggestion: %q", got)
	}
}

func TestQualityMenu(t *testing.T) {
	menu := qualityOptions(SourceFacts{Height: 1080, BitrateKbps: 9000}, false)
	want := []string{"original", "automatic", "2160p", "1080p-high", "1080p-low", "720p-high", "720p-low", "480p", "240p"}
	if len(menu) != len(want) {
		t.Fatalf("menu %+v", menu)
	}
	for i, o := range menu {
		name := o.Mode
		if o.Mode == QualityFixed {
			name = o.ID
		}
		if name != want[i] {
			t.Fatalf("menu[%d]=%s", i, name)
		}
	}
	if !menu[2].SameAsOriginal || !menu[3].SameAsOriginal || menu[4].SameAsOriginal || menu[4].MaxVideoBitrateKbps != 8000 {
		t.Fatalf("partial ordering %+v", menu)
	}
	if qualityOptions(SourceFacts{Height: 1080}, false)[2].SameAsOriginal {
		t.Fatal("guessed missing bitrate")
	}
	if qualityOptions(SourceFacts{BitrateKbps: 1000}, false)[2].SameAsOriginal {
		t.Fatal("guessed missing resolution")
	}
	music := qualityOptions(SourceFacts{}, true)
	if len(music) != 9 || !music[1].Applicable {
		t.Fatalf("audio menu %+v", music)
	}
	for _, q := range music[2:] {
		if q.Applicable || q.SameAsOriginal {
			t.Fatal("invented video quality for audio")
		}
	}
}

func TestPlanNeedsWhatItDelivers(t *testing.T) {
	trace := &playback.DecisionTrace{BitRate: 25_000_000, Video: &playback.TraceVideo{Height: 2160}}
	if got := planKbps(playback.DeliveryPlan{VideoAction: "original", AudioAction: "original", Trace: trace}); got != 25_000 {
		t.Fatalf("original: %d", got)
	}
	if got := planKbps(playback.DeliveryPlan{VideoAction: "copy", AudioAction: "convert", AudioBitrateBPS: 384_000, Trace: trace}); got != 25_000 {
		t.Fatalf("copied picture: %d", got)
	}
	if got := planKbps(playback.DeliveryPlan{VideoAction: "convert", VideoBitrateBPS: 4_000_000, AudioBitrateBPS: 128_000, Trace: trace}); got != 4128 {
		t.Fatalf("conversion: %d", got)
	}
	plan := playback.DeliveryPlan{Trace: trace}
	if facts := sourceFactsOf(&plan); facts.Height != 2160 || facts.BitrateKbps != 25_000 {
		t.Fatalf("facts %+v", facts)
	}
}

// What a playback carries to its next entry is applied by language: the best
// track in it, off when subtitles were turned off, and nothing when no track
// matches (the Settings then decide).
func TestCarriedSubtitleChoice(t *testing.T) {
	tracks := subtitles.PlaybackPlan{Resources: []subtitles.Resource{
		{ID: "eng-forced", Language: "eng", Forced: true, Enabled: true, Renderer: "external_text"},
		{ID: "eng-pgs", Language: "en", Enabled: true, Renderer: "burn_in", Format: "pgs"},
		{ID: "eng-full", Language: "en-US", Enabled: true, Renderer: "external_text"},
		{ID: "jpn", Language: "jpn", Enabled: true, Renderer: "external_text"},
		{ID: "ger-off", Language: "ger", Enabled: false, Renderer: "external_text"},
	}}
	if got := carriedSubtitle(&TrackCarry{SubtitleMode: "on", SubtitleLanguage: "en"}, tracks); got == nil || got.TrackID != "eng-full" {
		t.Fatalf("english: %+v", got)
	}
	if got := carriedSubtitle(&TrackCarry{SubtitleMode: "on", SubtitleLanguage: "ja"}, tracks); got == nil || got.TrackID != "jpn" {
		t.Fatalf("japanese: %+v", got)
	}
	if got := carriedSubtitle(&TrackCarry{SubtitleMode: "off"}, tracks); got == nil || got.TrackID != TrackOff {
		t.Fatalf("off: %+v", got)
	}
	if got := carriedSubtitle(nil, tracks); got != nil {
		t.Errorf("nothing carried chose %+v", got)
	}
	for name, carry := range map[string]TrackCarry{"no subtitle choice": {AudioLanguage: "en"}, "a language this file lacks": {SubtitleMode: "on", SubtitleLanguage: "fr"}, "a track that is not selectable": {SubtitleMode: "on", SubtitleLanguage: "de"}} {
		if got := carriedSubtitle(&carry, tracks); got != nil {
			t.Errorf("%s chose %+v", name, got)
		}
	}
	for in, want := range map[string]string{"eng": "en", "en-GB": "en", "JPN": "ja", "und": "", "": ""} {
		if got := trackLanguage(in); got != want {
			t.Errorf("language %q folds to %q, want %q", in, got, want)
		}
	}
	// Styled and image tracks weigh in the engine choice; text and off do not.
	pgs := &tracks.Resources[1]
	if got := subtitleWeight(pgs, &SubtitleRequest{TrackID: "eng-pgs"}, true); got != playback.SubtitlePGS {
		t.Fatalf("pgs weighs %q", got)
	}
	if got := subtitleWeight(pgs, &SubtitleRequest{TrackID: "eng-pgs", Delivery: "burn"}, true); got != playback.SubtitleBurnAlways {
		t.Fatalf("burn weighs %q", got)
	}
	if got := subtitleWeight(pgs, &SubtitleRequest{TrackID: "eng-pgs", Delivery: "embeddedClient"}, true); got != "" {
		t.Fatalf("a track the client draws weighs %q", got)
	}
	if got := subtitleWeight(&tracks.Resources[2], &SubtitleRequest{TrackID: "eng-full"}, true); got != "" {
		t.Fatalf("text weighs %q", got)
	}
	// A styled track shown as plain words weighs nothing: asked for as a
	// sidecar, or with "Styled subtitles" off and no delivery named. An image
	// track cannot be plain words, and an explicit burn still burns.
	styled := &subtitles.Resource{ID: "eng-ass", Format: "ass", Renderer: "burn_in", Enabled: true}
	if got := subtitleWeight(styled, &SubtitleRequest{TrackID: "eng-ass"}, true); got != playback.SubtitleASS {
		t.Fatalf("styled kept weighs %q", got)
	}
	for _, c := range []struct {
		req  SubtitleRequest
		keep bool
	}{{SubtitleRequest{TrackID: "eng-ass", Delivery: "sidecar"}, true}, {SubtitleRequest{TrackID: "eng-ass"}, false}, {SubtitleRequest{TrackID: "eng-ass", Delivery: "auto"}, false}} {
		if got := subtitleWeight(styled, &c.req, c.keep); got != "" || !styledAsText(styled, &c.req, c.keep) {
			t.Fatalf("styled as text %+v keep=%v weighs %q", c.req, c.keep, got)
		}
	}
	if got := subtitleWeight(styled, &SubtitleRequest{TrackID: "eng-ass", Delivery: "burn"}, false); got != playback.SubtitleBurnAlways {
		t.Fatalf("an explicit burn with styling off weighs %q", got)
	}
	if styledAsText(pgs, &SubtitleRequest{TrackID: "eng-pgs", Delivery: "sidecar"}, false) || subtitleWeight(pgs, &SubtitleRequest{TrackID: "eng-pgs"}, false) != playback.SubtitlePGS {
		t.Fatal("an image track is never plain words")
	}
	for format, kind := range map[string]string{"ass": "ass", "SSA": "ass", "hdmv_pgs_subtitle": "pgs", "dvd_subtitle": "vobsub", "dvb_subtitle": "dvb", "subrip": "text", "webvtt": "text"} {
		if got := subtitleKindOf(format); got != kind {
			t.Errorf("%s is %s, want %s", format, got, kind)
		}
	}
}
