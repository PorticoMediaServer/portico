package scanbench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// ReceiptJournal is a bounded harness journal under an exclusively created
// directory. It conveys no authority. Keep it until the run's recovery evidence
// is retired; do not confuse a file write with a committed writer receipt.
// One consumer owns it, and its retained directory handle confines all writes.
type ReceiptJournal struct{ root *os.Root }

func NewReceiptJournal(directory string) (*ReceiptJournal, error) {
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &ReceiptJournal{root: root}, nil
}

func (j *ReceiptJournal) Close() error {
	if j == nil || j.root == nil {
		return nil
	}
	err := j.root.Close()
	j.root = nil
	return err
}

type journalEntry struct {
	Version       int                       `json:"version"`
	Qualification bool                      `json:"qualification"`
	Original      writer.ScanReceiptRequest `json:"original"`
	Purpose       writer.CommandID          `json:"purpose"`
	Consumed      *writer.ScanRunSnapshot   `json:"consumed_lifecycle,omitempty"`
	Inventory     *writer.InventoryResult   `json:"consumed_inventory,omitempty"`
}

// save records the ORIGINAL before Apply/Accept. Lifecycle consumption is
// recorded separately before result release/ACK; it never overwrites the
// original. Exclusive files make an accidental second identity fail closed.
func (j *ReceiptJournal) save(original writer.ScanReceiptRequest, purpose writer.CommandID, value *writer.ScanRunSnapshot) error {
	if j == nil || j.root == nil || original.Receipt.Identity.Sequence <= 0 || original.RunID == "" ||
		original.Receipt.Identity.Incarnation == ([16]byte{}) || original.Receipt.Identity.OperationID == ([16]byte{}) || original.Receipt.RequestDigest == (writer.RequestDigest{}) ||
		(purpose != writer.InventoryCreate && purpose != writer.InventoryClaim && purpose != writer.InventoryAccept) {
		return writer.ErrInvalid
	}
	n := len(original.RunID) + len(original.LibraryKey)
	if value != nil {
		n += len(value.RunID) + len(value.RootIdentity) + len(value.RootRevision) + len(value.RootCursor)
	}
	if n > 32<<10 {
		return writer.ErrInvalid
	}
	if value != nil {
		if err := j.requireOriginal(original, purpose); err != nil {
			return err
		}
	}
	entry := journalEntry{Version: 1, Original: original, Purpose: purpose, Consumed: value}
	return j.write(entry)
}

func (j *ReceiptJournal) saveInventory(original writer.ScanReceiptRequest, value writer.InventoryResult) error {
	if j == nil || j.root == nil || value.Outcome.State != writer.Applied || value.Outcome.Reference != original.Receipt ||
		value.RunID != original.RunID || value.ResultCount != 1 || len(value.Members) != 1 || value.Digest == (writer.ResultDigest{}) ||
		value.AcceptedRunRevision <= 0 || value.NextCursor == "" || value.Members[0].SubjectID <= 0 || value.Members[0].AssetID <= 0 || value.Members[0].AcceptedAssetRevision <= 0 {
		return writer.ErrResolveRequired
	}
	if err := j.requireOriginal(original, writer.InventoryAccept); err != nil {
		return err
	}
	n := len(original.RunID) + len(original.LibraryKey) + len(value.RunID) + len(value.SourceID) + len(value.SourceIncarnation) + len(value.SourceRevision) + len(value.NextCursor) + len(value.Members[0].ObjectID)
	if n > 32<<10 {
		return writer.ErrInvalid
	}
	return j.write(journalEntry{Version: 1, Original: original, Purpose: writer.InventoryAccept, Inventory: &value})
}

func (j *ReceiptJournal) write(entry journalEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return writer.ErrInvalid
	}
	suffix := "original"
	if entry.Consumed != nil || entry.Inventory != nil {
		suffix = "consumed"
	}
	id := entry.Original.Receipt.Identity
	name := fmt.Sprintf("%x-%d-%x-%s.json", id.Incarnation, id.Sequence, id.OperationID, suffix)
	file, err := j.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// A failed write remains evidence and is never silently reused. No writer
	// effect has been offered before a successful original save.
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	dir, err := j.root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (j *ReceiptJournal) requireOriginal(original writer.ScanReceiptRequest, purpose writer.CommandID) error {
	id := original.Receipt.Identity
	name := fmt.Sprintf("%x-%d-%x-original.json", id.Incarnation, id.Sequence, id.OperationID)
	file, err := j.root.Open(name)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	expected, err := json.Marshal(journalEntry{Version: 1, Original: original, Purpose: purpose})
	if err != nil || len(data) > 64<<10 || !bytes.Equal(data, append(expected, '\n')) {
		return writer.ErrConflict
	}
	return nil
}
