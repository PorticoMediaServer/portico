package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// All original fields are typed and retained by UO's public receipt lifetime.
// No current asset/source FK is consulted on replay. This decoder grants no
// disclosure: UO must first reauthorize and classify the SAME public key/digest.
type mpReadingOriginal struct {
	Command, Version                              int64
	PublicID                                      [16]byte
	Asset                                         sql.NullInt64
	Token, Revision, Format, CFI, Section         sql.NullString
	Page                                          sql.NullInt64
	Progression                                   sql.NullFloat64
	ReadingRevision, PersonalRevision, ObservedMS int64
	Accepted, Stale, Cleared                      bool
	Reason                                        string
}

func (v mpReadingOriginal) validate() error {
	if (v.Command != 770 && v.Command != 771) || v.Version != 1 || v.PublicID == [16]byte{} || v.ReadingRevision < 0 || v.ReadingRevision > mpReadingMaxRevision || v.PersonalRevision < 0 || v.PersonalRevision > mpReadingMaxRevision || v.ObservedMS < 0 || v.ObservedMS > 9999999999999 || len(v.Reason) > 128 || !utf8.ValidString(v.Reason) || strings.ContainsRune(v.Reason, 0) || v.Accepted && v.Stale || v.Cleared && (v.Command != 771 || !v.Accepted) {
		return ErrResolveRequired
	}
	if !v.Asset.Valid {
		if v.Asset != (sql.NullInt64{}) || v.Token != (sql.NullString{}) || v.Revision != (sql.NullString{}) || v.Format != (sql.NullString{}) || v.CFI != (sql.NullString{}) || v.Section != (sql.NullString{}) || v.Page != (sql.NullInt64{}) || v.Progression != (sql.NullFloat64{}) {
			return ErrResolveRequired
		}
		return nil
	}
	if v.Asset.Int64 <= 0 || !v.Token.Valid || len(v.Token.String) == 0 || len(v.Token.String) > 512 || !utf8.ValidString(v.Token.String) || strings.ContainsRune(v.Token.String, 0) || !v.Revision.Valid || len(v.Revision.String) != 64 || strings.ToLower(v.Revision.String) != v.Revision.String || !v.Format.Valid || !v.Progression.Valid || v.Progression.Float64 < 0 || v.Progression.Float64 > 1 || v.Progression.Float64 != v.Progression.Float64 {
		return ErrResolveRequired
	}
	if _, err := hex.DecodeString(v.Revision.String); err != nil {
		return ErrResolveRequired
	}
	if v.Format.String == "epub" {
		if !v.CFI.Valid || len(v.CFI.String) == 0 || len(v.CFI.String) > 4096 || !utf8.ValidString(v.CFI.String) || v.Page != (sql.NullInt64{}) || v.Section.Valid && (len(v.Section.String) > 1024 || !utf8.ValidString(v.Section.String)) {
			return ErrResolveRequired
		}
	} else if v.Format.String == "pdf" || v.Format.String == "cbz" || v.Format.String == "cb7" || v.Format.String == "cbr" {
		if v.CFI != (sql.NullString{}) || v.Section != (sql.NullString{}) || !v.Page.Valid || v.Page.Int64 < 0 || v.Page.Int64 > 1000000 {
			return ErrResolveRequired
		}
	} else {
		return ErrResolveRequired
	}
	return nil
}
func readMPReadingOriginal(ctx context.Context, t *commandTurn, key string, kind CommandID) (out mpReadingOriginal, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if len(key) != 51 || !strings.HasPrefix(key, "p251_") || key[18] != '_' || (kind != 770 && kind != 771) {
		return out, ErrInvalid
	}
	for _, c := range key[5:18] {
		if c < '0' || c > '9' {
			return out, ErrInvalid
		}
	}
	if _, err = hex.DecodeString(key[19:]); err != nil || strings.ToLower(key) != key {
		return out, ErrInvalid
	}
	bytes, err := wc008DraftInteger(ctx, t, mpReadingResultBytes, key)
	if err != nil {
		return out, err
	}
	if bytes < 0 || bytes > 8192 {
		return out, ErrCap
	}
	row, err := wc008DraftQuery(ctx, t, mpReadingResultRead, key)
	if err != nil {
		return out, err
	}
	var public []byte
	if err = row.Scan(&out.Command, &out.Version, &public, &out.Asset, &out.Token, &out.Revision, &out.Format, &out.CFI, &out.Section, &out.Page, &out.Progression, &out.ReadingRevision, &out.PersonalRevision, &out.ObservedMS, &out.Accepted, &out.Stale, &out.Cleared, &out.Reason); err != nil {
		return out, err
	}
	if len(public) != 16 || out.Command != int64(kind) {
		return out, ErrResolveRequired
	}
	copy(out.PublicID[:], public)
	return out, out.validate()
}
