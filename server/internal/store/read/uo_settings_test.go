package read

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestUOSettingsSnapshotProjectionAndSecurityExclusion(t *testing.T) {
	pool, exec := uoReadTestFixture(t, UOSettingsDefinitions())
	exec(`INSERT INTO console_settings_revision VALUES(1,3)`)
	exec(`INSERT INTO server_settings(singleton,configuration_pending_revision,"hardwareDevice","remoteSecurity") VALUES(1,3,'海','enhanced')`)
	exec(`INSERT INTO server_setting_sources VALUES('hardwareDevice','file')`)
	exec(`INSERT INTO server_configuration_pending VALUES('hardwareDevice',2)`)
	exec(`INSERT INTO server_setting_lists VALUES('accessUrls',0,'https://one.example')`)
	var out uoSettingsDocument
	_, e := pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
		var e error
		out, e = uoReadSettings(t.Context(), s, []UOSettingKey{UOSettingHardwareDevice, UOSettingAccessUrls, UOSettingAnalysisStorageGiB})
		return e
	})
	if e != nil || out.Revision != 3 || out.ProjectionGeneration != 3 || len(out.Points) != 3 || out.Points[0].Value.Text != "海" || out.Points[0].Source != "file" || out.Points[0].PendingRevision != 2 || out.Points[1].Value.List[0] != "https://one.example" || out.Points[2].Value.Number != 32 {
		t.Fatal(out, e)
	}
	_, e = pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
		v, e := uoReadSettings(t.Context(), s, []UOSettingKey{"remoteSecurity"})
		if !reflect.DeepEqual(v, uoSettingsDocument{}) {
			t.Fatal(v)
		}
		return e
	})
	if !errors.Is(e, ErrUnqualified) {
		t.Fatal(e)
	}
}
func TestUOSettingsSnapshotBoundedListsAndPendingFence(t *testing.T) {
	pool, exec := uoReadTestFixture(t, UOSettingsDefinitions())
	exec(`INSERT INTO console_settings_revision VALUES(1,2)`)
	exec(`INSERT INTO server_settings(singleton,configuration_pending_revision) VALUES(1,2)`)
	for i := 0; i < 65; i++ {
		exec(`INSERT INTO server_setting_lists VALUES('accessUrls',?,?)`, int64(i), "https://one.example")
	}
	_, e := pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
		v, e := uoReadSettings(t.Context(), s, []UOSettingKey{UOSettingAccessUrls})
		if !reflect.DeepEqual(v, uoSettingsDocument{}) {
			t.Fatal(v)
		}
		return e
	})
	if !errors.Is(e, ErrUnqualified) {
		t.Fatal(e)
	}
	exec(`INSERT INTO server_configuration_pending VALUES('hardwareDevice',3)`)
	_, e = pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
		v, e := uoReadSettings(t.Context(), s, []UOSettingKey{UOSettingHardwareDevice})
		if !reflect.DeepEqual(v, uoSettingsDocument{}) {
			t.Fatal(v)
		}
		return e
	})
	if !errors.Is(e, ErrUnqualified) {
		t.Fatal(e)
	}
}
func TestUOSettingsReadRegistryIsFiniteAndInert(t *testing.T) {
	defs := UOSettingsDefinitions()
	if len(defs) > 128 {
		t.Fatal("exceeds GV registry bound", len(defs))
	}
	for _, d := range defs {
		if d.Kind != stmt.Read {
			t.Fatal(d.ID)
		}
	}
	if _, e := stmt.Describe(defs); e != nil {
		t.Fatal(e)
	}
	if _, e := stmt.New(defs); !errors.Is(e, stmt.ErrUnqualified) {
		t.Fatal(e)
	}
}

func TestUOSettingsSnapshotTypedRetentionAndAbsentName(t *testing.T) {
	pool, exec := uoReadTestFixture(t, UOSettingsDefinitions())
	exec(`INSERT INTO console_settings_revision VALUES(1,1)`)
	exec(`INSERT INTO server_settings(singleton) VALUES(1)`)
	exec(`INSERT INTO server_log_retention VALUES('server',7)`)
	exec(`INSERT INTO server_log_retention VALUES('client',30)`)
	var out uoSettingsDocument
	_, e := pool.ReadForecasted(t.Context(), uoReadTestRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 8192, MaxQueries: 256}, func(s *governor.ReadScope) error {
		var e error
		out, e = uoReadSettings(t.Context(), s, []UOSettingKey{UOSettingLogRetention, UOSettingName})
		return e
	})
	if e != nil || len(out.Points) != 2 || out.Points[1].Value.Text != "Portico" || !reflect.DeepEqual(out.Points[0].Value.Retention, []UOLogRetention{{UOLogClient, 30}, {UOLogServer, 7}}) {
		t.Fatal(out, e)
	}
}
