package httpapi

// GrandfatheredUnionTags keeps the domain tags response unions already had
// before R-09b (an event's type, a reading location's format, an offline
// refresh's outcome), keyed "route path". Every other response union is tagged
// by variant; apigen fails on anything else. Do not add entries: new unions use
// variant.
var GrandfatheredUnionTags = map[string]string{
	"get_reading_sessions_sessionId_annotations $.entries[items].location":  "format",
	"post_metadata_bulk_preview $|oneOf0.fields[additionalProperties]":      "state",
	"get_shows_id_recommendations_row $.seeAll":                             "type",
	"get_shows_id_recommendations_row $.children[items].seeAll":             "type",
	"get_shows_id_recommendations $.rows[items].seeAll":                     "type",
	"get_shows_id_recommendations $.rows[items].children[items].seeAll":     "type",
	"get_metadata_bulk_preview_query $|oneOf0.fields[additionalProperties]": "state",
	"get_items_id_recommendations_row $.seeAll":                             "type",
	"get_items_id_recommendations_row $.children[items].seeAll":             "type",
	"get_items_id_recommendations $.rows[items].seeAll":                     "type",
	"get_items_id_recommendations $.rows[items].children[items].seeAll":     "type",
	"get_home_row $.seeAll":                         "type",
	"get_home_row $.children[items].seeAll":         "type",
	"get_home $.rows[items].seeAll":                 "type",
	"get_home $.rows[items].children[items].seeAll": "type",
	"post_reading_offline_progress_read $.results[items]|oneOf0.progress|anyOf0.location": "format",
	"post_reading_offline_progress_read $.results[items]":                                 "outcome",
	"post_reading_offline_progress $.progress|anyOf0.location":                            "format",
	"get_selection_action_preparation $":                                                  "kind",
	"get_music_mixes $.data.mixes[items].nameSource":                                      "kind",
}
