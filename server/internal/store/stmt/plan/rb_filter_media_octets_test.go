package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func rbFilterMediaOctetControl(kind string) stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	d := stmt.Definition{Kind: stmt.Read, Owner: "store/read"}
	switch kind {
	case "attribution":
		d.ID, d.SQL = "read.browse.rb.filter.media.attribution.bytes", rbFilterMediaAttributionOctetsSQL
		d.Params, d.Plan = []stmt.Param{i("item"), i("library")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}
	case "links":
		d.ID, d.SQL = "read.browse.rb.filter.media.links.bytes", rbFilterMediaLinksOctetsSQL
		d.Params = []stmt.Param{i("library"), i("item"), i("limit")}
		d.Plan = stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, PrimaryKeys: []string{"a"}, Seeks: []string{"entity_id=?", "rowid=?"}}
	case "asset":
		d.ID, d.SQL = "read.browse.rb.filter.media.asset.bytes", rbFilterMediaAssetOctetsSQL
		d.Params, d.Plan = []stmt.Param{i("asset")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Seeks: []string{"rowid=?"}}
	case "sources":
		d.ID, d.SQL = "read.browse.rb.filter.media.sources.bytes", rbFilterMediaSourcesOctetsSQL
		d.Params, d.Plan = []stmt.Param{s("library"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"library_sources_library"}, Seeks: []string{"library_id=?"}}
	case "streams":
		d.ID, d.SQL = "read.browse.rb.filter.media.streams.bytes", rbFilterMediaStreamsOctetsSQL
		d.Params, d.Plan = []stmt.Param{s("asset"), i("after"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index>?"}}
	case "subtitles":
		d.ID, d.SQL = "read.browse.rb.filter.media.streams.subtitles.bytes", rbFilterMediaSubtitlesOctetsSQL
		d.Params, d.Plan = []stmt.Param{s("asset"), s("after"), i("limit")}, stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id>?"}}
	case "publication":
		d.ID, d.SQL = "read.browse.rb.filter.media.publication.bytes", rbFilterMediaPublicationOctetsSQL
		d.Params, d.Plan = []stmt.Param{i("item"), i("after"), i("limit")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"p"}}
	}
	return d
}

func TestRBFilterMediaOctetPurpose(t *testing.T) {
	for _, kind := range []string{"attribution", "links", "asset", "sources", "streams", "subtitles", "publication"} {
		original := rbFilterMediaOctetControl(kind)
		if !closedOctetDefinition(original) || checkSyntax(original) != nil || len(LintSQL(original.SQL)) == 0 {
			t.Fatal("closed media filter point widened generic grammar", kind)
		}
		mutations := []func(*stmt.Definition){
			func(d *stmt.Definition) { d.ID += ".borrowed" },
			func(d *stmt.Definition) { d.SQL += " " },
			func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "id=?", "id>?", 1) },
			func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "octet_length", "length", 1) },
			func(d *stmt.Definition) { d.Owner = "other" },
			func(d *stmt.Definition) { d.Params[0].Nullable = true },
			func(d *stmt.Definition) { d.Params[0].Type = stmt.Blob },
			func(d *stmt.Definition) { d.Plan.Hot = false },
			func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"l"} },
			func(d *stmt.Definition) { d.Plan.Indexes = append(d.Plan.Indexes, "foreign") },
			func(d *stmt.Definition) { d.Plan.PrimaryKeys = append(d.Plan.PrimaryKeys, "foreign") },
		}
		for n, mutate := range mutations {
			changed := rbFilterMediaOctetControl(kind)
			mutate(&changed)
			if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
				t.Fatal("media filter byte authority borrowed", kind, n)
			}
		}
	}
}
