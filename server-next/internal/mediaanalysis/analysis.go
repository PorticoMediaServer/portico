// Package mediaanalysis holds the measurements the server makes of a file's
// picture and sound: loudness, the audio fingerprint and frame evidence used to
// find intros and credits, and the spectral summary used for music similarity.
package mediaanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

var (
	ErrBudget      = errors.New("Analysis exceeded the configured work or generated-storage budget.")
	ErrUnsupported = errors.New("This analysis is not supported by the selected media or installed decoder.")
)

// Decode runs one bounded analysis decode of the keyed input and hands its
// output to consume. The caller supplies it (decoder.RunAnalysis over its own
// input bridge).
type Decode func(context.Context, string, decoder.AnalysisSpec, func(io.Reader) error) error

func token(parts ...string) string {
	h := sha256.New()
	for _, v := range parts {
		h.Write([]byte(strconv.Itoa(len(v)) + ":" + v))
	}
	return hex.EncodeToString(h.Sum(nil))
}
