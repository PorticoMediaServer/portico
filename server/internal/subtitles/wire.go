package subtitles

// Base64Bytes retains encoding/json's byte-string representation and gives the
// public contract generator its explicit wire type.
type Base64Bytes []byte

func (Base64Bytes) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "format": "byte", "contentEncoding": "base64"}
}
