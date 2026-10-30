package httpapi

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

type MusicMixesView struct {
	Mixes []MusicMixView `json:"mixes"`
}

type MusicMixView struct {
	ID         string                     `json:"id"`
	NameSource MusicMixNameSourceView     `json:"nameSource"`
	Tint       string                     `json:"tint"`
	Covers     []MusicMixCoverView        `json:"covers"`
	Artists    []catalog.ContentEntityRef `json:"artists"`
	More       bool                       `json:"more"`
	SongCount  int                        `json:"songCount"`
}

type MusicMixCoverView struct {
	Album     catalog.ContentEntityRef `json:"album"`
	PosterURL string                   `json:"posterUrl"`
}

type MusicMixNameSourceView struct {
	Kind     string `json:"kind"`
	Tag      string `json:"tag,omitempty"`
	Model    string `json:"model,omitempty"`
	Version  string `json:"version,omitempty"`
	ArtistID string `json:"artistId,omitempty"`
}

func (MusicMixNameSourceView) JSONSchema() map[string]any {
	text := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	return map[string]any{"oneOf": []any{
		map[string]any{"type": "object", "additionalProperties": false,
			"properties": map[string]any{"kind": map[string]any{"type": "string", "const": "style"}, "tag": text(), "model": text(), "version": text()},
			"required":   []string{"kind", "tag", "model", "version"}},
		map[string]any{"type": "object", "additionalProperties": false,
			"properties": map[string]any{"kind": map[string]any{"type": "string", "const": "artist"}, "artistId": text()},
			"required":   []string{"kind", "artistId"}},
	}}
}

func (MusicMixView) JSONSchema() map[string]any {
	type wire MusicMixView
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	properties := shape["properties"].(map[string]any)
	properties["id"].(map[string]any)["minLength"] = 1
	properties["tint"].(map[string]any)["pattern"] = "^#[0-9a-fA-F]{6}$"
	covers := properties["covers"].(map[string]any)
	covers["minItems"], covers["maxItems"] = 3, 3
	properties["artists"].(map[string]any)["maxItems"] = 2
	count := properties["songCount"].(map[string]any)
	count["minimum"], count["maximum"] = 1, catalog.MaxPlaylistEntries
	return shape
}

func (musicMixSaveChange) JSONSchema() map[string]any {
	type wire musicMixSaveChange
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["additionalProperties"] = false
	properties := shape["properties"].(map[string]any)
	properties["operationId"].(map[string]any)["minLength"] = 1
	name := properties["name"].(map[string]any)
	name["minLength"], name["maxLength"] = 1, 200
	return shape
}
