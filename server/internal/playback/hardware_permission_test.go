package playback

import "testing"

func TestConversionJobsUseEncoderBackendPermissions(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan *DeliveryPlan
		want bool
	}{
		{"legacy", nil, false},
		{"empty backend", &DeliveryPlan{}, false},
		{"software with GPU tone mapper", &DeliveryPlan{HardwareBackend: "software", ToneMap: true}, false},
		{"hardware encoder", &DeliveryPlan{HardwareBackend: "nvenc"}, true},
		{"copy with hardware plan", &DeliveryPlan{HardwareBackend: "vaapi", VideoAction: "copy"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := conversionJobHardware(tc.plan); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
}
