package assets_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestDecodedDolbyVisionLayerPublishesCanonicalBreadth(t *testing.T) {
	for _, tc := range []struct{ offset, layer string }{{"0", "mel"}, {"1", "fel"}} {
		t.Run(tc.layer, func(t *testing.T) {
			c := catalogtest.Open(t)
			lib := c.Library("dv", "DV", "movie", "/dv")
			item := c.Movie(lib, "/dv/file.mp4", "DV", 2026)
			c.Drain()
			stream := assets.Stream{Index: 0, Type: "video", Codec: "hevc", Detail: assets.StreamDetail{DolbyVisionProfile: 7, EnhancementLayer: true}}
			component := `{"nlq_offset":` + tc.offset + `,"vdr_in_max":8388608,"linear_deadzone_slope":0,"linear_deadzone_threshold":0}`
			raw := `{"frames":[{"media_type":"video","width":160,"height":90,"side_data_list":[{"side_data_type":"Dolby Vision Metadata","rpu_type":2,"bl_bit_depth":10,"disable_residual_flag":0,"nlq_method_idc":0,"coef_log2_denom":23,"components":[` + strings.Join([]string{component, component, component}, ",") + `]}]}]}`
			if err := assets.ApplyFrameHDR([]byte(raw), &stream.Detail); err != nil {
				t.Fatal(err)
			}
			c.Write(func(_ context.Context, tx *sql.Tx) error {
				return assets.PersistStreams(tx, item.Token, 1000, 1, []assets.Stream{stream})
			})
			saved, err := assets.ReadStreamBreadth(context.Background(), c.DB, item.Token)
			if err != nil || saved[0].DolbyVisionEnhancementType != tc.layer || !saved[0].DolbyVisionMetadataKnown {
				t.Fatal("decoded layer did not publish canonical scalar", saved, err)
			}
		})
	}
}
