package nativeinventory

func EnvelopeCandidate(raw []byte) bool              { return nativeEnvelopeCandidate(raw) }
func DecodeEnvelope(raw []byte) (*Wire, bool, error) { return decodeNativeEnvelope(raw) }
func ValidatePageText(p Page) error                  { return nativePageText(p) }

func MarshalRequest(w *Wire) ([]byte, error) { return marshalNativeRequest(w) }
