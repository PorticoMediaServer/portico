package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
)

type hardwareEngine struct {
	*fakeEngine
	tests int
}

func (e *hardwareEngine) HardwareState(context.Context) mediaengine.StreamingHardwareState {
	device := mediaengine.StreamingHardwareDevice{ID: "gpu-one", Name: "Observed GPU", ToneMappingWithMasteringData: true}
	return mediaengine.StreamingHardwareState{Checking: e.tests != 0, ToneMappingWithMasteringData: true, Devices: []mediaengine.StreamingHardwareDevice{device}}
}
func (e *hardwareEngine) TestHardware() { e.tests++ }

func TestOwnerReadsAndTestsHardwareWithoutARestart(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	initial := get[mediaengine.StreamingHardwareState](t, s, owner, "/v1/admin/hardware")
	if !initial.Checking || initial.Devices == nil || initial.Formats == nil {
		t.Fatalf("before the engine answers: %+v", initial)
	}
	engine := &hardwareEngine{fakeEngine: &fakeEngine{}}
	s.playback.SetEngine(engine, engine)
	var state mediaengine.StreamingHardwareState
	if w := call(t, s, "GET", "/v1/admin/hardware", owner, nil, &state); w.Code != 200 || len(state.Devices) != 1 || state.Devices[0].ID != "gpu-one" || engine.tests != 0 {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
	if !state.ToneMappingWithMasteringData || state.ToneMappingWithoutMasteringData || !state.Devices[0].ToneMappingWithMasteringData {
		t.Fatal("the two tone-mapping cases were collapsed", state)
	}
	if w := call(t, s, "POST", "/v1/admin/hardware/test", owner, nil, &state); w.Code != 200 || !state.Checking || engine.tests != 1 {
		t.Fatalf("test: %d %s", w.Code, w.Body)
	}
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	for _, route := range []struct{ method, path string }{{"GET", "/v1/admin/hardware"}, {"POST", "/v1/admin/hardware/test"}} {
		if code := status(t, s, route.method, route.path, admin, nil); code != 403 {
			t.Fatalf("administrator reached hardware: %d", code)
		}
	}
}
