package qualification

import (
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Encode seals raw values, never their presentation as JSON or text. Strings
// retain every byte; types, field names, collection lengths and nil values are
// explicit. New exported struct fields automatically become part of identity.
// Unsupported/private fields fail closed instead of being silently omitted.
func Encode(value any) ([]byte, error) {
	e := canonicalEncoder{}
	e.text("portico-stmt-receipt-v1")
	if err := e.value(reflect.ValueOf(value), 0); err != nil {
		return nil, err
	}
	return e.data, nil
}

type canonicalEncoder struct{ data []byte }

func (e *canonicalEncoder) number(v uint64) { e.data = binary.BigEndian.AppendUint64(e.data, v) }
func (e *canonicalEncoder) text(v string) {
	e.number(uint64(len(v)))
	e.data = append(e.data, v...)
}
func (e *canonicalEncoder) flag(v bool) {
	if v {
		e.data = append(e.data, 1)
	} else {
		e.data = append(e.data, 0)
	}
}
func (e *canonicalEncoder) value(v reflect.Value, depth int) error {
	if depth > 64 {
		return fmt.Errorf("receipt encoding exceeds structural depth")
	}
	if !v.IsValid() {
		e.text("nil")
		return nil
	}
	t := v.Type()
	e.text(t.Kind().String())
	e.text(t.PkgPath())
	e.text(t.Name())
	switch v.Kind() {
	case reflect.String:
		e.text(v.String())
	case reflect.Bool:
		e.flag(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.number(uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.number(v.Uint())
	case reflect.Float64:
		e.number(math.Float64bits(v.Float()))
	case reflect.Float32:
		e.number(uint64(math.Float32bits(float32(v.Float()))))
	case reflect.Interface, reflect.Pointer:
		e.flag(!v.IsNil())
		if !v.IsNil() {
			return e.value(v.Elem(), depth+1)
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			e.flag(!v.IsNil())
		}
		e.number(uint64(v.Len()))
		for i := 0; i < v.Len(); i++ {
			if err := e.value(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		e.number(uint64(v.NumField()))
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				return fmt.Errorf("private receipt field %s.%s", t, field.Name)
			}
			e.text(field.Name)
			if err := e.value(v.Field(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("unsupported receipt map key %s", t.Key())
		}
		e.flag(!v.IsNil())
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		e.number(uint64(len(keys)))
		for _, key := range keys {
			if err := e.value(key, depth+1); err != nil {
				return err
			}
			if err := e.value(v.MapIndex(key), depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported receipt value %s", t)
	}
	return nil
}
