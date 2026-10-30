package librarychannels

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"strings"
	"testing"
	"time"
)

func TestConfigCollectionsAndDormantBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := openFixture(t)
	s, _ := New(f.db)
	c := channel("config-contract", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}, "sequential", "none")
	c.Enabled = false
	saved, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save-config", Config: c})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := s.List(context.Background(), ownerAuthority(nil))
	if err != nil || len(listed) != 1 {
		t.Fatalf("list %v %v", listed, err)
	}
	replay, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "save-config", Config: c})
	if err != nil {
		t.Fatal(err)
	}
	doc, schema, err := apispec.Schema("LibraryChannelConfig")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []Config{c, saved.Config, listed[0].Config, replay.Config} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if issues := doc.ValidateJSON(schema, raw); len(issues) > 0 {
			t.Fatalf("%v: %s", issues, raw)
		}
		var obj map[string]any
		json.Unmarshal(raw, &obj)
		obj["blocks"] = nil
		bad, _ := json.Marshal(obj)
		if len(doc.ValidateJSON(schema, bad)) == 0 {
			t.Fatal("schema accepts null blocks")
		}
	}
	for name, edit := range map[string]func(*Config){
		"template":       func(c *Config) { c.TemplateID = strings.Repeat("x", 129) },
		"corner":         func(c *Config) { c.Overlay.Corner = strings.Repeat("x", 41) },
		"treatment":      func(c *Config) { c.Overlay.Treatment = strings.Repeat("x", 41) },
		"negative-size":  func(c *Config) { c.Overlay.SizePercent = -1 },
		"size":           func(c *Config) { c.Overlay.SizePercent = 101 },
		"negative-inset": func(c *Config) { c.Overlay.InsetPercent = -1 },
		"inset":          func(c *Config) { c.Overlay.InsetPercent = 101 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := c
			edit(&bad)
			if _, err := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "bad-" + name, ExpectedRevision: saved.Revision, Config: bad}); err == nil {
				t.Fatal("accepted invalid dormant value")
			}
		})
	}
	// Marshalling must not mutate the caller's slice elements.
	if c.Blocks != nil || c.Rules[0].Weights != nil || c.Rules[0].Query.ShowIDs != nil {
		t.Fatal("wire normalization mutated input")
	}
}

func TestLibraryGuideCopiedBlockRecipeSurvivesPresetChanges(t *testing.T) {
	f := justinsLibrary(t)
	s, e := New(f.db)
	if e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return f.now }
	c := channel("copied-recipe", Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}, "sequential", "none")
	c.Rules = append(c.Rules, Rule{ID: "recipe-copy", Name: "1985 films", Query: Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title", Filter: raw(`{"variant":"predicate","field":"year","operator":"equals","value":1985}`)}, Mode: "sequential", EpisodeMode: "none", Exhaustion: "loop", MaxConsecutive: 100})
	c.Blocks = []Block{{ID: "afternoon", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, StartMinute: 15 * 60, EndMinute: 18 * 60, Priority: 1, RuleID: "recipe-copy", FallbackRuleID: "main", Anchor: "block-start", Overrun: "finish"}}
	f.exec(t, `INSERT INTO admin_channel_presets VALUES('recipe','1985 films','1985 films',1,'{"year":1985}',1,1)`)
	saved, e := s.Save(context.Background(), ownerAuthority(nil), SaveInput{RequestID: "copy-recipe", Config: c})
	if e != nil {
		t.Fatal(e)
	}
	preview, e := s.Preview(context.Background(), ownerAuthority(nil), saved.Config)
	if e != nil || len(preview.FirstDay) == 0 {
		t.Fatalf("preview %v %+v", e, preview)
	}
	found := false
	for _, entry := range preview.FirstDay {
		if strings.HasPrefix(entry.BlockID, "afternoon:") {
			found = true
			if entry.RuleID != "recipe-copy" {
				t.Fatal("block not reflected in firstDay", entry)
			}
		}
	}
	if !found {
		t.Fatalf("firstDay omitted installed block: blocks=%+v day=%+v",preview.Blocks,preview.FirstDay)
	}
	f.exec(t, `UPDATE admin_channel_presets SET revision=2,body='{"year":1990}' WHERE id='recipe'`)
	list, e := s.List(context.Background(), ownerAuthority(nil))
	if e != nil || len(list) != 1 || list[0].Config.Rules[1].Query.Filter == nil || string(list[0].Config.Rules[1].Query.Filter) != string(c.Rules[1].Query.Filter) {
		t.Fatalf("preset changed installed copy %+v %v", list, e)
	}
	f.exec(t, `DELETE FROM admin_channel_presets WHERE id='recipe'`)
	list, e = s.List(context.Background(), ownerAuthority(nil))
	if e != nil || len(list) != 1 || len(list[0].Config.Blocks) != 1 {
		t.Fatal("preset deletion changed channel", list, e)
	}
}
