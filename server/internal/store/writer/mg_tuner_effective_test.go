package writer

import (
	"errors"
	"testing"
)

func TestMGTunerNativePublishedEffectiveCapacity(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "discovery-disabled", true: "configured-without-discovery"}[known], func(t *testing.T) {
			_, turn, r := mgTunerFixture(t, MGAcquireAllocation)
			effective, physical := int64(0), int64(1)
			if known {
				effective = 1
				physical = 0
			}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET tuner_count=? WHERE id='source'`, effective); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_source_identities SET settings_physical_count=?,settings_owner_limit=0 WHERE id='source'`, physical); err != nil {
				t.Fatal(err)
			}
			held := mgTunerAllocation{"held", "source", "other-channel", "held-resource", "live", "local", "account", "profile", "held-token", 1, "active", 1, 0, 0}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, held.values()...); err != nil {
				t.Fatal(err)
			}
			out, err := acquireMGTuner(t.Context(), turn, r)
			if known {
				if !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
					t.Fatal("configured effective capacity ignored", out, err)
				}
			} else if err != nil || out != r.allocation {
				t.Fatal("discovery metadata invented a disabled hard cap", out, err)
			}
		})
	}
}
func TestMGTunerNativeGenerationSourceCoupling(t *testing.T) {
	_, turn, r := mgTunerFixture(t, MGAcquireAllocation)
	for _, query := range []string{
		`INSERT INTO live_source_identities(id) VALUES('foreign')`,
		`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('foreign-guide','foreign','Foreign',1,1,0,'now')`,
		`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('foreign-guide','channel','provider','Foreign','1','',0,x'01','','')`,
		`UPDATE live_sources SET active_generation='foreign-guide' WHERE id='source'`,
	} {
		if _, err := turn.transaction.conn.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	// The stock fixture predates G's native composite head FK. The point body
	// independently binds the generation's actual source before allocation.
	r.guide = "foreign-guide"
	if out, err := acquireMGTuner(t.Context(), turn, r); !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
		t.Fatal("foreign generation was accepted", out, err)
	}
}
