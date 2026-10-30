package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type inventoryAbsence struct {
	ErrorCode string `json:"errorCode,omitempty"`
	Absent    bool   `json:"absent"`
}

// LocalInventoryAbsence proves a named file is absent from a readable parent
// under the admitted root. Production calls it only in the supervised helper.
func LocalInventoryAbsence(r InventoryRequest) (bool, error) {
	if r.RootIdentity == "" || !filepath.IsLocal(r.RelativePath) || r.RelativePath == "." {
		return false, ErrInventoryChanged
	}
	parentRequest := r
	parentRequest.RelativePath = filepath.Dir(r.RelativePath)
	path, err := inventoryPath(parentRequest)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(r.Root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return false, ErrInventoryChanged
	}
	root, err := openInventoryRoot(r)
	if err != nil {
		return false, err
	}
	defer root.Close()
	handle, err := root.Open(".")
	if err != nil {
		return false, err
	}
	identity, err := FileObjectIdentity(handle)
	handle.Close()
	if err != nil || !SameRoot(r.RootIdentity, identity) {
		return false, ErrInventoryChanged
	}
	parent, err := root.OpenRoot(relative)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	_, err = parent.Lstat(filepath.Base(r.RelativePath))
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	folder, err := parent.Open(".")
	if err != nil {
		return false, err
	}
	defer folder.Close()
	_, err = folder.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return true, nil
}

func inventoryAbsenceHelper(r InventoryRequest, out io.Writer) error {
	absent, err := LocalInventoryAbsence(r)
	reply := inventoryAbsence{Absent: absent}
	if err != nil {
		reply.ErrorCode = inventoryErrorCode(err)
	}
	return json.NewEncoder(out).Encode(reply)
}

func (c *Client) InventoryAbsence(ctx context.Context, key string, r InventoryRequest) (bool, error) {
	if c.IsRemote(r.Root) {
		absence, ok := c.Remote.(RemoteInventoryAbsence)
		if !ok {
			// Never turn "this build has no remote proof" into an absence: the
			// walk keeps its cursor and its pending row. A local helper request
			// on a virtual root would prove the wrong namespace.
			return false, ErrRemoteCursor
		}
		return absence.InventoryAbsence(ctx, key, r)
	}
	var reply inventoryAbsence
	if err := c.inventoryRequest(ctx, key, "inventory-absence", r, &reply); err != nil {
		return false, err
	}
	return reply.Absent && reply.ErrorCode == "", inventoryCodeError(reply.ErrorCode)
}
