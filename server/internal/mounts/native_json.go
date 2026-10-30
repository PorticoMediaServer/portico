package mounts

import (
	"bufio"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Read one bounded JSON object at a time, imposing the byte/depth ceiling BEFORE
// json.Unmarshal can allocate for attacker/provider-controlled element contents.
func eachNativeJSON(input io.Reader, visit func([]byte) error) error {
	r := bufio.NewReaderSize(input, 8192)
	next := func() (byte, error) {
		for {
			b, e := r.ReadByte()
			if e != nil {
				return 0, e
			}
			if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
				return b, nil
			}
		}
	}
	b, e := next()
	if e != nil || b != '[' {
		return storage.ErrRemoteConfig
	}
	b, e = next()
	if e != nil {
		return storage.ErrRemoteConfig
	}
	n := 0
	for b != ']' {
		if b != '{' {
			return storage.ErrRemoteConfig
		}
		if n >= 2000000 {
			return storage.ErrRemoteLimit
		}
		n++
		raw := make([]byte, 1, 1024)
		raw[0] = b
		depth := 1
		quoted, escape := false, false
		for depth > 0 {
			b, e = r.ReadByte()
			if e != nil {
				return storage.ErrRemoteConfig
			}
			if len(raw) >= 64<<10 {
				return storage.ErrRemoteLimit
			}
			raw = append(raw, b)
			if quoted {
				if escape {
					escape = false
				} else if b == '\\' {
					escape = true
				} else if b == '"' {
					quoted = false
				}
				continue
			}
			switch b {
			case '"':
				quoted = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
			if depth > 24 {
				return storage.ErrRemoteLimit
			}
		}
		if e = visit(raw); e != nil {
			return e
		}
		b, e = next()
		if e != nil {
			return storage.ErrRemoteConfig
		}
		if b == ']' {
			break
		}
		if b != ',' {
			return storage.ErrRemoteConfig
		}
		b, e = next()
		if e != nil || b != '{' {
			return storage.ErrRemoteConfig
		}
	}
	if _, e = next(); e != io.EOF {
		return storage.ErrRemoteConfig
	}
	return nil
}
