package operations

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

// Settings publishes the exact confidence choices accepted by its write path.
func (Settings) JSONSchema() map[string]any {
	type wire Settings
	schema, err := apikit.Schema(reflect.TypeFor[wire]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	properties["segments.tvMode"].(map[string]any)["enum"] = []string{"server", "standard", "thorough", "off"}
	properties["segments.animeMode"].(map[string]any)["enum"] = []string{"server", "standard", "thorough", "off"}
	properties["segments.movieMode"].(map[string]any)["enum"] = []string{"server", "standard", "thorough", "off"}
	properties["segments.useChapterNames"] = map[string]any{"type": "boolean"}
	properties["segments.viewerConfidencePercent"].(map[string]any)["enum"] = []int{30, 50, 70}
	properties["segments.automaticConfidencePercent"].(map[string]any)["enum"] = []int{70, 85, 95}
	properties["segments.shortestEpisode"].(map[string]any)["enum"] = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30}
	properties["segments.shortestFilm"].(map[string]any)["enum"] = []int{5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60}
	properties["segments.introWindow"].(map[string]any)["enum"] = []string{"usual", "long"}
	properties["segments.creditsWindow"].(map[string]any)["enum"] = []string{"usual", "long"}
	properties["segments.filmCreditsWindow"].(map[string]any)["enum"] = []string{"usual", "long"}
	return schema
}
