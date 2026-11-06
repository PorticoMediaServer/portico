package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const nativeInventoryOperation = "inventory-page-native-v1"

type nativeRequestMessage struct {
	Operation       string               `json:"operation"`
	NativeInventory *nativeInventoryWire `json:"nativeInventory"`
}

func marshalNativeRequest(w *nativeInventoryWire) ([]byte, error) {
	return json.Marshal(nativeRequestMessage{nativeInventoryOperation, w})
}

type nativeShape uint8

const (
	nativeEnvelopeShape nativeShape = iota
	nativeRequestShape
	nativePageShape
	nativeEntryShape
	nativeIssueShape
)

func nativeKnownKey(shape nativeShape, key string) bool {
	switch shape {
	case nativeEnvelopeShape:
		return key == "operation" || key == "nativeInventory"
	case nativeRequestShape:
		switch key {
		case "Root", "RelativePath", "MountedRoot", "RootIdentity", "DirectoryRevision", "Cursor", "Limit", "FollowSymlinks":
			return true
		}
	case nativePageShape:
		switch key {
		case "Version", "ErrorCode", "RootIdentity", "DirectoryIdentity", "DirectoryRevision", "Entries", "Issues", "NextCursor", "Complete", "Unstable":
			return true
		}
	case nativeEntryShape:
		switch key {
		case "Path", "Name", "ObjectIdentity", "ChangeToken", "Size", "ModifiedNS", "Directory":
			return true
		}
	case nativeIssueShape:
		switch key {
		case "Path", "Reason", "Line", "Protect", "Warning":
			return true
		}
	}
	return false
}
func nativeObjectKeys(d *json.Decoder, shape nativeShape) error {
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrNativeInventory
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return ErrNativeInventory
		}
		key, ok := token.(string)
		if !ok || seen[key] || !nativeKnownKey(shape, key) {
			return ErrNativeInventory
		}
		seen[key] = true
		switch {
		case shape == nativeEnvelopeShape && key == "nativeInventory":
			err = nativeObjectKeys(d, nativeRequestShape)
		case shape == nativePageShape && key == "Entries":
			err = nativeObjectArray(d, nativeEntryShape, 128)
		case shape == nativePageShape && key == "Issues":
			err = nativeObjectArray(d, nativeIssueShape, 512)
		default:
			var value json.RawMessage
			err = d.Decode(&value)
		}
		if err != nil {
			return errors.Join(ErrNativeInventory, err)
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return ErrNativeInventory
	}
	return nil
}
func nativeObjectArray(d *json.Decoder, shape nativeShape, limit int) error {
	token, err := d.Token()
	if err != nil {
		return ErrNativeInventory
	}
	if token == nil {
		return nil
	} // generated empty cohorts use JSON null
	if token != json.Delim('[') {
		return ErrNativeInventory
	}
	count := 0
	for d.More() {
		count++
		if count > limit {
			return ErrNativeInventory
		}
		if err := nativeObjectKeys(d, shape); err != nil {
			return err
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim(']') {
		return ErrNativeInventory
	}
	return nil
}

// Detect a native envelope BEFORE encoding/json can repair text or overwrite
// duplicate fields. Recognize ANY native discriminator, including duplicates
// and case aliases, so a later legacy discriminator cannot hide a native frame.
func nativeEnvelopeCandidate(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok {
			return false
		}
		if strings.EqualFold(key, "nativeInventory") {
			return true
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return false
		}
		if strings.EqualFold(key, "operation") {
			var op string
			if json.Unmarshal(value, &op) == nil && op == nativeInventoryOperation {
				return true
			}
		}
	}
	return false
}
func decodeNativeEnvelope(raw []byte) (*nativeInventoryWire, bool, error) {
	if !nativeEnvelopeCandidate(raw) {
		return nil, false, nil
	}
	var message nativeRequestMessage
	if err := nativeDecode(raw, &message, nativeEnvelopeShape); err != nil {
		return nil, true, err
	}
	if message.Operation != nativeInventoryOperation || message.NativeInventory == nil {
		return nil, true, ErrNativeInventory
	}
	return message.NativeInventory, true, nil
}
func nativeKeyFrame(raw []byte, shape nativeShape) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := nativeObjectKeys(d, shape); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrNativeInventory
	}
	return nil
}
