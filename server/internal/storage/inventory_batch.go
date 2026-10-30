package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// A library built from empty lists one folder per film, season or album, and
// each listing was one helper process: starting the server's own binary costs
// tens of milliseconds, far more than reading a small folder, so a library of
// fifty thousand film folders spent most of its scan starting processes. A
// batch lists or checks several folders of one root in one helper, still
// killable and still bounded by the same timeout. One folder that fails gives
// that folder its own error; the others are unaffected.

// MaxInventoryBatch bounds the folders one helper lists or checks.
const MaxInventoryBatch = 32

// inventoryBatchOutput bounds a batch's response (32 full pages);
// inventoryBatchInput keeps its request inside what the helper reads.
const (
	inventoryBatchOutput = 8 << 20
	inventoryBatchInput  = 60 << 10
)

var errInventoryBatch = errors.New("invalid inventory batch")

// InventoryPages lists one page of each folder. The per-folder errors line up
// with the requests; the returned error means none was listed (the caller
// lists them one by one instead). Where listings are retained streams
// (InventoryPage off Linux) the folders are listed one by one.
func (c *Client) InventoryPages(ctx context.Context, key string, requests []InventoryRequest) ([]InventoryPage, []error, error) {
	pages, errs := make([]InventoryPage, len(requests)), make([]error, len(requests))
	if err := c.checkInventoryBatch(requests); err != nil {
		return nil, nil, err
	}
	if runtime.GOOS != "linux" {
		for i, r := range requests {
			pages[i], errs[i] = c.InventoryPage(ctx, key, r)
		}
		return pages, errs, nil
	}
	err := c.inventoryBatchRequest(ctx, key, "inventory-pages", requests, 0, func(i int, d *json.Decoder) error {
		if err := d.Decode(&pages[i]); err != nil {
			return err
		}
		errs[i] = inventoryCodeError(pages[i].ErrorCode)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return pages, errs, nil
}

// InventoryDirectories reads each folder's identity and revision, as
// InventoryDirectory does for one.
func (c *Client) InventoryDirectories(ctx context.Context, key string, requests []InventoryRequest) ([]Snapshot, []error, error) {
	values, errs := make([]Snapshot, len(requests)), make([]error, len(requests))
	if err := c.checkInventoryBatch(requests); err != nil {
		return nil, nil, err
	}
	err := c.inventoryBatchRequest(ctx, key, "inventory-directories", requests, 0, func(i int, d *json.Decoder) error {
		if err := d.Decode(&values[i]); err != nil {
			return err
		}
		errs[i] = inventoryCodeError(values[i].ErrorCode)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return values, errs, nil
}

// checkInventoryBatch admits a batch of local folders of one root.
func (c *Client) checkInventoryBatch(requests []InventoryRequest) error {
	if len(requests) == 0 || len(requests) > MaxInventoryBatch {
		return errInventoryBatch
	}
	for _, r := range requests {
		if r.Root != requests[0].Root || r.Stream || c.IsRemote(r.Root) {
			return errInventoryBatch
		}
	}
	return nil
}

func (c *Client) inventoryBatchRequest(ctx context.Context, key, op string, requests []InventoryRequest, budget int, decode func(int, *json.Decoder) error) error {
	for _, r := range requests {
		if c.Guard != nil {
			if err := c.Guard(filepath.Join(r.Root, r.RelativePath)); err != nil {
				return err
			}
		}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := request{Operation: op, Path: filepath.Join(requests[0].Root, requests[0].RelativePath), Inventories: requests, InventoryBudget: budget}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(req.Path)
	}
	data, _ := json.Marshal(req)
	if len(data) > inventoryBatchInput {
		return errInventoryBatch
	}
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(data)
	return c.Supervisor.Run(ctx, "inventory:"+key, cmd, func(reader io.Reader) error {
		d := json.NewDecoder(io.LimitReader(reader, inventoryBatchOutput))
		for i := range requests {
			if err := decode(i, d); err != nil {
				return err
			}
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return errors.New("invalid inventory response")
		}
		return nil
	})
}

// inventoryBatchHelper answers a batch in the helper: one value per folder,
// in order, each a page (or snapshot) or the folder's error code.
func inventoryBatchHelper(op string, requests []InventoryRequest, out io.Writer) error {
	if len(requests) == 0 || len(requests) > MaxInventoryBatch {
		return errInventoryBatch
	}
	encoder := json.NewEncoder(out)
	for _, r := range requests {
		if r.Root != requests[0].Root || r.Stream {
			return errInventoryBatch
		}
		var err error
		switch op {
		case "inventory-pages":
			var p InventoryPage
			if p, err = LocalInventoryPage(r); err == nil {
				err = encoder.Encode(p)
			} else {
				err = inventoryFailure(err, out)
			}
		case "inventory-directories":
			var v Snapshot
			if v, err = inspectInventoryDirectory(r); err == nil {
				err = encoder.Encode(v)
			} else {
				err = inventoryFailure(err, out)
			}
		default:
			return errInventoryBatch
		}
		if err != nil {
			return err
		}
	}
	return nil
}
