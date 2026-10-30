package metadata

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

func metadataArtworkEdgeSchema(t reflect.Type) map[string]any {
	shape, _ := apikit.Schema(t)
	shape["properties"].(map[string]any)["artworkMaxEdge"].(map[string]any)["enum"] = []int{1280, 1920, 2560}
	return shape
}

func (MetadataSettings) JSONSchema() map[string]any {
	type wire MetadataSettings
	return metadataArtworkEdgeSchema(reflect.TypeFor[wire]())
}

func (MetadataSettingsUpdate) JSONSchema() map[string]any {
	type wire MetadataSettingsUpdate
	return metadataArtworkEdgeSchema(reflect.TypeFor[wire]())
}
