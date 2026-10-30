package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"reflect"
	"strings"
	"unicode/utf8"
)

// errStrictJSON is a body strictDecode refused (400 invalid_request).
var errStrictJSON = requestinput.New("invalid request body")
var errStrictMediaType = requestinput.New("send application/json with UTF-8 encoding")

func strictDecode(w http.ResponseWriter, r *http.Request, out any) error {
	if err := strictDecodeLimit(w, r, out, 1<<20); err != nil {
		return errStrictJSON
	}
	return nil
}

// strictDecodeLimit keeps the shared document/field checks while allowing an
// edge to retain its own body limit and distinguish media/size refusals.
func strictDecodeLimit(w http.ResponseWriter, r *http.Request, out any, limit int64) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		media, params, err := mime.ParseMediaType(contentType)
		if err != nil || media != "application/json" {
			return errStrictMediaType
		}
		for key, value := range params {
			if key != "charset" || !strings.EqualFold(value, "utf-8") {
				return errStrictMediaType
			}
		}
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return errStrictJSON
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	if err = strictJSONValue(tokens, 0); err != nil {
		return errStrictJSON
	}
	if _, err = tokens.Token(); err != io.EOF {
		return errStrictJSON
	}
	if err = strictJSONShape(raw, reflect.TypeOf(out)); err != nil {
		return errStrictJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return errStrictJSON
	}
	return nil
}

// encoding/json accepts case-insensitive field aliases; the queue protocol does not.
func strictJSONShape(raw []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		for key, value := range object {
			field, ok := fields[key]
			if !ok {
				return errStrictJSON
			}
			if err := strictJSONShape(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := strictJSONShape(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
func strictJSONValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errStrictJSON
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return e
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errStrictJSON
			}
			seen[name] = true
			if e = strictJSONValue(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if err = strictJSONValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errStrictJSON
	}
	_, err = d.Token()
	return err
}
