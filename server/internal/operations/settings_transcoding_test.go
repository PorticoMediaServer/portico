package operations

import (
	"context"
	"errors"
	"testing"
)

func TestTranscodingSettingsDefaultsRoundTripAndReject(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx := context.Background()
	before, e := s.Settings(ctx, auth)
	if e != nil {
		t.Fatal(e)
	}
	// A database written before these fields existed must still read as the
	// registry's defaults rather than as empty strings and zeroes.
	defaults := DefaultSettings()
	if before.Effective.HardwareBackend != defaults.HardwareBackend || before.Effective.X264Preset != defaults.X264Preset ||
		before.Effective.PlanningPolicy != defaults.PlanningPolicy || !before.Effective.DirectStreamRemux ||
		before.Effective.TranscodeLocation != "memory" || before.Effective.MemoryCap != "quarter" ||
		before.Effective.ThrottleBufferMs != 60000 || before.Effective.PlayedRetentionMs != 180000 {
		t.Fatal("registry defaults not published", before.Effective)
	}

	v := before.Requested
	v.HardwareBackend = "vaapi"
	v.HardwareDevice = "/dev/dri/renderD128"
	v.HDRToneMapping = true
	v.HDRToneMappingAlgorithm = "mobius"
	v.X264Preset = "slow"
	v.DirectStreamRemux = false
	v.PlanningPolicy = "minimize_server_work"
	v.TranscodeLocation = "disk"
	v.MemoryCap = "2gb"
	v.TemporaryDirectory = t.TempDir()
	v.MaxConcurrentSessions = 4
	after, e := s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "transcoding-one", Values: v})
	if e != nil {
		t.Fatal(e)
	}
	if after.Effective.HardwareBackend != "vaapi" || after.Effective.HardwareDevice != "/dev/dri/renderD128" ||
		!after.Effective.HDRToneMapping || after.Effective.HDRToneMappingAlgorithm != "mobius" ||
		after.Effective.X264Preset != "slow" || after.Effective.DirectStreamRemux ||
		after.Effective.TranscodeLocation != "disk" || after.Effective.MemoryCap != "2gb" ||
		after.Effective.PlanningPolicy != "minimize_server_work" || after.Effective.MaxConcurrentSessions != 4 {
		t.Fatal("saved transcoding values not readable", after.Effective)
	}

	for field, mutate := range map[string]func(*Settings){
		"values.transcodeLocation":       func(v *Settings) { v.TranscodeLocation = "anywhere" },
		"values.memoryCap":               func(v *Settings) { v.MemoryCap = "unlimited" },
		"values.hardwareBackend":         func(v *Settings) { v.HardwareBackend = "magic" },
		"values.hdrToneMappingAlgorithm": func(v *Settings) { v.HDRToneMappingAlgorithm = "filmic" },
		"values.x264Preset":              func(v *Settings) { v.X264Preset = "placebo" },
		"values.planningPolicy":          func(v *Settings) { v.PlanningPolicy = "fastest" },
		"values.temporaryDirectory":      func(v *Settings) { v.TemporaryDirectory = "relative/path" },
		"values.hardwareDevice":          func(v *Settings) { v.HardwareDevice = "renderD128\nrm -rf" },
	} {
		invalid := after.Requested
		mutate(&invalid)
		_, e := s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: after.Revision, IdempotencyKey: "reject-" + field, Values: invalid})
		var fields *ValidationError
		if !errors.As(e, &fields) || len(fields.Fields) != 1 || fields.Fields[0] != field {
			t.Fatalf("%s must be rejected by name, got %v", field, e)
		}
	}
	// An unclean absolute path is refused too, so no traversal reaches the disk.
	invalid := after.Requested
	invalid.TemporaryDirectory = "/var/portico/../etc"
	if _, e := s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: after.Revision, IdempotencyKey: "reject-unclean", Values: invalid}); !errors.Is(e, ErrInvalid) {
		t.Fatal("an unclean path must be refused", e)
	}
}

