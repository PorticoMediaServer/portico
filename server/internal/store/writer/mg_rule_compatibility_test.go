package writer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
)

func TestMGRuleNativeAcceptedConfigurationRange(t *testing.T) {
	for _, count := range []int{1100, 4000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			w, r := mgRuleFixture(t)
			cfg := dvr.RuleConfig{Name: "Large lineup", SourceID: r.after.source, SeriesID: r.after.series, Enabled: true, Episodes: "all", AllowedChannels: []string{}, BlockedChannels: []string{}, Keywords: []string{}}
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("%064x", i+1)
				if i < 2000 {
					cfg.AllowedChannels = append(cfg.AllowedChannels, id)
				} else {
					cfg.BlockedChannels = append(cfg.BlockedChannels, id)
				}
			}
			if count == 4000 {
				cfg.Name = strings.Repeat("<", 160)
				cfg.SeriesID = strings.Repeat("&", 512)
				r.after.series = cfg.SeriesID
				for i := 0; i < 20; i++ {
					cfg.Keywords = append(cfg.Keywords, strings.Repeat("\x01", 128))
					cfg.BlockedKeywords = append(cfg.BlockedKeywords, strings.Repeat("\x02", 128))
				}
			}
			if !cfg.Valid() {
				t.Fatal("public RuleConfig rejects compatibility fixture")
			}
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
