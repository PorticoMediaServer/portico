package writer

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
)

// Typed descriptive owner edit: a finite, ordered set of prepared field edits
// with ONE owner revision, ONE normalized history entry and ONE exact saved
// result. Before values and the current projection are ORIGINAL request inputs;
// replay never reloads them from a mutable head. This file performs pure
// preparation only: no admission, authority, SQL or COMMIT proof.
const (
	ipTextMaxFields = 12
	ipTextActorMax  = 4096

	// Author is a book-only descriptive field. It is deliberately outside
	// metadata.TextField's generic set because the accepted author value, its
	// reading identity key and its owner source must move as one fact.
	ipTextAuthorField fieldmeta.TextField = "author"
	ipTextAuthorMax                       = 300
)

// ipTextAuthorKey is the exact author identity key derivation of the merged
// dbwork.AuthorIdentityKey oracle. It is copied locally rather than importing
// dbwork into the writer: dbwork's init registers SQL functions and would
// change pragma_function_list for every plan capture in this test binary.
func ipTextAuthorKey(name string) string { return "author:" + strings.ToLower(name) }

type ipTextProjection struct {
	Title, SortTitle, Language                                   string
	SortKey, SortHead, SortKeyKeepArticles, SortHeadKeepArticles string
}

type ipTextEditField struct {
	Field  fieldmeta.TextField
	Before fieldmeta.TextState
	Edit   fieldmeta.TextEdit
}

type ipTextEditSource struct {
	EntityID, PersonID, LibraryID                    int64
	Kind, Actor, PersonToken, LibraryKey             string
	TargetKind                                       int64
	OwnerRevision, SubjectRevision, MetadataRevision int64
	SourceRevision, IdentityRevision                 int64
	MetadataIncarnation                              [16]byte
	Projection                                       ipTextProjection
}

type ipTextEditRequest struct {
	Kind   CommandID
	Source ipTextEditSource
	Fields []ipTextEditField
}

type ipTextEditCommand struct {
	identity          CommandIdentity
	request           ipTextEditRequest
	digest            RequestDigest
	after             []fieldmeta.TextChange
	projection        ipTextProjection
	projectionChanged bool
	changed           bool
	effective         bool
	valid             bool
}

type ipTextEditFieldResult struct {
	Field  fieldmeta.TextField
	Change fieldmeta.TextChange
}

type ipTextEditResult struct {
	OwnerID, EntityID, PersonID, Revision, SubjectRevision, AppliedMS int64
	History                                                           EvidenceInteger
	Fields                                                            []ipTextEditFieldResult
}

type ipTextEditSavedResult struct {
	Kind  CommandID
	Value ipTextEditResult
}

func ipTextEditTarget(s ipTextEditSource) int64 {
	if s.EntityID > 0 {
		return s.EntityID
	}
	return s.PersonID
}

func ipTextEditCloneProjection(p ipTextProjection) ipTextProjection {
	return ipTextProjection{
		Title: strings.Clone(p.Title), SortTitle: strings.Clone(p.SortTitle),
		Language: strings.Clone(p.Language), SortKey: strings.Clone(p.SortKey),
		SortHead: strings.Clone(p.SortHead), SortKeyKeepArticles: strings.Clone(p.SortKeyKeepArticles),
		SortHeadKeepArticles: strings.Clone(p.SortHeadKeepArticles),
	}
}

func ipTextEditKeyField(field fieldmeta.TextField) bool {
	return field == fieldmeta.Title || field == fieldmeta.SortTitle || field == fieldmeta.MetadataLanguage
}

func ipTextEditEntityKind(kind string) bool {
	switch kind {
	case "item", "show", "season", "album", "artist", "book", "collection":
		return true
	}
	return false
}

// ipTextEditFieldAllowed is the finite descriptive API. Edition and content
// rating change identity/admission projections, so they need a later typed
// body; author exists only on books; a person carries biography alone.
func ipTextEditFieldAllowed(kind string, field fieldmeta.TextField) bool {
	if kind == "person" {
		return field == fieldmeta.Description
	}
	if !ipTextEditEntityKind(kind) {
		return false
	}
	switch field {
	case fieldmeta.Title, fieldmeta.SortTitle, fieldmeta.OriginalTitle, fieldmeta.Description,
		fieldmeta.Tagline, fieldmeta.Studio, fieldmeta.Network, fieldmeta.Country, fieldmeta.MetadataLanguage:
		return true
	case ipTextAuthorField:
		return kind == "book"
	}
	return false
}

