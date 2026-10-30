package catalog

// MusicFormatBadge is a notable, measured format, never a source locator or a
// playback capability. Album headers receive at most two distinct descriptors.
type MusicFormatBadge struct {
	Variant    string `json:"variant"`
	Codec      string `json:"codec,omitempty"`
	BitDepth   int    `json:"bitDepth,omitempty"`
	SampleRate int    `json:"sampleRate,omitempty"`
	Layout     string `json:"layout,omitempty"`
}

type MusicFormatBadges []MusicFormatBadge

func (MusicFormatBadges) JSONSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 2, "items": map[string]any{
		"oneOf": []any{
			map[string]any{"type": "object", "additionalProperties": false, "required": []string{"variant", "codec", "bitDepth", "sampleRate"}, "properties": map[string]any{"variant": map[string]any{"const": "lossless"}, "codec": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.+-]{1,64}$"}, "bitDepth": map[string]any{"type": "integer", "minimum": 17}, "sampleRate": map[string]any{"type": "integer", "minimum": 1}}},
			map[string]any{"type": "object", "additionalProperties": false, "required": []string{"variant"}, "properties": map[string]any{"variant": map[string]any{"const": "atmos"}}},
			map[string]any{"type": "object", "additionalProperties": false, "required": []string{"variant"}, "properties": map[string]any{"variant": map[string]any{"const": "dsd"}}},
			map[string]any{"type": "object", "additionalProperties": false, "required": []string{"variant", "layout"}, "properties": map[string]any{"variant": map[string]any{"const": "surround"}, "layout": map[string]any{"enum": []string{"5.1", "7.1"}}}},
		},
	}}
}

// A single indexed album-membership query, independent of the loaded song page.
// Every alternative source must still be available, current and visible to this
// viewer. SQL reduces to at most two distinct public descriptors; no song list,
// asset IDs or per-track requests cross the boundary. No badges on album cards.
func (s *Service) albumMusicBadges(r ContentRequest, album string) (MusicFormatBadges, error) {
	visible, args := r.Viewer.itemVisibilitySQL("song.id")
	bind := []any{album, r.Library}
	bind = append(bind, args...)
	rows, err := s.read().Query(`WITH formats AS (
 SELECT lower(f.codec) codec,f.bit_depth,f.sample_rate,f.lossless,`+musicLayoutProjection+` layout,`+musicAtmosProjection+` atmos
 FROM catalog_entities album
 JOIN catalog_libraries library ON library.id=album.library_id
 CROSS JOIN catalog_songs member INDEXED BY catalog_songs_album_order ON member.album_id=album.id
 JOIN catalog_entities song ON song.id=member.entity_id AND song.library_id=album.library_id
 JOIN catalog_asset_links link ON link.entity_id=song.id AND link.available=1
 JOIN catalog_assets a ON a.id=link.asset_id AND a.available=1
 JOIN music_technical_facts f ON f.asset_id=a.token AND f.size=a.size AND f.modified_ns=a.modified_ns
 WHERE album.public_id=pid_blob(?) AND library.library_id=? AND album.kind=6 AND album.retired=0 AND song.kind=7 AND `+visible+`
), badges AS (
 SELECT 0 priority,'lossless' kind,codec,bit_depth,sample_rate,'' layout FROM formats WHERE atmos=0 AND codec NOT GLOB 'dsd*' AND lossless=1 AND bit_depth>16 AND sample_rate>0
 UNION SELECT 1,'atmos','',0,0,'' FROM formats WHERE atmos=1
 UNION SELECT 2,'dsd','',0,0,'' FROM formats WHERE atmos=0 AND (codec='dsd' OR codec GLOB 'dsd_*')
 UNION SELECT 3,'surround','',0,0,CASE WHEN layout IN('5.1','5.1(side)') THEN '5.1' ELSE '7.1' END FROM formats WHERE atmos=0 AND layout IN('5.1','5.1(side)','7.1','7.1(wide)','7.1(wide-side)')
) SELECT kind,codec,bit_depth,sample_rate,layout FROM badges ORDER BY priority,codec,bit_depth DESC,sample_rate DESC,layout LIMIT 2`, bind...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	badges := MusicFormatBadges{}
	for rows.Next() {
		var badge MusicFormatBadge
		if err := rows.Scan(&badge.Variant, &badge.Codec, &badge.BitDepth, &badge.SampleRate, &badge.Layout); err != nil {
			return nil, err
		}
		badges = append(badges, badge)
	}
	return badges, rows.Err()
}