func TestTranscodingNumericSettingsClampInsteadOfFailing(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx := context.Background()
	before, e := s.Settings(ctx, auth)
	if e != nil {
		t.Fatal(e)
	}
	v := before.Requested
	v.ThrottleBufferMs = 2000
	v.PlayedRetentionMs = 99999999
	v.MaxConcurrentSessions = -4
	v.MaxHardwareSessions = 50000
	v.MaxSoftwareSessions = 0
	v.MaxBackgroundSessions = 3
	after, e := s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "clamp-one", Values: v})
	if e != nil {
		t.Fatal(e)
	}
	if after.Effective.ThrottleBufferMs != 10000 || after.Effective.PlayedRetentionMs != 3600000 ||
		after.Effective.MaxConcurrentSessions != 0 || after.Effective.MaxHardwareSessions != 1000 ||
		after.Effective.MaxSoftwareSessions != 0 || after.Effective.MaxBackgroundSessions != 3 {
		t.Fatal("numeric transcoding controls must clamp to the published bounds", after.Effective)
	}
}

func TestRegistryPublishesEveryTranscodingFieldWithItsBounds(t *testing.T) {
	published := map[string]Field{}
	for _, f := range Registry() {
		published[f.ID] = f
	}
	for _, id := range []string{"hardwareBackend", "hardwareDevice", "hdrToneMapping", "hdrToneMappingAlgorithm",
		"x264Preset", "directStreamRemux", "planningPolicy", "throttleBufferMs", "playedRetentionMs",
		"transcodeLocation", "memoryCap", "temporaryDirectory", "maxConcurrentSessions", "maxHardwareSessions", "maxSoftwareSessions", "maxBackgroundSessions"} {
		f, ok := published[id]
		if !ok {
			t.Fatal("registry must publish", id)
		}
		if f.Group != "transcoding" || f.Permission != "owner" || f.Description == "" || f.Apply == "" {
			t.Fatal("registry entry is incomplete", f)
		}
		if f.Type == "enumeration" && len(f.Options) == 0 {
			t.Fatal("an enumeration must publish its accepted values", id)
		}
		if f.Type == "integer" && (f.Min == nil || f.Max == nil) {
			t.Fatal("a numeric field must publish its bounds", id)
		}
	}
	if published["temporaryDirectory"].Apply != "restart" {
		t.Fatal("the working directory only takes effect on restart and must say so")
	}
}

func TestConversionFolderReportsPendingRestart(t *testing.T) {
	s, p, auth := consoleFixture(t)
	applied := ""
	s.AppliedTemporaryDirectory = &applied
	ctx := context.Background()
	before, err := s.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	values := before.Requested
	values.TemporaryDirectory = t.TempDir()
	after, err := s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "conversion-folder", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if after.Requested.TemporaryDirectory != values.TemporaryDirectory || after.Effective.TemporaryDirectory != "" || len(after.RestartFields) != 1 || after.RestartFields[0] != "temporaryDirectory" {
		t.Fatal(after)
	}
}

func TestConversionFolderRejectsUnsupportedRuntimePath(t *testing.T) {
	s, p, auth := consoleFixture(t)
	s.TranscodeDirectoryAllowed = func(path string) bool { return path == "" || path == "/transcode" }
	ctx := context.Background()
	before, err := s.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	values := before.Requested
	values.TemporaryDirectory = "/config/Keys"
	_, err = s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "reject-conversion-root", Values: values})
	var invalid *ValidationError
	if !errors.As(err, &invalid) || len(invalid.Fields) != 1 || invalid.Fields[0] != "values.temporaryDirectory" {
		t.Fatal("unsupported runtime folder was not rejected by name", err)
	}
	values.TemporaryDirectory = "/transcode"
	after, err := s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "supported-conversion-root", Values: values})
	if err != nil || after.Requested.TemporaryDirectory != "/transcode" {
		t.Fatal("supported disk folder could not be configured", after, err)
	}
}