func ipTextEditText(value string, limit int) bool {
	if len(value) > 4*limit || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func ipTextEditValidSource(t fieldmeta.Text) bool {
	return (t.Present || t.Value == "") && len(t.Value) <= 4<<20 && utf8.ValidString(t.Value) && strings.IndexByte(t.Value, 0) < 0
}

// prepareIPTextField is the single typed preparation step. Author keeps the
// same absent/empty/locked-empty distinctions as every other descriptive field
// but adds its own bounded edit validation.
func prepareIPTextField(field fieldmeta.TextField, current fieldmeta.TextState, edit fieldmeta.TextEdit) (fieldmeta.TextChange, error) {
	if field != ipTextAuthorField {
		return fieldmeta.PrepareTextEdit(field, current, edit)
	}
	if !ipTextEditValidSource(current.Effective) || !ipTextEditValidSource(current.Automatic) ||
		(current.Locked && !current.Effective.Present) || edit.Action != fieldmeta.SetText || edit.Value != "" && !ipTextEditText(edit.Value, ipTextAuthorMax) ||
		edit.Lock > fieldmeta.ForceUnlocked {
		return fieldmeta.TextChange{}, fieldmeta.ErrField
	}
	state := current
	state.Effective = fieldmeta.Text{Value: strings.Clone(edit.Value), Present: true}
	state.Locked = true
	switch edit.Lock {
	case fieldmeta.ForceLocked:
		state.Locked = true
	case fieldmeta.ForceUnlocked:
		state.Locked = false
	}
	if state.Locked && !state.Effective.Present {
		return fieldmeta.TextChange{}, fieldmeta.ErrField
	}
	return fieldmeta.TextChange{State: state, Changed: current != state, EffectiveChanged: current.Effective != state.Effective}, nil
}

func ipTextEditIdentityValid(identity CommandIdentity) bool {
	return identity.Incarnation != [16]byte{} && identity.OperationID != [16]byte{} && identity.Sequence > 0
}

// newIPTextEditCommand freezes the complete original request. It runs under W
// preparation custody before any hold and performs no SQL.
func newIPTextEditCommand(identity CommandIdentity, in ipTextEditRequest) (*ipTextEditCommand, error) {
	if !ipTextEditIdentityValid(identity) || in.Kind != IPFieldEdit {
		return nil, ErrInvalid
	}
	s := in.Source
	if s.OwnerRevision <= 0 || s.OwnerRevision == math.MaxInt64 || (s.EntityID > 0) == (s.PersonID > 0) ||
		s.EntityID < 0 || s.PersonID < 0 || len(s.Actor) == 0 || len(s.Actor) > ipTextActorMax || !utf8.ValidString(s.Actor) ||
		len(in.Fields) == 0 || len(in.Fields) > ipTextMaxFields {
		return nil, ErrInvalid
	}
	if s.PersonID > 0 {
		if s.Kind != "person" || s.TargetKind != 0 || s.LibraryID != 0 || s.LibraryKey != "" || s.MetadataRevision != 0 || s.SourceRevision != 0 ||
			s.IdentityRevision != 0 || s.MetadataIncarnation != [16]byte{} || s.Projection != (ipTextProjection{}) ||
			len(s.PersonToken) == 0 || len(s.PersonToken) > 128 || !utf8.ValidString(s.PersonToken) {
			return nil, ErrInvalid
		}
	} else {
		if !ipTextEditEntityKind(s.Kind) || s.TargetKind <= 0 || ipEntryOwnerKind(s.TargetKind) != s.Kind ||
			s.LibraryID <= 0 || len(s.LibraryKey) == 0 || len(s.LibraryKey) > 128 || !utf8.ValidString(s.LibraryKey) ||
			s.PersonToken != "" ||
			s.SubjectRevision <= 0 || s.SubjectRevision == math.MaxInt64 ||
			s.MetadataRevision <= 0 || s.MetadataRevision == math.MaxInt64 ||
			s.SourceRevision <= 0 || s.SourceRevision == math.MaxInt64 ||
			s.IdentityRevision <= 0 || s.IdentityRevision == math.MaxInt64 ||
			s.MetadataIncarnation == [16]byte{} {
			return nil, ErrInvalid
		}
	}
	digest, err := canonicalRequestDigest(in.Kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	c := ipTextEditCommand{identity: identity, digest: digest}
	last := ""
	hasKeys := false
	p := s.Projection
	for _, f := range in.Fields {
		if string(f.Field) <= last || !ipTextEditFieldAllowed(s.Kind, f.Field) {
			return nil, ErrInvalid
		}
		last = string(f.Field)
		change, err := prepareIPTextField(f.Field, f.Before, f.Edit)
		if err != nil {
			return nil, ErrInvalid
		}
		if change.EffectiveChanged && ipTextEditKeyField(f.Field) {
			hasKeys = true
			c.projectionChanged = true
		}
		if ipTextEditKeyField(f.Field) {
			hasKeys = true
			before := p.Title
			switch f.Field {
			case fieldmeta.SortTitle:
				before = p.SortTitle
			case fieldmeta.MetadataLanguage:
				before = p.Language
			}
			if before != f.Before.Effective.Value {
				return nil, ErrInvalid
			}
			switch f.Field {
			case fieldmeta.Title:
				p.Title = change.State.Effective.Value
			case fieldmeta.SortTitle:
				p.SortTitle = change.State.Effective.Value
			case fieldmeta.MetadataLanguage:
				p.Language = change.State.Effective.Value
			}
		}
		c.after = append(c.after, change)
		c.changed = c.changed || change.Changed
		c.effective = c.effective || change.EffectiveChanged
	}
	if hasKeys {
		old, err := ProjectFilenameMetadata(s.Projection.Title, s.Projection.SortTitle, s.Projection.Language)
		if err != nil || old.SortKey != s.Projection.SortKey || old.SortHead != s.Projection.SortHead ||
			old.SortKeyKeepArticles != s.Projection.SortKeyKeepArticles ||
			old.SortHeadKeepArticles != s.Projection.SortHeadKeepArticles {
			return nil, ErrInvalid
		}
		next, err := ProjectFilenameMetadata(p.Title, p.SortTitle, p.Language)
		if err != nil {
			return nil, err
		}
		c.projection = ipTextProjection{next.Title, next.SortTitle, next.MetadataLanguage,
			next.SortKey, next.SortHead, next.SortKeyKeepArticles, next.SortHeadKeepArticles}
	} else if s.Projection != (ipTextProjection{}) {
		return nil, ErrInvalid
	}
	// Freeze every retained string. Even a no-op result keeps its own bytes.
	s.Kind = strings.Clone(s.Kind)
	s.Actor = strings.Clone(s.Actor)
	s.PersonToken = strings.Clone(s.PersonToken)
	s.LibraryKey = strings.Clone(s.LibraryKey)
	s.Projection = ipTextEditCloneProjection(s.Projection)
	in.Source = s
	in.Fields = append([]ipTextEditField(nil), in.Fields...)
	for j := range in.Fields {
		f := &in.Fields[j]
		f.Field = fieldmeta.TextField(strings.Clone(string(f.Field)))
		f.Before.Effective.Value = strings.Clone(f.Before.Effective.Value)
		f.Before.Automatic.Value = strings.Clone(f.Before.Automatic.Value)
		f.Edit.Value = strings.Clone(f.Edit.Value)
	}
	c.request = in
	c.valid = true
	return &c, nil
}

func ipTextEditResultEqual(a, b ipTextEditResult) bool {
	if a.OwnerID != b.OwnerID || a.EntityID != b.EntityID || a.PersonID != b.PersonID ||
		a.Revision != b.Revision || a.SubjectRevision != b.SubjectRevision || a.AppliedMS != b.AppliedMS ||
		a.History != b.History || len(a.Fields) != len(b.Fields) {
		return false
	}
	for j := range a.Fields {
		if a.Fields[j].Field != b.Fields[j].Field || a.Fields[j].Change != b.Fields[j].Change {
			return false
		}
	}
	return true
}
