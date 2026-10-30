package catalog

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

func (DashboardPeople) JSONSchema() map[string]any {
	type wire DashboardPeople
	s, e := apikit.Schema(reflect.TypeFor[wire]())
	if e != nil {
		panic(e)
	}
	p := s["properties"].(map[string]any)
	p["items"].(map[string]any)["maxItems"] = 100
	p["nextCursor"].(map[string]any)["maxLength"] = 512
	return s
}
func (DashboardHistory) JSONSchema() map[string]any {
	type wire DashboardHistory
	s, e := apikit.Schema(reflect.TypeFor[wire]())
	if e != nil {
		panic(e)
	}
	p := s["properties"].(map[string]any)
	p["buckets"].(map[string]any)["maxItems"] = 1201
	p["top"].(map[string]any)["maxItems"] = 6
	p["busiestTimes"].(map[string]any)["minItems"] = 7
	p["busiestTimes"].(map[string]any)["maxItems"] = 7
	row := p["busiestTimes"].(map[string]any)["items"].(map[string]any)
	row["minItems"], row["maxItems"] = 24, 24
	return s
}
