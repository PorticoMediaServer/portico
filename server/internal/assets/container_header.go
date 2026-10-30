package assets

import (
	"context"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

const containerHeaderLimit = 4096

// FFprobe names both formats "matroska,webm". Only the EBML header's DocType
// establishes WebM; an extension or a codec combination cannot do so.
func containerFromHeader(container string, prefix []byte) string {
	if container != "matroska" {
		return container
	}
	id, n, ok := ebmlInteger(prefix, true)
	if !ok || id != 0x1a45dfa3 {
		return container
	}
	size, m, ok := ebmlInteger(prefix[n:], false)
	if !ok || size > containerHeaderLimit || size > uint64(len(prefix)-n-m) {
		return container
	}
	header := prefix[n+m : n+m+int(size)]
	docType := ""
	seenDocType := false
	for len(header) > 0 {
		id, n, ok = ebmlInteger(header, true)
		if !ok {
			return container
		}
		size, m, ok = ebmlInteger(header[n:], false)
		if !ok || size > uint64(len(header)-n-m) {
			return container
		}
		if id == 0x4282 {
			if seenDocType {
				return container
			}
			seenDocType = true
			docType = string(header[n+m : n+m+int(size)])
		}
		header = header[n+m+int(size):]
	}
	if docType == "webm" {
		return "webm"
	}
	return container
}

// IDs retain the width marker; sizes do not. Unknown lengths cannot describe a
// bounded EBML header and are refused without looking past the prefix.
func ebmlInteger(data []byte, id bool) (uint64, int, bool) {
	if len(data) == 0 || data[0] == 0 {
		return 0, 0, false
	}
	n, marker := 1, byte(0x80)
	for data[0]&marker == 0 {
		n++
		marker >>= 1
	}
	if n > len(data) || id && n > 4 {
		return 0, 0, false
	}
	value := uint64(data[0])
	if !id {
		value &^= uint64(marker)
	}
	for _, b := range data[1:n] {
		value = value<<8 | uint64(b)
	}
	if !id && value == uint64(1)<<(7*n)-1 {
		return 0, 0, false
	}
	return value, n, true
}

func (p Probe) completeContainer(ctx context.Context, path string, f *Facts) error {
	if f.Container != "matroska" || strings.Contains(path, "://") {
		return nil
	}
	if p.ReadGuard != nil {
		if err := p.ReadGuard(ctx, path); err != nil {
			return err
		}
	}
	prefix, err := storage.ReadScanPrefix(ctx, path, containerHeaderLimit)
	if err != nil {
		return err
	}
	f.Container = containerFromHeader(f.Container, prefix)
	return nil
}
