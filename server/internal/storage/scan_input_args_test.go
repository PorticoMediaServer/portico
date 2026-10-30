package storage

import "testing"

func TestScanInputReplacementKeepsProtocolAndFormatBounds(t *testing.T) {
	args := []string{"probe", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska", "retained-input"}
	got, err := replaceScanInput(args, "retained-input", "C:\\media folder\\file.mkv")
	if err != nil || got[2] != "file,pipe" || got[4] != "mov,matroska" || args[5] != "retained-input" {
		t.Fatalf("argument policy changed: %v %v", got, err)
	}
	for _, invalid := range [][]string{{"probe"}, {"probe", "retained-input", "retained-input"}} {
		if _, err := replaceScanInput(invalid, "retained-input", "path"); err == nil {
			t.Fatal("ambiguous input accepted")
		}
	}
}

func TestDiscScanInputReplacementPreservesOnlyClosedBlurayPrefix(t *testing.T) {
	args := []string{"probe", "-f", "mpegts", "bluray:retained-input"}
	got, err := replaceScanInput(args, "retained-input", `C:\media\disc.iso`)
	if err != nil || got[3] != `bluray:C:\media\disc.iso` || args[3] != "bluray:retained-input" {
		t.Fatal(got, err)
	}
	for _, args := range [][]string{{"probe", "http:retained-input"}, {"probe", "bluray:retained-input", "retained-input"}, {"probe", "bluray:bluray:retained-input"}} {
		if _, err := replaceScanInput(args, "retained-input", `C:\media\disc.iso`); err == nil {
			t.Fatal("unbounded protocol/ambiguous replacement", args)
		}
	}
}
