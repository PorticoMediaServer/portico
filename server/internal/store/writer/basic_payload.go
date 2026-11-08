package writer

import "reflect"

func basicCopyPayloadBytes(v reflect.Value, remaining *uint64) error {
	var size uint64
	switch v.Kind() {
	case reflect.Struct:
		for n := 0; n < v.NumField(); n++ {
			if err := basicCopyPayloadBytes(v.Field(n), remaining); err != nil {
				return err
			}
		}
		return nil
	case reflect.Array, reflect.Slice:
		if v.Kind() == reflect.Slice {
			if *remaining < 8 {
				return ErrCap
			}
			*remaining -= 8
		}
		for n := 0; n < v.Len(); n++ {
			if err := basicCopyPayloadBytes(v.Index(n), remaining); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		size = uint64(v.Len())
	case reflect.Bool, reflect.Uint8:
		size = 1
	case reflect.Int, reflect.Int64, reflect.Float64:
		size = 8
	default:
		return ErrInvalid
	}
	if size > *remaining {
		return ErrCap
	}
	*remaining -= size
	return nil
}
