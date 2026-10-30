package playback

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"strings"
	"testing"
)

func TestConversionExecutionCountsAndViewerOmission(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	defer db.Close()
	session, err := s.Create(p, item, "original", "execution")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := &HLS{db: db}
	plan := &DeliveryPlan{VideoAction: "convert", SourceVideoCodec: "h264", HardwareBackend: "vaapi"}
	stages := []decoder.HardwareStage{{Operation: "decode", Execution: "hardware"}, {Operation: "encode", Execution: "hardware"}}
	args := []string{"-init_hw_device", "vaapi=hw:/dev/dri/renderD128", "-hwaccel", "vaapi", "-i", "https://user:secret.invalid/source", "-c:v", "h264_vaapi"}
	h.recordStages(ctx, session.ID, plan, stages)
	h.recordConversionStart(ctx, session.ID, plan, args, stages, 1, false)
	// Rebuilding/retrying a graph must not erase an earlier launch or fallback.
	stages[0].Execution = "software"
	stages[1].Execution = "software"
	h.recordStages(ctx, session.ID, plan, stages)
	h.recordConversionStart(ctx, session.ID, plan, []string{"-c:v", "libx264"}, stages, 2, true)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	admin, err := AdminSessionDeliveryTx(ctx, tx, DefaultDeliverySettings(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution := admin.Hardware.Execution
	if execution == nil || execution.LaunchCount != 2 || execution.SoftwareLaunchCount != 1 || execution.UnknownLaunchCount != 0 || execution.FallbackCount != 1 || execution.LastLaunch.Sequence != 2 || execution.LastLaunch.Generation != 2 {
		t.Fatalf("lost launch history: %+v", execution)
	}
	ordinary, err := SessionDeliveryTx(ctx, tx, DefaultDeliverySettings(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ordinary)
	if ordinary.Hardware.Execution != nil || strings.Contains(string(raw), `"execution":{`) || strings.Contains(string(raw), "renderD128") || strings.Contains(string(raw), "libx264") {
		t.Fatal("ordinary viewer received host execution diagnostics")
	}
}
func TestObservedConversionLaunchRejectsUnknownAndLeaksNoArguments(t *testing.T) {
	plan := &DeliveryPlan{SourceVideoCodec: "h264"}
	stages := []decoder.HardwareStage{{Operation: "decode", Execution: "hardware"}, {Operation: "encode", Execution: "hardware"}}
	launch := observedConversionLaunch(plan, []string{"-hwaccel", "auto", "-c:v", "unknown_vendor", "-i", "https://secret.invalid"}, stages, 1)
	if launch.Encoder.Execution != "unknown" || launch.Decoder.Execution != "unknown" {
		t.Fatal(launch)
	}
	raw, _ := json.Marshal(launch)
	if strings.Contains(string(raw), "secret.invalid") {
		t.Fatal("arguments leaked")
	}
	launch = observedConversionLaunch(plan, []string{"-init_hw_device", "vaapi=hw:/dev/dri/renderD128", "-hwaccel", "vaapi", "-c:v", "h264_vaapi"}, stages, 1)
	if launch.Encoder.Execution != "hardware" || launch.Decoder.Execution != "hardware" || launch.RenderNode != "/dev/dri/renderD128" {
		t.Fatal(launch)
	}
}
