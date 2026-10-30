package compactcatalog

// Complete source primary keys used by bounded retirement.
var infoRetirePrimaryKeys = map[string]string{
	"catalog_info_input_families":                  "change_id,family",
	"catalog_info_input_witnesses":                 "change_id,ordinal",
	"catalog_info_new_chapter_facts":               "change_id,asset_id",
	"catalog_info_new_chapters":                    "change_id,asset_id,chapter_index",
	"catalog_info_new_disc_chapters":               "change_id,object_id,title_key,chapter_order",
	"catalog_info_new_disc_clips":                  "change_id,object_id,title_key,sequence",
	"catalog_info_new_disc_facts":                  "change_id,object_id",
	"catalog_info_new_disc_media_members":          "change_id,object_id,path",
	"catalog_info_new_disc_streams":                "change_id,object_id,title_key,stream_order",
	"catalog_info_new_disc_title_media":            "change_id,object_id,title_key",
	"catalog_info_new_disc_title_media_members":    "change_id,object_id,title_key,path",
	"catalog_info_new_disc_title_media_streams":    "change_id,object_id,title_key,stream_index",
	"catalog_info_new_disc_titles":                 "change_id,object_id,title_key",
	"catalog_info_new_stream_breadth":              "change_id,asset_id,stream_index",
	"catalog_info_new_stream_facts":                "change_id,asset_id",
	"catalog_info_new_streams":                     "change_id,asset_id,stream_index",
	"catalog_info_new_subtitle_facts":              "change_id,asset_id",
	"catalog_info_new_subtitle_inventory_evidence": "change_id,asset_id",
	"catalog_info_new_subtitle_sidecars":           "change_id,id",
	"catalog_info_new_subtitles":                   "change_id,id",
	"catalog_info_path_old":                        "change_id",
	"catalog_info_scalar_old":                      "change_id,field",
}
