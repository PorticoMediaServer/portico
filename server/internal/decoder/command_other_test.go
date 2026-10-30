//go:build !linux

package decoder

func helperSpec(string) (string, []string, error) { return "", nil, ErrInvalidConfiguration }
