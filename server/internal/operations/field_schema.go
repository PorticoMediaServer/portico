package operations

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
)

// fieldShape is Field without this package's schema method, so the ordinary
// closed object can still be generated from its tags and required members.
type fieldShape Field

// JSONSchema publishes the console registry's real wire shape. A nullable
// integer setting's default is null on purpose, so the required default member
// admits null; it stays the single exception. Every other field, its
// requiredness and its nullable-integer bounds are unchanged.
func (Field) JSONSchema() map[string]any {
	shape, err := apikit.Schema(reflect.TypeFor[fieldShape]())
	if err != nil {
		panic(err)
	}
	shape["properties"].(map[string]any)["default"] = map[string]any{"type": "any", "nullable": true}
	return shape
}
