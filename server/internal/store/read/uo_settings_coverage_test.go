package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"testing"
	"time"
)

func TestUOSettingsConnectivityAndNullableCapSnapshot(t *testing.T) {
	pool, exec := uoReadTestFixture(t, UOSettingsDefinitions())
	exec(`INSERT INTO console_settings_revision VALUES(1,4)`)
	exec(`INSERT INTO server_settings(singleton,configuration_pending_revision,"advertiseUrl","connectionLimit") VALUES(1,4,'https://example.com',12)`)
	exec(`INSERT INTO playback_owner_policy(singleton,revision,transcoding_enabled,server_cap) VALUES(1,7,1,9)`)
	exec(`INSERT INTO server_configuration_pending VALUES('serverCap',4)`)
	_, e := pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
		out, e := uoReadSettings(t.Context(), s, []UOSettingKey{UOSettingAdvertiseUrl, UOSettingConnectionLimit, UOSettingLanDiscoveryEnabled, UOSettingPerAccountCap, UOSettingServerCap})
		if e != nil {
			return e
		}
		if out.PolicyRevision != 7 || out.Revision != 4 || out.Points[0].Value.Text != "https://example.com" || out.Points[1].Value.Number != 12 || !out.Points[2].Value.Bool {
			t.Fatal(out)
		}
		unlimited, e := uoReadCap(out.Points[3])
		if e != nil || unlimited.Limited {
			t.Fatal(unlimited, e)
		}
		limited, e := uoReadCap(out.Points[4])
		if e != nil || !limited.Limited || limited.Limit != 9 || out.Points[4].PendingRevision != 4 {
			t.Fatal(limited, e, out)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
