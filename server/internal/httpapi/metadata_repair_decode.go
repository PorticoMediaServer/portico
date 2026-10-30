package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// A full bounded credit family carries exact source references. This route-only
// bound leaves the generic decoder, all other routes and strict outer parsing intact.
func decodeMetadataRepair(w http.ResponseWriter, r *http.Request, value *metadata.RepairCommand) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return requestinput.New("invalid JSON request")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return requestinput.New("invalid JSON request")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return requestinput.New("one JSON object required")
	}
	return nil
}
