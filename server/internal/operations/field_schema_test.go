package operations

import (
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

// The console registry publishes null defaults for its nullable integer
// settings. The generated decoder must accept those and keep every other
// required member, including non-null defaults, exactly as before.
func TestFieldSchemaAdmitsOnlyTheRequiredDefaultNull(t *testing.T) {
	shape, err := apikit.Schema(reflect.TypeFor[Field]())
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := apikit.Schema(reflect.TypeFor[fieldShape]())
	if err != nil {
		t.Fatal(err)
	}
	if shape["type"] != "object" || ordinary["type"] != "object" {
		t.Fatalf("field schema is not an object: %v", shape)
	}
	// Field preserves the ordinary method-free alias, including its additive
	// object semantics. The only change is that the required default admits null.
	expectedDefault := map[string]any{}
	for name, value := range ordinary["properties"].(map[string]any)["default"].(map[string]any) {
		expectedDefault[name] = value
	}
	expectedDefault["nullable"] = true
	ordinary["properties"].(map[string]any)["default"] = expectedDefault
	if !reflect.DeepEqual(shape, ordinary) {
		t.Fatalf("field schema changed beyond its nullable default: got %#v, want %#v", shape, ordinary)
	}
	required := map[string]bool{}
	for _, name := range shape["required"].([]string) {
		required[name] = true
	}
	for _, name := range []string{"id", "group", "scope", "overrideScopes", "type", "default", "saveMode", "applicationMode", "runtimeConsumer", "description", "permission", "secret", "retentionClass", "auditClass"} {
		if !required[name] {
			t.Fatalf("%s is no longer required: %v", name, shape["required"])
		}
	}
	for _, name := range []string{"min", "max", "options"} {
		if required[name] {
			t.Fatalf("%s became required: %v", name, shape["required"])
		}
	}
	properties := shape["properties"].(map[string]any)
	defaultProperty := properties["default"].(map[string]any)
	if defaultProperty["type"] != "any" || defaultProperty["nullable"] != true {
		t.Fatalf("default does not admit null: %v", defaultProperty)
	}

	var nullableIntegers, otherDefaults int
	for _, field := range Registry() {
		if field.Type != "nullable-integer" {
			if field.Default != nil {
				otherDefaults++
			}
			continue
		}
		if field.Default != nil {
			t.Fatalf("%s no longer publishes a null default: %#v", field.ID, field.Default)
		}
		nullableIntegers++
	}
	if nullableIntegers == 0 || otherDefaults == 0 {
		t.Fatalf("registry lost its default coverage: %d nullable integers, %d other defaults", nullableIntegers, otherDefaults)
	}
}
