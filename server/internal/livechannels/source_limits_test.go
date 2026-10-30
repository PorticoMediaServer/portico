package livechannels

import "testing"

func TestSourceImportLimitsOnlyLowerTheServersOwn(t *testing.T) {
	server := ImportLimits{Bytes: 100 << 20, Channels: 5000, Programmes: 200000}
	if got := SourceImportLimits(server, nil); got != server {
		t.Fatalf("no override: %+v", got)
	}
	got := SourceImportLimits(server, &ImportLimits{Channels: 300, Programmes: 900000})
	if got.Channels != 300 || got.Programmes != 200000 || got.Bytes != 100<<20 {
		t.Fatalf("a source may lower, never raise: %+v", got)
	}
}
