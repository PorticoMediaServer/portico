package storage

import (
	"context"
	"io"
	"time"
)

// readRemoteSmall keeps the path server-private and the read bounded. Inventory
// invocations use their typed current-job producer; rooted scan checks surround
// the read, and native sources never fall through to an empty mount path.
func (c *Client) readRemoteSmall(ctx context.Context, path string, limit int64) ([]byte, error) {
	return c.readRemoteBlob(ctx, path, limit, 8<<20)
}
func (c *Client) readRemoteBlob(ctx context.Context, path string, limit, maximum int64) ([]byte, error) {
	if limit <= 0 || limit > maximum {
		return nil, ErrRemoteLimit
	}
	req, input, err := inventoryReadRequest(ctx, path)
	if err != nil {
		return nil, err
	}
	if input != nil {
		reader, ok := c.Remote.(RemoteInventoryBlobReader)
		if !ok || input.client != c || input.blob == nil {
			return nil, ErrRemoteConfig
		}
		if err = CheckScanRead(ctx, path); err != nil {
			return nil, err
		}
		raw, err := reader.ReadInventoryBlob(ctx, input.source, req, *input.blob, limit)
		if err != nil {
			clear(raw)
			return nil, err
		}
		if int64(len(raw)) > limit {
			clear(raw)
			return nil, ErrRemoteLimit
		}
		if err = CheckScanRead(ctx, path); err != nil {
			clear(raw)
			return nil, err
		}
		return raw, nil
	}
	if err = CheckScanRead(ctx, path); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	object, err := c.Remote.Acquire(ctx, path, "analysis")
	if err != nil {
		return nil, err
	}
	defer object.Close()
	before := object.Snapshot()
	if before.Directory || before.Size < 0 || before.Size > limit {
		return nil, ErrRemoteLimit
	}
	var raw []byte
	if before.Size > 0 {
		body, e := object.OpenRange(ctx, 0, before.Size)
		if e != nil {
			return nil, e
		}
		raw, err = io.ReadAll(io.LimitReader(body, limit+1))
		body.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(raw)) != before.Size {
			return nil, ErrRemoteChanged
		}
	}
	if err = CheckScanRead(ctx, path); err != nil {
		return nil, err
	}
	after, err := object.Observe(ctx)
	if err != nil {
		return nil, err
	}
	if after.Revision != before.Revision {
		return nil, ErrRemoteChanged
	}
	return raw, nil
}
