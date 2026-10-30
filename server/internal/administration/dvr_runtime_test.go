package administration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
)

// The toggle path checks capture availability but does not start a capture.
type ruleToggleDriver struct{}

func (ruleToggleDriver) Available() bool           { return true }
func (ruleToggleDriver) RetirementAvailable() bool { return false }
func (ruleToggleDriver) Capture(context.Context, dvr.CaptureRequest, func(dvr.CaptureCheckpoint) error) (dvr.CaptureResult, error) {
	panic("unexpected capture")
}
func (ruleToggleDriver) Recover(context.Context, dvr.CaptureRequest, dvr.CaptureCheckpoint) (dvr.CaptureResult, error) {
	panic("unexpected recovery")
}
func (ruleToggleDriver) Remove(context.Context, livechannels.Owner, string, mediaartifact.Object) error {
	panic("unexpected remove")
}

func TestAdministrativeGroupOwnsRealRuleAndLegacyRemainsDisabled(t *testing.T) {
	s, db, _ := newService(t)
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	s.DVR, err = dvr.New(db, live, func(ctx context.Context, tx *sql.Tx, o livechannels.Owner, _, _ string) error {
		return recordingaccess.GrantedTx(ctx, tx, o)
	})
	if err != nil {
		t.Fatal(err)
	}
	src, gen, ch, prog := strings.Repeat("a", 48), strings.Repeat("b", 48), strings.Repeat("c", 64), strings.Repeat("d", 64)
	now := time.Now().UTC()
	start, end := now.Add(time.Hour).Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339)
	queries := []struct {
		q    string
		args []any
	}{{`INSERT INTO live_source_identities VALUES(?)`, []any{src}}, {`INSERT INTO live_generations VALUES(?,?,'Guide',2,1,1,?)`, []any{gen, src, start}}, {`INSERT INTO live_sources VALUES(?,'Aerial',1,'active',2,?,?)`, []any{src, gen, start}}, {`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'channel','Channel','1',NULL,'',1,x'00','','')`, []any{gen, ch}}, {`INSERT INTO live_programmes VALUES(?,?,?,'programme','Episode',?,?,'lineage')`, []any{gen, prog, ch, start, end}}, {`INSERT INTO live_programme_metadata(generation_id,id,series_id,episode_id,new_evidence,description) VALUES(?,?,'series','episode','new','')`, []any{gen, prog}}, {`INSERT INTO admin_dvr_groups VALUES('legacy','keyword','Legacy','news','',1,1,'{}',1,1)`, nil}}
	for _, q := range queries {
		if _, err = db.Exec(q.q, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	c := RecordingGroupChange{Kind: "series", Name: "Series", Match: "series", SourceID: src, OperationID: "real-rule", Anchor: dvr.Occurrence{SourceID: src, ChannelID: ch, Generation: gen, ProgrammeID: prog}}
	if _, err = s.SaveRecordingGroup(ctx, allow, "", c); err == nil {
		t.Fatal("ownerless group accepted")
	}
	c.Owner = livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	if _, err = s.SaveRecordingGroup(ctx, allow, "", c); err == nil {
		t.Fatal("administration bypassed the profile's Recordings switch")
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('server-owner','owner',x'00','owner-profile',1); INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','member',x'00','profile',1); INSERT INTO profile_restrictions(profile_id,allow_dvr) VALUES('profile',1)`); err != nil {
		t.Fatal(err)
	}
	rule, err := s.SaveRecordingGroup(ctx, allow, "", c)
	if err != nil {
		t.Fatal(err)
	}
	if rule.Owner != c.Owner || rule.Options.PrePaddingMs == nil || *rule.Options.PrePaddingMs != 60000 {
		t.Fatal(rule)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM dvr_rule_work WHERE rule_id=?`, rule.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("saved group has no scheduler intent", count, err)
	}
	page, err := s.RecordingGroups(ctx, allow, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatal(page)
	}
	for _, g := range page.Items {
		if g.ID == "legacy" && (g.Enabled || g.Status != "needs-owner-and-guide-selection") {
			t.Fatal(g)
		}
	}
	// A rule created through the DVR API can carry criteria and options that
	// are absent from the administrative projection.
	var raw string
	if err = db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, rule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var original dvr.RuleConfig
	if err = json.Unmarshal([]byte(raw), &original); err != nil {
		t.Fatal(err)
	}
	original.AllowedChannels = []string{ch}
	original.BlockedChannels = []string{strings.Repeat("f", 64)}
	original.BlockedKeywords = []string{"repeat"}
	original.Keywords = []string{"episode"}
	original.Options = dvr.Options{BeforeMs: 3600000, AfterMs: 7200000, Priority: 500, RetentionDays: 30, EpisodeLimit: 5000}
	direct, err := s.DVR.SaveRule(ctx, groupAuthority(allow), c.Owner, dvr.RuleInput{ID: rule.ID, Mutation: dvr.Mutation{ExpectedRevision: rule.Revision, RequestID: strings.Repeat("e", 48)}, Config: original})
	if err != nil {
		t.Fatal(err)
	}
	projected := groupFromRule(direct, c.Owner)
	c.ExpectedRevision, c.OperationID, c.Options, c.Name = direct.Revision, "rename-rule", projected.Options, "Renamed"
	updated, err := s.SaveRecordingGroup(ctx, allow, rule.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, rule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var retained dvr.RuleConfig
	if err = json.Unmarshal([]byte(raw), &retained); err != nil {
		t.Fatal(err)
	}
	original.Name = "Renamed"
	if !reflect.DeepEqual(retained, original) {
		t.Fatalf("projection lost original config: got %+v want %+v", retained, original)
	}
	if _, err = s.SaveRecordingGroup(ctx, allow, rule.ID, c); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	stale := c
	stale.OperationID = "stale-edit"
	if _, err = s.SaveRecordingGroup(ctx, allow, rule.ID, stale); err != ErrConflict {
		t.Fatalf("stale edit: %v", err)
	}
	rule = updated
	c.ExpectedRevision = rule.Revision
	c.OperationID = "enable-rule"
	c.Enabled = true
	if _, err = s.SaveRecordingGroup(ctx, allow, rule.ID, c); err != ErrUnavailable {
		t.Fatal("promised capture with no driver", err)
	}
	locks, err := livechannels.NewPhysicalLocks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DVR.ConfigureCapture(ruleToggleDriver{}, locks); err != nil {
		t.Fatal(err)
	}
	c.OperationID = "resume-with-driver"
	resumed, err := s.SaveRecordingGroup(ctx, allow, rule.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	original.Enabled = true
	if err = db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, rule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &retained); err != nil || !reflect.DeepEqual(retained, original) {
		t.Fatalf("resume changed config: %+v %v", retained, err)
	}
	c.ExpectedRevision, c.OperationID, c.Enabled = resumed.Revision, "pause-with-driver", false
	paused, err := s.SaveRecordingGroup(ctx, allow, rule.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	original.Enabled = false
	if err = db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, rule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &retained); err != nil || !reflect.DeepEqual(retained, original) {
		t.Fatalf("pause changed config: %+v %v", retained, err)
	}
	updated = paused
	c.ExpectedRevision, c.OperationID = updated.Revision, "change-policy"
	c.Options.Keep = &KeepPolicy{Mode: "keep-days", KeepDays: 14}
	updated, err = s.SaveRecordingGroup(ctx, allow, rule.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, rule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &retained); err != nil {
		t.Fatal(err)
	}
	original.Options.RetentionDays, original.Options.EpisodeLimit = 14, 0
	if !reflect.DeepEqual(retained, original) {
		t.Fatalf("policy edit changed other config: got %+v want %+v", retained, original)
	}
	wrongOwner := c
	wrongOwner.Owner.ProfileID = "other"
	wrongOwner.ExpectedRevision, wrongOwner.OperationID = updated.Revision, "wrong-owner"
	if _, err = s.SaveRecordingGroup(ctx, allow, rule.ID, wrongOwner); err == nil {
		t.Fatal("wrong owner accepted")
	}
	changedSource := c
	changedSource.SourceID = strings.Repeat("9", 48)
	changedSource.ExpectedRevision, changedSource.OperationID = updated.Revision, "changed-source"
	if _, err = s.SaveRecordingGroup(ctx, allow, rule.ID, changedSource); err == nil {
		t.Fatal("changed source accepted")
	}
	// A fresh operation cannot use an owner whose recording grant was revoked.
	if _, err = db.Exec(`UPDATE profile_restrictions SET allow_dvr=0 WHERE profile_id='profile'`); err != nil {
		t.Fatal(err)
	}
	denied := c
	denied.ExpectedRevision, denied.OperationID = updated.Revision, "revoked-rule"
	if _, err = s.SaveRecordingGroup(ctx, allow, rule.ID, denied); err != ErrDenied {
		t.Fatalf("revoked grant: %v", err)
	}
	if _, err = db.Exec(`UPDATE profile_restrictions SET allow_dvr=1 WHERE profile_id='profile'`); err != nil {
		t.Fatal(err)
	}
	// Changing current defaults cannot reinterpret an unchanged administrative
	// projection of this already-snapshotted rule.
	document, err := s.DVRSettings(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	defaults := document.Settings
	defaults.PrePaddingMs, defaults.PostPaddingMs = 120000, 180000
	if _, err = s.SaveDVRSettings(ctx, allow, Change[DVRDefaults]{ExpectedRevision: document.Revision, OperationID: "change-current-defaults", Settings: defaults}); err != nil {
		t.Fatal(err)
	}
	c.ExpectedRevision, c.OperationID = updated.Revision, "unchanged-after-defaults"
	updated, err = s.SaveRecordingGroup(ctx, allow, rule.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	stored := func() dvr.RuleConfig {
		t.Helper()
		var data string
		var value dvr.RuleConfig
		if err := db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, rule.ID).Scan(&data); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := stored(); !reflect.DeepEqual(got, original) {
		t.Fatalf("new defaults rewrote unchanged projection: %+v", got)
	}
	// Only an explicit changed null asks to snapshot today's default.
	c.ExpectedRevision, c.OperationID, c.Options.PrePaddingMs = updated.Revision, "explicit-null-default", nil
	updated, err = s.SaveRecordingGroup(ctx, allow, rule.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	original.Options.BeforeMs = 120000
	if got := stored(); !reflect.DeepEqual(got, original) {
		t.Fatalf("changed null touched unrelated fields: %+v", got)
	}
	// The canonical DVR API can create the complete maxima simultaneously;
	// an unchanged administrative save must retain all of them, including
	// both retention dimensions hidden behind the one projected Keep policy.
	original.Options = dvr.Options{BeforeMs: 21600000, AfterMs: 21600000, Priority: 1000, RetentionDays: 3650, EpisodeLimit: 100000}
	maxRule, err := s.DVR.SaveRule(ctx, groupAuthority(allow), c.Owner, dvr.RuleInput{ID: rule.ID, Mutation: dvr.Mutation{ExpectedRevision: updated.Revision, RequestID: strings.Repeat("8", 48)}, Config: original})
	if err != nil {
		t.Fatal(err)
	}
	c.ExpectedRevision, c.OperationID, c.Options = maxRule.Revision, "maxima-projection", groupFromRule(maxRule, c.Owner).Options
	updated, err = s.SaveRecordingGroup(ctx, allow, rule.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored(); !reflect.DeepEqual(got, original) {
		t.Fatalf("maxima were clamped or erased: %+v", got)
	}
	for index, change := range []func(*RecordingGroupChange){
		func(v *RecordingGroupChange) { n := 21600001; v.Options.PrePaddingMs = &n },
		func(v *RecordingGroupChange) { n := 21600001; v.Options.PostPaddingMs = &n },
		func(v *RecordingGroupChange) { v.Options.Priority = 1001 },
		func(v *RecordingGroupChange) { v.Options.Priority = -1001 },
		func(v *RecordingGroupChange) { v.Options.Keep = &KeepPolicy{Mode: "keep-count", KeepCount: 100001} },
		func(v *RecordingGroupChange) { v.Options.Keep = &KeepPolicy{Mode: "keep-days", KeepDays: 3651} },
	} {
		bad := c
		bad.ExpectedRevision = updated.Revision
		bad.OperationID = fmt.Sprintf("over-bound-%d", index)
		change(&bad)
		if _, err = s.SaveRecordingGroup(ctx, allow, rule.ID, bad); err == nil {
			t.Fatalf("over-bound option %d accepted", index)
		}
		if got := stored(); !reflect.DeepEqual(got, original) {
			t.Fatalf("refused option %d changed durable config", index)
		}
	}
	rule = updated
	// Title identity can use the entire DVR domain and stays attached to the
	// original channel when the limited administrative projection is saved.
	longTitle := strings.Repeat("T", 512)
	if _, err = db.Exec(`UPDATE live_programmes SET title=? WHERE generation_id=? AND id=?`, longTitle, gen, prog); err != nil {
		t.Fatal(err)
	}
	title := RecordingGroupChange{Owner: c.Owner, Kind: "title", Name: "Long title", Match: longTitle, SourceID: src, OperationID: "long-title", Anchor: c.Anchor}
	titleRule, err := s.SaveRecordingGroup(ctx, allow, "", title)
	if err != nil {
		t.Fatal(err)
	}
	if titleRule.Kind != "title" || titleRule.Match != longTitle {
		t.Fatal(titleRule)
	}
	if err = db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, titleRule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var titleOriginal dvr.RuleConfig
	if err = json.Unmarshal([]byte(raw), &titleOriginal); err != nil {
		t.Fatal(err)
	}
	titleOriginal.AllowedChannels, titleOriginal.BlockedChannels = []string{ch}, []string{strings.Repeat("f", 64)}
	titleOriginal.Keywords, titleOriginal.BlockedKeywords = []string{"title"}, []string{"rerun"}
	titleOriginal.Options.RetentionDays, titleOriginal.Options.EpisodeLimit = 7, 10
	directTitle, err := s.DVR.SaveRule(ctx, groupAuthority(allow), c.Owner, dvr.RuleInput{ID: titleRule.ID, Mutation: dvr.Mutation{ExpectedRevision: titleRule.Revision, RequestID: strings.Repeat("f", 48)}, Config: titleOriginal})
	if err != nil {
		t.Fatal(err)
	}
	titleProjection := groupFromRule(directTitle, c.Owner)
	title.ExpectedRevision, title.OperationID, title.Options, title.Name = directTitle.Revision, "long-title-rename", titleProjection.Options, "Renamed title"
	titleRule, err = s.SaveRecordingGroup(ctx, allow, titleRule.ID, title)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT config_json FROM dvr_rules WHERE id=?`, titleRule.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var titleConfig dvr.RuleConfig
	if err = json.Unmarshal([]byte(raw), &titleConfig); err != nil {
		t.Fatal(err)
	}
	titleOriginal.Name = "Renamed title"
	if !reflect.DeepEqual(titleConfig, titleOriginal) {
		t.Fatalf("title projection changed durable config: %+v", titleConfig)
	}
	longSeries := strings.Repeat("S", 512)
	if _, err = db.Exec(`UPDATE live_programme_metadata SET series_id=? WHERE generation_id=? AND id=?`, longSeries, gen, prog); err != nil {
		t.Fatal(err)
	}
	series := RecordingGroupChange{Owner: c.Owner, Kind: "series", Name: "Long series", Match: longSeries, SourceID: src, OperationID: "long-series", Anchor: c.Anchor}
	seriesRule, err := s.SaveRecordingGroup(ctx, allow, "", series)
	if err != nil {
		t.Fatal(err)
	}
	series.ExpectedRevision, series.OperationID, series.Options, series.Name = seriesRule.Revision, "long-series-rename", seriesRule.Options, "Renamed series"
	seriesRule, err = s.SaveRecordingGroup(ctx, allow, seriesRule.ID, series)
	if err != nil || seriesRule.Match != longSeries {
		t.Fatal(seriesRule, err)
	}
	if err = s.DeleteRecordingGroup(ctx, allow, rule.ID, rule.Revision, "remove-rule"); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM dvr_rules WHERE id=? AND deleted=1`, rule.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestAdministrativeGroupAcceptsDVRMatchDomain(t *testing.T) {
	for _, kind := range []string{"series", "title"} {
		c := RecordingGroupChange{Kind: kind, Name: "Name", Match: strings.Repeat("a", 512)}
		if err := validateGroup(&c); err != nil {
			t.Fatalf("%s legal match rejected: %v", kind, err)
		}
		c.Match += "a"
		if err := validateGroup(&c); err == nil {
			t.Fatalf("%s overlong match accepted", kind)
		}
	}
}
