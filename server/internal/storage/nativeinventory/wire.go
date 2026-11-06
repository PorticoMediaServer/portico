package nativeinventory

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// nativeBytes admits one canonical StdEncoding JSON STRING, never arrays,
// null, nonzero pad bits, embedded CR/LF or replacement-decoded JSON text.
type nativeBytes []byte

func (b *nativeBytes) UnmarshalJSON(raw []byte) error {
	if len(raw) < 2 || raw[0] != '"' || !nativeJSONText(raw) {
		return ErrNativeInventory
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	if base64.StdEncoding.DecodedLen(len(text)) > naming.MaxPathBytes+2 {
		return ErrNativeInventory
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil || len(decoded) > naming.MaxPathBytes || base64.StdEncoding.EncodeToString(decoded) != text {
		return ErrNativeInventory
	}
	*b = decoded
	return nil
}

// encoding/json repairs invalid UTF8 and lone UTF16 surrogates. Check all raw
// native JSON text BEFORE decoding, while allowing genuine valid U+FFFD text.
func nativeJSONText(raw []byte) bool {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	quoted := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		value := nativeHex4(raw[i+2 : i+6])
		if value >= 0xd800 && value <= 0xdbff {
			if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
				return false
			}
			low := nativeHex4(raw[i+8 : i+12])
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 11
		} else {
			if value >= 0xdc00 && value <= 0xdfff {
				return false
			}
			i += 5
		}
	}
	return true
}
func nativeHex4(raw []byte) uint16 {
	var value uint16
	for _, b := range raw {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value += uint16(b - '0')
		case b >= 'a' && b <= 'f':
			value += uint16(b - 'a' + 10)
		default:
			value += uint16(b - 'A' + 10)
		}
	}
	return value
}
func nativeDecode(raw []byte, out any, shape nativeShape) error {
	if !nativeJSONText(raw) {
		return ErrNativeInventory
	}
	if err := nativeKeyFrame(raw, shape); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrNativeInventory
	}
	return nil
}

func (w *Wire) UnmarshalJSON(raw []byte) error {
	var d struct {
		Root, RelativePath                      nativeBytes
		MountedRoot                             json.RawMessage
		RootIdentity, DirectoryRevision, Cursor string
		Limit                                   int
		FollowSymlinks                          bool
	}
	if err := nativeDecode(raw, &d, nativeRequestShape); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	var mount nativeBytes
	if len(d.MountedRoot) > 0 {
		if err := json.Unmarshal(d.MountedRoot, &mount); err != nil {
			return err
		}
	}
	request := NativeInventoryRequest{Root: []byte(d.Root), RelativePath: []byte(d.RelativePath), RootIdentity: d.RootIdentity,
		DirectoryRevision: d.DirectoryRevision, Cursor: d.Cursor, Limit: d.Limit, FollowSymlinks: d.FollowSymlinks}
	if !validNativeRequest(request) || bytes.IndexByte(mount, 0) >= 0 {
		return ErrNativeInventory
	}
	*w = Wire{NativeInventoryRequest: request, MountedRoot: []byte(mount)}
	return nil
}

func (e *NativeInventoryEntry) UnmarshalJSON(raw []byte) error {
	type alias NativeInventoryEntry
	var d struct {
		alias
		Path, Name nativeBytes
	}
	if err := nativeDecode(raw, &d, nativeEntryShape); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	value := NativeInventoryEntry(d.alias)
	value.Path = []byte(d.Path)
	value.Name = []byte(d.Name)
	if !nativeRequiredBytes(value.Path) || !nativeRequiredBytes(value.Name) {
		return ErrNativeInventory
	}
	*e = value
	return nil
}
func (i *NativeInventoryIssue) UnmarshalJSON(raw []byte) error {
	type alias NativeInventoryIssue
	var d struct {
		alias
		Path, Reason nativeBytes
	}
	if err := nativeDecode(raw, &d, nativeIssueShape); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	value := NativeInventoryIssue(d.alias)
	value.Path = []byte(d.Path)
	value.Reason = []byte(d.Reason)
	if !nativeRequiredBytes(value.Path) || !nativeRequiredBytes(value.Reason) || len(value.Reason) > 1024 {
		return ErrNativeInventory
	}
	*i = value
	return nil
}
func (p *NativeInventoryPage) UnmarshalJSON(raw []byte) error {
	type alias NativeInventoryPage
	var value alias
	if err := nativeDecode(raw, &value, nativePageShape); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	if err := nativePageText(NativeInventoryPage(value)); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	*p = NativeInventoryPage(value)
	return nil
}
func nativeRequiredBytes(raw []byte) bool {
	return len(raw) > 0 && len(raw) <= naming.MaxPathBytes && bytes.IndexByte(raw, 0) < 0
}

func nativePageText(p NativeInventoryPage) error {
	if p.Version != 1 || len(p.Entries) > 128 || len(p.Issues) > 512 {
		return ErrNativeInventory
	}
	for _, v := range []string{p.ErrorCode, p.RootIdentity, p.DirectoryIdentity, p.DirectoryRevision, p.NextCursor} {
		if !utf8.ValidString(v) || len(v) > 4096 || bytes.IndexByte([]byte(v), 0) >= 0 {
			return ErrNativeInventory
		}
	}
	for _, e := range p.Entries {
		if !nativeRequiredBytes(e.Path) || !nativeRequiredBytes(e.Name) || e.Size < 0 {
			return ErrNativeInventory
		}
		for _, v := range []string{e.ObjectIdentity, e.ChangeToken} {
			if !utf8.ValidString(v) || len(v) > 4096 || bytes.IndexByte([]byte(v), 0) >= 0 {
				return ErrNativeInventory
			}
		}
		if e.ObjectIdentity == "" {
			return ErrNativeInventory
		}
	}
	for _, i := range p.Issues {
		if !nativeRequiredBytes(i.Path) || !nativeRequiredBytes(i.Reason) || len(i.Reason) > 1024 {
			return ErrNativeInventory
		}
	}
	return nil
}
func validateNativePage(p NativeInventoryPage, r NativeInventoryRequest) error {
	if err := nativePageText(p); err != nil {
		return errors.Join(ErrNativeInventory, err)
	}
	if len(p.Entries) > r.Limit {
		return ErrNativeInventory
	}
	if p.ErrorCode != "" {
		switch p.ErrorCode {
		case "not_found", "changed", "unreadable":
		default:
			return ErrNativeInventory
		}
		if len(p.Entries) > 0 || len(p.Issues) > 0 || p.Complete || p.Unstable || p.NextCursor != "" {
			return ErrNativeInventory
		}
		return nil
	}
	if p.RootIdentity == "" || p.DirectoryIdentity == "" || p.DirectoryRevision == "" || p.Complete == (p.NextCursor != "") || !p.Complete && p.NextCursor == r.Cursor {
		return ErrNativeInventory
	}
	for _, e := range p.Entries {
		if bytes.Equal(e.Name, []byte(".")) || bytes.Equal(e.Name, []byte("..")) || bytes.ContainsAny(e.Name, "/\\\x00") ||
			!bytes.Equal(e.Path, []byte(filepath.Join(string(r.Root), string(r.RelativePath), string(e.Name)))) {
			return ErrNativeInventory
		}
	}
	return nil
}
