package dlna

import "testing"

func TestMusicDIDLMeasuredFactsAndMIMER29(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	song := f.items["song1"]
	f.c.Exec(`INSERT INTO music_technical_facts(asset_id,size,modified_ns,codec,sample_rate,bit_depth,channels,lossless) SELECT token,size,modified_ns,'flac',192000,24,6,1 FROM catalog_assets WHERE token=?`, song.Token)
	for _, tc := range []struct{ container, mime string }{{"ape", "audio/x-ape"}, {"wv", "audio/x-wavpack"}, {"dsf", "audio/x-dsf"}, {"matroska", "audio/x-matroska"}, {"asf", "audio/x-ms-wma"}, {"tta", "audio/x-tta"}, {"mpc8", "audio/x-musepack"}} {
		f.c.Exec(`UPDATE catalog_assets SET container=? WHERE token=?`, tc.container, song.Token)
		got := f.browse("e"+song.Public, "BrowseMetadata", 0, 1)
		if len(got.objects) != 1 || len(got.objects[0].Res) != 1 {
			t.Fatal(got)
		}
		r := got.objects[0].Res[0]
		if r.Protocol != "http-get:*:"+tc.mime+":"+contentFeatures || r.SampleRate != "192000" || r.BitDepth != "24" || r.Channels != "6" {
			t.Fatal(tc, r)
		}
	}
	// A source replacement cannot leave old technical claims in the resource.
	f.c.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE token=?`, song.Token)
	got := f.browse("e"+song.Public, "BrowseMetadata", 0, 1)
	r := got.objects[0].Res[0]
	if r.SampleRate != "" || r.BitDepth != "" || r.Channels != "" {
		t.Fatal("stale measured facts escaped", r)
	}
}
