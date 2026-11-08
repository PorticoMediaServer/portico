package mginput

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/mglimits"
)

func ruleFixture() dvr.RuleConfig {
	return dvr.RuleConfig{SourceID: strings.Repeat("a", 48), SeriesID: "series", Name: " name ", Enabled: true, Episodes: "new", AllowedChannels: []string{}, BlockedChannels: []string{}, Keywords: []string{" x "}}
}

func prepared(t *testing.T, c dvr.RuleConfig) (string, dvr.RuleConfig) {
	t.Helper()
	out, err := PrepareRuleConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > mglimits.RuleConfigBytes {
		t.Fatal("serialized output exceeds ceiling")
	}
	var got dvr.RuleConfig
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid() {
		t.Fatal("prepared config is not publicly valid")
	}
	return out, got
}

func TestPrepareRuleConfigPadding(t *testing.T) {
	c := ruleFixture()
	c.Keywords = []string{"x" + strings.Repeat("\u2028", 70000)}
	if !c.Valid() {
		t.Fatal("issue reproducer must be publicly valid")
	}
	raw, _ := json.Marshal(c)
	if len(raw) <= mglimits.RuleConfigBytes {
		t.Fatal("issue reproducer must exceed ceiling before preparation")
	}
	_, got := prepared(t, c)
	want := c
	want.Keywords = []string{"x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("preparation changed more than keyword padding")
	}
}

func TestPrepareRuleConfigMaximum(t *testing.T) {
	c := ruleFixture()
	c.MatchKind = "title"
	c.SeriesID = ""
	c.ChannelID = fmt.Sprintf("%064x", 1)
	c.Name = strings.Repeat("<", 160)
	c.Title = strings.Repeat("<", 512)
	c.Options = dvr.Options{BeforeMs: 21600000, AfterMs: 21600000, Priority: -1000, RetentionDays: 3650, EpisodeLimit: 100000}
	for i := 1; i <= 2000; i++ {
		c.AllowedChannels = append(c.AllowedChannels, fmt.Sprintf("%064x", i))
		c.BlockedChannels = append(c.BlockedChannels, fmt.Sprintf("%064x", i+2000))
	}
	c.Keywords = nil
	for i := 0; i < 20; i++ {
		c.Keywords = append(c.Keywords, strings.Repeat("<", 128))
		c.BlockedKeywords = append(c.BlockedKeywords, strings.Repeat("\x01", 128))
	}
	_, got := prepared(t, c)
	if !reflect.DeepEqual(got, c) {
		t.Fatal("maximum config truncated or changed")
	}
}

func TestPrepareRuleConfigMatches(t *testing.T) {
	c := ruleFixture()
	c.BlockedKeywords = []string{" \tbad\u2028"}
	allowed, blocked := strings.Repeat("1", 64), strings.Repeat("2", 64)
	c.AllowedChannels = []string{allowed}
	c.BlockedChannels = []string{blocked}
	_, got := prepared(t, c)
	for _, tc := range []struct {
		name, title, channel, evidence, series string
		want                                   bool
	}{
		{"positive", "X", allowed, "new", "series", true},
		{"blocked keyword", "x BAD", allowed, "new", "series", false},
		{"blocked channel", "x", blocked, "new", "series", false},
		{"unlisted channel", "x", strings.Repeat("3", 64), "new", "series", false},
		{"repeat", "x", allowed, "repeat", "series", false},
		{"unknown", "x", allowed, "unknown", "series", false},
		{"identity", "x", allowed, "new", "other", false},
		{"missing keyword", "other", allowed, "new", "series", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := livechannels.Programme{Title: tc.title, ChannelID: tc.channel, NewEvidence: tc.evidence, SeriesID: tc.series}
			if c.Matches(p) != tc.want || got.Matches(p) != tc.want {
				t.Fatal("public match semantics changed")
			}
		})
	}
}

func TestPrepareRuleConfigOwnershipAndRequestIdentity(t *testing.T) {
	// Hash the whole original request, including mutation/default/anchor fields.
	request := dvr.RuleInput{UseDefaults: true, Mutation: dvr.Mutation{RequestID: strings.Repeat("b", 48), ExpectedRevision: 7}, ID: strings.Repeat("c", 64), Anchor: dvr.Occurrence{SourceID: strings.Repeat("a", 48), ChannelID: strings.Repeat("1", 64), Generation: strings.Repeat("d", 48), ProgrammeID: strings.Repeat("2", 64)}, Config: ruleFixture()}
	request.Config.BlockedKeywords = []string{" bad "}
	before, _ := json.Marshal(request)
	hash := sha256.Sum256(before)
	out, got := prepared(t, request.Config)
	after, _ := json.Marshal(request)
	if string(before) != string(after) || sha256.Sum256(after) != hash {
		t.Fatal("original full request changed")
	}
	request.Config.Keywords[0] = "changed"
	request.Config.BlockedKeywords[0] = "changed"
	request.Config.Name = "changed"
	request.Config.AllowedChannels = append(request.Config.AllowedChannels, "changed")
	var again dvr.RuleConfig
	if err := json.Unmarshal([]byte(out), &again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatal("serialized output aliases caller")
	}
	for _, keywords := range [][]string{nil, {}} {
		c := ruleFixture()
		c.BlockedKeywords = keywords
		_, got := prepared(t, c)
		if !reflect.DeepEqual(got.BlockedKeywords, keywords) {
			t.Fatal("optional nil/empty distinction lost")
		}
	}
}

func TestPrepareRuleConfigInvalid(t *testing.T) {
	for _, mutate := range []func(*dvr.RuleConfig){
		func(c *dvr.RuleConfig) { c.AllowedChannels = nil },
		func(c *dvr.RuleConfig) { c.BlockedChannels = nil },
		func(c *dvr.RuleConfig) { c.Keywords = nil },
		func(c *dvr.RuleConfig) { c.Keywords = []string{" \t\u2028"} },
		func(c *dvr.RuleConfig) { c.BlockedKeywords = []string{" \t"} },
		func(c *dvr.RuleConfig) { c.Options.Priority = 1001 },
		func(c *dvr.RuleConfig) { c.Keywords = []string{strings.Repeat("x", 129)} },
	} {
		c := ruleFixture()
		mutate(&c)
		out, err := PrepareRuleConfig(c)
		if out != "" || !errors.Is(err, dvr.ErrInvalid) {
			t.Fatalf("invalid input returned %q, %v", out, err)
		}
	}
}
