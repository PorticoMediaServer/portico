package qual

import "testing"

func TestHostKernelDeviceOnlyIOStat(t *testing.T) {
	h := validHost(t)
	// Actual Linux7 root output shape, with ordinary counters and idle devices.
	raw := "259:0 rbytes=876452864 wbytes=6226408960 rios=22740 wios=29710 dbytes=418250752 dios=877\n7:7 \n7:6 \n"
	setCG(&h, 2, "io.stat", raw)
	if p := ValidateHost(h, "legacy8"); len(p) > 0 {
		t.Fatal(p)
	}
	if h.Cgroups[2].Files[h.Cgroups[2].Path+"/io.stat"] != raw {
		t.Fatal("raw counters rewritten")
	}
	for _, bad := range []string{
		"7:7\n7:7\n", "bad-device\n", "7:7 rbytes=0\n",
		"7:7 rbytes=0 wbytes=0 rios=0\n",
		"7:7 rbytes=0 wbytes=0 rios=0 wios=0 wios=0\n",
	} {
		if err := ioStat(bad); err == nil {
			t.Fatalf("accepted malformed/populated-incomplete IO stat %q", bad)
		}
	}
}

func TestHostDeviceOnlyStatDoesNotExcuseMissingEvidenceOrLimits(t *testing.T) {
	h := validHost(t)
	setCG(&h, 2, "io.stat", "7:7\n")
	missingCG(&h, 1, "io.stat", "permission")
	rejectHost(t, h)
	h = validHost(t)
	setCG(&h, 2, "io.stat", "7:7\n")
	setCG(&h, 1, "io.max", "7:7 rbps=max wbps=max riops=max")
	rejectHost(t, h)
	h = validHost(t)
	setCG(&h, 2, "io.stat", "7:7\n")
	setCG(&h, 1, "io.max", "7:7 rbps=1 wbps=max riops=max wiops=max")
	rejectHost(t, h)
}
