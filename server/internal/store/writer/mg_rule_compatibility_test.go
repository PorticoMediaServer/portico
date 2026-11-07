package writer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMGRuleNativeAcceptedConfigurationRange(t *testing.T) {
	for _, count := range []int{1100, 4000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			w, r := mgRuleFixture(t)
			// Frozen public RuleConfig wire values; the public validator accepted
			// both fixtures in the compatibility audit. Keep legacy package
			// init registrations out of the native writer/plan test binary.
			allowed, blocked := []string{}, []string{}
			keywords, blockedKeywords := []string{}, []string{}
			cfg := map[string]any{"name": "Large lineup", "sourceId": r.after.source, "seriesId": r.after.series, "enabled": true, "episodes": "all", "matchKind": "", "title": "", "channelId": "", "options": map[string]int{"beforeMs": 0, "afterMs": 0, "priority": 0, "retentionDays": 0, "episodeLimit": 0}}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("%064x", i+1)
				if i < 2000 {
					allowed = append(allowed, id)
				} else {
					blocked = append(blocked, id)
				}
			}
			if count == 4000 {
				cfg["name"] = strings.Repeat("<", 160)
				cfg["seriesId"] = strings.Repeat("&", 512)
				r.after.series = cfg["seriesId"].(string)
				for i := 0; i < 20; i++ {
					keywords = append(keywords, strings.Repeat("\x01", 128))
					blockedKeywords = append(blockedKeywords, strings.Repeat("\x02", 128))
				}
			}
			cfg["allowedChannels"], cfg["blockedChannels"] = allowed, blocked
			cfg["keywords"], cfg["blockedKeywords"] = keywords, blockedKeywords
			raw, err := json.Marshal(cfg)
			if err != nil || len(raw) <= 65536 || len(raw) > mgRuleMaxConfigBytes {
				t.Fatal("accepted canonical byte range", len(raw), err)
			}
			r.after.config = string(raw)
			turn := mgRuleTestTurn(t, w)
			out, err := saveMGRule(t.Context(), turn, r)
			if err != nil || out != r.after {
				t.Fatal("large configuration save", len(raw), out.revision, err)
			}
			withoutConfig := out
			withoutConfig.config = ""
			if out.variableBytes()-withoutConfig.variableBytes() != int64(len(raw)) {
				t.Fatal("configuration bytes were not accounted exactly")
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			r.before, r.after = out, out
			r.after.revision++
			r.after.updated++
			r.after.enabled, r.after.deleted = 0, 1
			turn = mgRuleTestTurn(t, w)
			turn.transaction.descriptor.ID = MGDeleteRule
			out, err = deleteMGRule(t.Context(), turn, r)
			if err != nil || out != r.after || out.config != string(raw) {
				t.Fatal("large configuration delete changed bytes", len(raw), out.revision, err)
			}
		})
	}
}
