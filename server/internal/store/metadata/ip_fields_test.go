package metadata

import (
	"strings"
	"testing"
)

func present(value string) Text { return Text{Value: value, Present: true} }

func TestIPLockedEmptyTracksAutomaticAndRestoresIt(t *testing.T) {
	current := TextState{Effective: present("owner"), Automatic: present("old"), Locked: true}
	clear, err := PrepareTextEdit(Title, current, TextEdit{Action: SetText, Value: ""})
	if err != nil || !clear.Changed || !clear.EffectiveChanged || clear.State.Effective != present("") || !clear.State.Locked {
		t.Fatalf("clear: %+v, %v", clear, err)
	}
	refresh, err := PrepareAutomaticText(Title, clear.State, present("new"), ReplaceUnlocked)
	if err != nil || !refresh.Changed || refresh.EffectiveChanged || refresh.State.Effective != present("") || refresh.State.Automatic != present("new") || !refresh.State.Locked {
		t.Fatalf("locked refresh: %+v, %v", refresh, err)
	}
	restore, err := PrepareTextEdit(Title, refresh.State, TextEdit{Action: RestoreAutomatic})
	if err != nil || restore.State.Effective != present("new") || restore.State.Locked || !restore.EffectiveChanged {
		t.Fatalf("restore: %+v, %v", restore, err)
	}
}

func TestIPAutomaticPresenceAndFillMissing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current TextState
		next    Text
		mode    RefreshMode
		want    TextState
	}{
		{"absent-observation-keeps", TextState{Effective: present("old"), Automatic: present("old")}, Text{}, ReplaceUnlocked, TextState{Effective: present("old"), Automatic: present("old")}},
		{"explicit-clear", TextState{Effective: present("old"), Automatic: present("old")}, present(""), ReplaceUnlocked, TextState{Effective: present(""), Automatic: present("")}},
		{"fill-accepted-empty", TextState{Effective: present(""), Automatic: present("")}, present("new"), FillMissing, TextState{Effective: present(""), Automatic: present("")}},
		{"fill-absent", TextState{}, present("new"), FillMissing, TextState{Effective: present("new"), Automatic: present("new")}},
		{"fill-owner-keeps-display", TextState{Effective: present("owner")}, present("new"), FillMissing, TextState{Effective: present("owner"), Automatic: present("new")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PrepareAutomaticText(Description, tc.current, tc.next, tc.mode)
			if err != nil || got.State != tc.want || got.Changed != (tc.current != tc.want) || got.EffectiveChanged != (tc.current.Effective != tc.want.Effective) {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestIPTextEditLockActionsAndNoop(t *testing.T) {
	current := TextState{Effective: present("owner"), Automatic: present("automatic"), Locked: true}
	unlock, err := PrepareTextEdit(Title, current, TextEdit{Action: UnlockText})
	if err != nil || unlock.State.Locked || unlock.State.Effective != current.Effective || unlock.EffectiveChanged {
		t.Fatalf("unlock must keep effective text: %+v, %v", unlock, err)
	}
	locked, err := PrepareTextEdit(Title, unlock.State, TextEdit{Action: LockText})
	if err != nil || locked.State != current || locked.EffectiveChanged {
		t.Fatalf("lock: %+v, %v", locked, err)
	}
	noop, err := PrepareTextEdit(Title, current, TextEdit{Action: SetText, Value: "owner"})
	if err != nil || noop.Changed || noop.EffectiveChanged {
		t.Fatalf("identical edit: %+v, %v", noop, err)
	}
	forced, err := PrepareTextEdit(Title, current, TextEdit{Action: SetText, Value: "new", Lock: ForceUnlocked})
	if err != nil || forced.State.Locked || forced.State.Effective != present("new") || forced.State.Automatic != current.Automatic {
		t.Fatalf("explicit override: %+v, %v", forced, err)
	}
}

func TestIPTextValidationRefusesWithoutPartialOutput(t *testing.T) {
	current := TextState{Effective: present("old"), Automatic: present("source")}
	for _, tc := range []struct {
		field TextField
		state TextState
		edit  TextEdit
	}{
		{"provider_id", current, TextEdit{Action: SetText, Value: "new"}},
		{Title, current, TextEdit{Action: SetText, Value: "a\x00b"}},
		{Title, current, TextEdit{Action: SetText, Value: "a\nb"}},
		{Title, current, TextEdit{Action: SetText, Value: "\xff"}},
		{Title, current, TextEdit{Action: SetText, Value: strings.Repeat("é", 301)}},
		{MetadataLanguage, current, TextEdit{Action: SetText, Value: "!!!"}},
		{Title, current, TextEdit{Action: UnlockText, Value: "hidden"}},
		{Title, current, TextEdit{Action: SetText, Lock: 100}},
		{Title, current, TextEdit{}},
		{Title, TextState{}, TextEdit{Action: LockText}},
		{Title, TextState{Automatic: Text{Value: "not-present"}}, TextEdit{Action: SetText}},
	} {
		got, err := PrepareTextEdit(tc.field, tc.state, tc.edit)
		if err != ErrField || got != (TextChange{}) {
			t.Fatalf("accepted invalid edit for %s: %+v, %v", tc.field, got, err)
		}
	}
	if got, err := PrepareAutomaticText(Title, current, present("new"), 0); err != ErrField || got != (TextChange{}) {
		t.Fatalf("unknown refresh: %+v, %v", got, err)
	}
	if got, err := PrepareAutomaticText(Title, current, Text{Value: "hidden"}, ReplaceUnlocked); err != ErrField || got != (TextChange{}) {
		t.Fatalf("absent observation with bytes: %+v, %v", got, err)
	}
}

func TestIPTextRuneBoundsDoNotShrinkAcceptedAutomaticText(t *testing.T) {
	value := strings.Repeat("界", 300)
	if got, err := PrepareTextEdit(Title, TextState{}, TextEdit{Action: SetText, Value: value}); err != nil || got.State.Effective.Value != value {
		t.Fatalf("300 runes: %+v, %v", got, err)
	}
	large := present(strings.Repeat("source", 4000))
	got, err := PrepareAutomaticText(Description, TextState{}, large, ReplaceUnlocked)
	if err != nil || got.State.Automatic != large || got.State.Effective != large {
		t.Fatalf("accepted source must not use editor bound: %v", err)
	}
	if _, err := PrepareTextEdit(Description, got.State, TextEdit{Action: LockText}); err != nil {
		t.Fatalf("locking existing large source: %v", err)
	}
	if got, err := PrepareTextEdit(Description, TextState{}, TextEdit{Action: SetText, Value: "line one\nline two\tend"}); err != nil || !got.Changed {
		t.Fatalf("multiline description: %+v, %v", got, err)
	}
}
