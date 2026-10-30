package assets

import "testing"

func TestPacketPeakIncludesBurstsAcrossClockBoundary(t *testing.T) {
	if got := packetPeak([]byte("0.1,100\n0.9,500\n1.1,700\n1.95,100\n")); got != 9600 {
		t.Fatal(got)
	}
	for _, v := range []string{"0,100\nN/A,200\n", "1,100\n0,200\n", "0,100\n1,-1\n", "0,100\n", "NaN,100\n1,200\n"} {
		if packetPeak([]byte(v)) != 0 {
			t.Fatal(v)
		}
	}
}
