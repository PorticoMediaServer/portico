package httpapi

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// PublishedSchemas derives reusable schemas from the same wire types as the
// routes. Bounds here describe limits already enforced by their owning services.
func PublishedSchemas() (map[string]reflect.Type, map[reflect.Type]map[string]any, error) {
	named := map[string]reflect.Type{
		"LibraryChannelConfig":    reflect.TypeFor[librarychannels.Config](),
		"HomeLayout":              reflect.TypeFor[operations.HomeLayoutDocument](),
		"HomeDocument":            reflect.TypeFor[catalog.HomeDocument](),
		"DetailAction":            reflect.TypeFor[catalog.DetailAction](),
		"AudioRenditionPlan":      reflect.TypeFor[playback.AudioRenditionPlan](),
		"Capture":                 reflect.TypeFor[operations.Capture](),
		"FeedbackSubmission":      reflect.TypeFor[operations.FeedbackSubmission](),
		"ContentEntry":            reflect.TypeFor[catalog.ContentEntry](),
		"Category":                reflect.TypeFor[catalog.Category](),
		"FilesystemPage":          reflect.TypeFor[administration.FilesystemPage](),
		"MoveCollisionDetails":    reflect.TypeFor[MoveCollisionDetails](),
		"SimpleFacetPage":         reflect.TypeFor[catalog.SimpleFacetPage](),
		"SimpleFacetWindow":       reflect.TypeFor[catalog.SimpleFacetWindow](),
		"SimpleFacetPreparing":    reflect.TypeFor[catalog.SimpleFacetPreparing](),
		"SimpleFacetFallbackPage": reflect.TypeFor[catalog.SimpleFacetFallbackPage](),
		"SearchGenrePage":         reflect.TypeFor[catalog.SearchGenrePage](),
		"SearchGenrePreparing":    reflect.TypeFor[catalog.SearchGenrePreparing](),
		"SearchGenreFallbackPage": reflect.TypeFor[catalog.SearchGenreFallbackPage](),
	}
	wire := map[reflect.Type]map[string]any{}
	for name, typ := range named {
		shape, err := apikit.Schema(typ)
		if err != nil {
			return nil, nil, err
		}
		props, _ := shape["properties"].(map[string]any)
		field := func(key string) map[string]any { v, _ := props[key].(map[string]any); return v }
		switch name {
		case "SearchGenrePage":
			shape = genreReadySchema()
		case "SearchGenrePreparing":
			shape = genrePreparingSchema()
		case "SearchGenreFallbackPage":
			shape = genreFallbackSchema()
		case "HomeDocument":
			field("rows")["maxItems"] = 64
			field("spotlight")["maxItems"] = 5
		case "HomeLayout":
			for _, key := range []string{"rowOrder", "hiddenRowIds"} {
				v := field(key)
				v["maxItems"] = 64
				if item, ok := v["items"].(map[string]any); ok {
					item["maxLength"] = 128
				}
			}
		case "FilesystemPage":
			field("roots")["maxItems"] = 64
			field("entries")["maxItems"] = 200
			field("suggestions")["maxItems"] = 8
		case "Capture":
			field("component")["enum"] = []string{"scheduler"}
		case "FeedbackSubmission":
			field("message")["minLength"] = 8
			field("message")["maxLength"] = 2000
		}
		wire[typ] = shape
	}
	return named, wire, nil
}

type libraryCreationRequest struct {
	// ScanAfter defaults to true; false saves the library without its first scan.
	ScanAfter            *bool                             `json:"scanAfter,omitempty"`
	ExpectedRemoteSource catalog.ExpectedRemoteSourceInput `json:"expectedRemoteSource,omitempty"`
	Name                 string                            `json:"name"`
	Kind                 string                            `json:"kind"`
	Path                 string                            `json:"path,omitempty"`
	Folders              []string                          `json:"folders,omitempty"`
	MetadataAgent        string                            `json:"metadataAgent,omitempty"`
	MetadataLanguage     string                            `json:"metadataLanguage,omitempty"`
}

func (libraryCreationRequest) JSONSchema() map[string]any {
	type wire libraryCreationRequest
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["properties"].(map[string]any)["kind"].(map[string]any)["enum"] = []string{"movie", "tv", "anime", "music", "audiobook", "book", "comic"}
	folders := shape["properties"].(map[string]any)["folders"].(map[string]any)
	folders["minItems"] = 1
	folders["maxItems"] = 64
	return shape
}
