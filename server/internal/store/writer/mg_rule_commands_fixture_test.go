package writer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// MG-11 reuses the reviewed MG-07 owner root fixture (real embedded A/B/G+013
// schema, US owner session and lifetime root) and adds only the native live
// source/head rows a rule identity needs. The rule id/source are 64/48 lowercase
// hex so the merged native effect's own exact-shape validation accepts them.
type mgRuleCommandValues struct {
	owner          mgRecordingOwner
	source         string
	generation     string
	sourceRevision int64
	root           string
	proof          *usOwnerAuthorityProof
	personal       mgPersonalFixtureValues
}

func mgRuleCommandFixture(t *testing.T) (*executor, string, mgRuleCommandValues) {
	t.Helper()
	w, path, personal := mgPersonalFixture(t)
	i := personal.proof.witness.issuer
	v := mgRuleCommandValues{
		owner:          mgRecordingOwner{account: i.account, profile: i.profile, memberIncarnation: i.memberIncarnation, profileLifetime: i.profileLifetime, accountEpoch: i.epoch, memberRevision: 1, profileRevision: 1},
		source:         strings.Repeat("a", 48),
		generation:     strings.Repeat("b", 48),
		sourceRevision: 3,
		root:           personal.root,
		proof:          personal.proof,
		personal:       personal,
	}
	start, end := time.Now().Add(24*time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339), time.Now().Add(25*time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO live_source_identities(id) VALUES(?)`, []any{v.source}},
		{`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,'Rule guide',1,1,1,'2026-11-07T00:00:00Z')`, []any{v.generation, v.source}},
		{`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES(?,'Rule source',3,'active',1,?,'2026-11-07T00:00:00Z')`, []any{v.source, v.generation}},
		{`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,'rule-channel','rule-provider','Channel','1','Group',0,X'01','','')`, []any{v.generation}},
		{`INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES(?,'rule-programme','rule-channel','p1','Programme',?,?,'lineage')`, []any{v.generation, start, end}},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	return w, path, v
}

func mgRuleCommandTurnDefinitions() []stmt.Definition {
	defs := mgPersonalRootDefinitions()
	defs = append(defs, mgRuleDefinitions()...)
	return append(defs, mgRuleCommandDefinitions()...)
}

// Synthetic component budgets only: the merged native effect and the lifetime
// receipt are separate statements. Parent owns measured admission.
func mgRuleCommandTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgRuleCommandTurnDefinitions()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{
		limit:    Work{4096, 10_000_000, 4096, 256 << 20, 25 * time.Millisecond},
		setup:    Work{8, 1000, 8, 8192, time.Millisecond},
		terminal: Work{8, 1000, 8, 8192, time.Millisecond},
	}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{64, 200000, 64, 4 << 20, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_ = turn.closeRows()
			_, _ = tx.rollback(nil)
		}
	})
	return turn
}

func mgRuleCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func mgRuleValuesConfig(v mgRuleCommandValues, keywords []string) mgRuleConfig {
	return mgRuleConfig{
		matchKind: "series", name: "Rule", source: v.source, series: "series-one", episodes: "all",
		keywords: keywords, allowed: []string{}, blocked: []string{}, blockedKeywords: []string{},
		options: mgRuleOptions{retentionDays: 7, episodeLimit: 10}, enabled: true,
	}
}

// mgRulePreparedWire is the stored configuration the real preparer emits for a
// semantic config: identical values, keyword padding trimmed. The writer must
// never depend on this encoder; it only compares the identity it carries.
type mgRuleWireConfig struct {
	MatchKind       string       `json:"matchKind"`
	Title           string       `json:"title"`
	ChannelID       string       `json:"channelId"`
	Name            string       `json:"name"`
	SourceID        string       `json:"sourceId"`
	SeriesID        string       `json:"seriesId"`
	Enabled         bool         `json:"enabled"`
	Episodes        string       `json:"episodes"`
	AllowedChannels []string     `json:"allowedChannels"`
	BlockedChannels []string     `json:"blockedChannels"`
	BlockedKeywords []string     `json:"blockedKeywords"`
	Keywords        []string     `json:"keywords"`
	Options         mgRuleWireOp `json:"options"`
}

type mgRuleWireOp struct {
	BeforeMS      int `json:"beforeMs"`
	AfterMS       int `json:"afterMs"`
	Priority      int `json:"priority"`
	RetentionDays int `json:"retentionDays"`
	EpisodeLimit  int `json:"episodeLimit"`
}

func mgRulePreparedWire(c mgRuleConfig) string {
	w := mgRuleWireConfig{
		MatchKind: c.matchKind, Title: c.title, ChannelID: c.channel, Name: c.name, SourceID: c.source,
		SeriesID: c.series, Enabled: c.enabled, Episodes: c.episodes,
		AllowedChannels: append([]string{}, c.allowed...), BlockedChannels: append([]string{}, c.blocked...),
		BlockedKeywords: []string{}, Keywords: []string{},
		Options: mgRuleWireOp{c.options.beforeMS, c.options.afterMS, c.options.priority, c.options.retentionDays, c.options.episodeLimit},
	}
	for _, keyword := range c.keywords {
		w.Keywords = append(w.Keywords, strings.TrimSpace(keyword))
	}
	for _, keyword := range c.blockedKeywords {
		w.BlockedKeywords = append(w.BlockedKeywords, strings.TrimSpace(keyword))
	}
	data, err := json.Marshal(w)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func mgRuleSaveRequestFor(v mgRuleCommandValues, id string, expected int64, config mgRuleConfig, prepared string) mgRuleSaveRequest {
	return mgRuleSaveRequest{
		owner:          v.owner,
		input:          mgRuleSaveInput{requestID: strings.Repeat("c", 48), id: id, expectedRevision: expected, anchor: mgRuleAnchor{source: v.source, channel: "rule-channel", generation: v.generation, programme: "rule-programme"}, config: config},
		prepared:       prepared,
		sourceRevision: v.sourceRevision,
		generation:     v.generation,
	}
}

func mgRuleDeleteRequestFor(v mgRuleCommandValues, id string, expected int64) mgRuleDeleteRequest {
	return mgRuleDeleteRequest{
		owner: v.owner, id: id,
		input:          mgRuleDeleteInput{requestID: strings.Repeat("d", 48), expectedRevision: expected, future: "keep"},
		sourceRevision: v.sourceRevision, generation: v.generation,
	}
}

func mgRuleSaveReference(t *testing.T, id CommandIdentity, r mgRuleSaveRequest) ReceiptReference {
	t.Helper()
	c, err := newMGRuleSaveCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgRuleDeleteReference(t *testing.T, id CommandIdentity, r mgRuleDeleteRequest) ReceiptReference {
	t.Helper()
	c, err := newMGRuleDeleteCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}
