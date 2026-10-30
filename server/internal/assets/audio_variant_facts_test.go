package assets

import "testing"

func TestDTSVariantRequiresActualNamedProfile(t *testing.T) {
	for _, c := range []struct{ profile, objects, want string }{
		{"DTS-HD MA", "", "dts_hd_ma"},
		{"DTS-HD Master Audio", "", "dts_hd_ma"},
		{"DTS-HD HRA", "", "dts_hd_hra"},
		{"DTS-HD High Resolution Audio", "", "dts_hd_hra"},
		{"DTS Express", "", "dts_express"},
		{"DTS", "", "dts_core"},
		{"DTS-ES Matrix", "", "dts_core"},
		{"DTS 96/24", "", "dts_core"},
		{"", "", ""},
		{"unobserved main profile", "", ""},
		{"DTS-HD MA", "dtsx", "dtsx"},
	} {
		if got := AudioVariant("dts", c.profile, c.objects); got != c.want {
			t.Fatalf("%+v got %s", c, got)
		}
	}
	if got := AudioVariant("eac3", "", ""); got != "eac3" {
		t.Fatal("codec alone became object audio", got)
	}
	if got := AudioVariant("eac3", "E-AC-3 JOC", ""); got != "eac3_joc" {
		t.Fatal(got)
	}
}
