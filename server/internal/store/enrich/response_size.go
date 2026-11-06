package enrich

import (
	"errors"
	"fmt"
	"io"
)

// ResponseSizeError refuses the WHOLE response without returning a prefix.
// ObservedBytes is actual observed data, never an untrusted Content-Length.
// A bounded HTTP reader sets ObservedIsLowerBound when it stopped at limit+1.
// errors.Is still identifies ErrResponse through any provider-floor wrapper.
type ResponseSizeError struct {
	LimitBytes, ObservedBytes int64
	ObservedIsLowerBound      bool
}

func (e *ResponseSizeError) Error() string {
	if e == nil {
		return ErrResponse.Error()
	}
	if e.ObservedIsLowerBound {
		return fmt.Sprintf("%s: observed at least %d bytes, limit %d", ErrResponse, e.ObservedBytes, e.LimitBytes)
	}
	return fmt.Sprintf("%s: observed %d bytes, limit %d", ErrResponse, e.ObservedBytes, e.LimitBytes)
}

func (*ResponseSizeError) Unwrap() error { return ErrResponse }

// The HTTP adapter must close its response before returning to the broker.
// Read at most limit+1 ACTUAL bytes; never trust Content-Length or return a
// successful truncated prefix. Even simultaneous read failure keeps the size
// diagnostic when oversize has already been observed.
func readTMDBResponse(body io.Reader, limit int64) ([]byte, error) {
	if body == nil || limit <= 0 || limit > 4<<20 {
		return nil, ErrTMDBPolicy
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.Join(&ResponseSizeError{LimitBytes: limit, ObservedBytes: int64(len(data)), ObservedIsLowerBound: true}, err)
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}
