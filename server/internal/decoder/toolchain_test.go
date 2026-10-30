package decoder

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"testing"
)

func TestToolchainFactsKeepsJoinQualificationPrivate(t *testing.T) {
	raw, err := json.Marshal(ToolchainFacts{DolbyVisionDualLayerJoin: true, DolbyVisionProfile7To81: true})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["dolbyVisionDualLayerJoin"]; exists {
		t.Fatal("private join qualification escaped through the capacity toolchain response")
	}
	if wire["dolbyVisionProfile7To81"] != true {
		t.Fatal("existing public profile conversion fact changed")
	}
}

func TestToolchainListFormats(t *testing.T) {
	for _, version := range []string{"4.4", "6.1", "8.1"} {
		t.Run(version, func(t *testing.T) {
			enc := parseToolList("Encoders:\n V..... = Video\n ------\n V....D libx264 H.264\n A..... aac AAC\n", "encoders")
			filters := parseToolList("Filters:\n ... = support\n ---\n .SC zscale V->V Convert\n T.. tonemap V->V Tone map\n", "filters")
			if !enc["libx264"] || !enc["aac"] || enc["="] || !filters["zscale"] || !filters["tonemap"] {
				t.Fatal(enc, filters)
			}
		})
	}
}
func TestInstalledToolchain(t *testing.T) {
	path := decodertest.QualifiedFFmpeg(t)
	f, e := ProbeToolchain(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	if !f.Encoders["aac"] || !f.Muxers["mp4"] || !f.Decoders["h264"] {
		t.Fatalf("%+v", f)
	}
	if missing := f.MissingRequired(); len(missing) > 0 {
		decodertest.Unavailable(t, fmt.Sprintf("toolchain missing requirements: %v", missing))
	}
	t.Log(f.Version, f.MissingRequired())
}
func TestHardwareRuntimeCircuit(t *testing.T) {
	clearHardwareCircuit(BackendNVENC)
	defer clearHardwareCircuit(BackendNVENC)
	for i := 0; i < 3; i++ {
		RecordHardwareFailure(BackendNVENC)
	}
	if !hardwareCircuitOpen(BackendNVENC) {
		t.Fatal("did not open")
	}
	clearHardwareCircuit(BackendNVENC)
	if hardwareCircuitOpen(BackendNVENC) {
		t.Fatal("did not reset")
	}
}

func TestSoftwareToneMapProbeUsesSoftwarePermissions(t *testing.T) {
	for _, device := range []string{"vulkan=tone", "opencl=tone"} {
		job := toneMapProbeJob("/usr/bin/ffmpeg", device, "hwupload,hwdownload")
		if job.Hardware {
			t.Fatalf("%s probe gets permissions denied to software conversion", device)
		}
		if job.Loopback != "" || len(job.ReadPaths) > 0 || len(job.WriteDirs) > 0 {
			t.Fatal("probe must not gain source, network or output access", job)
		}
	}
}
