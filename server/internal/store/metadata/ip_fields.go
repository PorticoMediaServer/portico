// Package metadata prepares typed descriptive metadata changes for the private
// writer. Preparation neither authorizes an edit nor persists a field.
package metadata

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
)

var ErrField = errors.New("invalid descriptive field preparation")

// TextField deliberately excludes numeric, relationship, list and identity
// edits: those require their own typed validation and command families.
type TextField string

const (
	Title            TextField = "title"
	SortTitle        TextField = "sortTitle"
	OriginalTitle    TextField = "originalTitle"
	Description      TextField = "description"
	Edition          TextField = "edition"
	Tagline          TextField = "tagline"
	ContentRating    TextField = "contentRating"
	Studio           TextField = "studio"
	Network          TextField = "network"
	Country          TextField = "country"
	MetadataLanguage TextField = "metadataLanguage"
)

// Text distinguishes an absent accepted source from an explicit empty value.
// Snapshots must derive presence from actual source/origin facts; the old
// owner row's empty automatic_value alone does not establish source absence.
type Text struct {
	Value   string
	Present bool
}

// TextState is a copied same-field snapshot. Automatic is the selected local
// or provider source after source precedence has been resolved. A lock keeps
// Effective, including a locked empty string, while Automatic can advance.
type TextState struct {
	Effective, Automatic Text
	Locked               bool
}

type RefreshMode uint8

const (
	FillMissing RefreshMode = iota + 1
	ReplaceUnlocked
)

type EditAction uint8

const (
	SetText EditAction = iota + 1
	LockText
	UnlockText
	RestoreAutomatic
)

// LockOverride matches the editor's explicit optional lock choice. SetText
// locks by default; RestoreAutomatic unlocks by default. An explicit override
// is applied last. Unlock alone keeps the displayed value until a subsequent
// automatic publication, rather than silently performing RestoreAutomatic.
type LockOverride uint8

const (
	KeepDefaultLock LockOverride = iota
	ForceLocked
	ForceUnlocked
)

type TextEdit struct {
	Action EditAction
	Value  string // Only SetText carries a value; empty is an intentional clear.
	Lock   LockOverride
}

// TextChange contains the complete prospective state and exact change flags.
// A change of the automatic source under a lock is still a durable state
// change, even when the effective display text remains identical. The writer
// chooses revision/event effects under current authorization and source CAS.
type TextChange struct {
	State            TextState
	Changed          bool
	EffectiveChanged bool
}

// PrepareAutomaticText never treats an absent observation as a clear. A caller
// clearing a complete accepted field uses Text{Present:true, Value:""}.
// FillMissing preserves an accepted automatic value, including accepted empty;
// ReplaceUnlocked advances Automatic even under a lock. A fill can capture the
// first automatic source without replacing an existing effective owner value.
func PrepareAutomaticText(field TextField, current TextState, next Text, mode RefreshMode) (TextChange, error) {
	if (mode != FillMissing && mode != ReplaceUnlocked) || !validState(field, current) || !validSourceText(next) {
		return TextChange{}, ErrField
	}
	state := current
	if next.Present && !(mode == FillMissing && current.Automatic.Present) {
		state.Automatic = next
		if !state.Locked && (mode == ReplaceUnlocked || !state.Effective.Present) {
			state.Effective = next
		}
	}
	return textChange(current, state), nil
}

// PrepareTextEdit validates a single descriptive edit. Kind/field eligibility,
// actor authorization, expected revision, history and source provenance remain
// the writer's responsibility. It returns no digest, seal or authority token.
func PrepareTextEdit(field TextField, current TextState, edit TextEdit) (TextChange, error) {
	if !validState(field, current) || edit.Lock > ForceUnlocked ||
		(edit.Action != SetText && edit.Value != "") {
		return TextChange{}, ErrField
	}
	state := current
	switch edit.Action {
	case SetText:
		if !validEditText(field, edit.Value) {
			return TextChange{}, ErrField
		}
		state.Effective = Text{Value: edit.Value, Present: true}
		state.Locked = true
	case LockText:
		state.Locked = true
	case UnlockText:
		state.Locked = false
	case RestoreAutomatic:
		state.Effective, state.Locked = current.Automatic, false
	default:
		return TextChange{}, ErrField
	}
	switch edit.Lock {
	case ForceLocked:
		state.Locked = true
	case ForceUnlocked:
		state.Locked = false
	}
	if !validState(field, state) {
		return TextChange{}, ErrField
	}
	return textChange(current, state), nil
}

func textChange(before, after TextState) TextChange {
	// Strings can be short views of a much larger decoded provider body. Copy
	// both retained values even when a no-op returns the old source value.
	after.Effective.Value = strings.Clone(after.Effective.Value)
	after.Automatic.Value = strings.Clone(after.Automatic.Value)
	return TextChange{State: after, Changed: before != after, EffectiveChanged: before.Effective != after.Effective}
}

func validState(field TextField, state TextState) bool {
	return fieldLimit(field) > 0 && validSourceText(state.Effective) && validSourceText(state.Automatic) &&
		(!state.Locked || state.Effective.Present)
}

func fieldLimit(field TextField) int {
	switch field {
	case Title, SortTitle, OriginalTitle, Edition, Tagline, ContentRating, Studio, Network, Country, MetadataLanguage:
		return 300
	case Description:
		return 20000
	default:
		return 0
	}
}

// Already accepted source text is not subjected to the editor's smaller rune
// limits. Its caller must reserve actual source bytes before copying it here.
// The common evidence preparation envelope bounds one source header to 4MiB;
// this check is an additional preparation safety bound, not field truncation.
func validSourceText(text Text) bool {
	return (text.Present || text.Value == "") && len(text.Value) <= 4<<20 &&
		utf8.ValidString(text.Value) && strings.IndexByte(text.Value, 0) < 0
}

func validEditText(field TextField, value string) bool {
	limit := fieldLimit(field)
	if limit == 0 || len(value) > 4*limit || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(field == Description && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	if field == MetadataLanguage && value != "" {
		if _, err := language.Parse(value); err != nil {
			return false
		}
	}
	return true
}
