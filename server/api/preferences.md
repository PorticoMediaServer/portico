# Viewer preferences

GET and PATCH `/v1/preferences` publish and update the server's canonical typed
registry. Clients use its defaults, domains and clamps. `null` removes only the
addressed override. A stored value equal to the default is still explicit;
`effectiveSource` reports that distinction. Empty language lists are explicit:
no preferred audio language is read as `[@original]`, and no subtitle language
keeps automatic subtitles off.

Overrides use normalized `preference_scopes`, `preference_values` and ordered
`preference_list_items` rows. The pre-release baseline is edited directly;
existing databases with a different baseline still require the established reset
procedure. No JSON preference-document fallback or dual-write is used.

`profile-server` applies to one authority/account/profile on this server.
`profile-device-class` separates web, mobile, tablet and television defaults.
`profile-device` uses only the authenticated session's device ID, never an ID in
the request. A device-bound response sets `deviceBound: true` and includes its
third scope document. Credentials without a device may read profile/kind values
and cannot write the device scope. The authority, account and profile tuple is
part of every stored key. Revision checks, authorization and refresh events are
inside the same write transaction, and idempotent replay emits no new event.

Audio language defaults to `[@appLanguage, @original]`, subtitles to
`[@appLanguage]`, and subtitle mode to `foreign`. Language entries otherwise use
canonical BCP-47 tags and reject duplicates. `@original` is audio-only and uses
the title's published catalogue language, supplied only on the server. A known
language selects a non-commentary match, or falls through to the next language
entry if no match exists. An unknown language uses the exact file default, then
the first non-commentary track. Exhausted lists use the default, then the first
track. Explicit and carried session choices keep precedence. App language resolves
from the request's `X-Portico-App-Language` (the catalogue
actually displayed after client fallback), then the explicit profile locale, then
the app's English catalogue fallback. The OS Accept-Language is not substituted
for a catalogue the app does not ship. Original never counts as one of “my languages”.
`foreign` chooses full subtitles for unfamiliar audio and forced lines otherwise;
`forced` chooses only forced lines. SDH preference, full fallback and the single
untagged-forced-track rule are explicit registry choices. Session track selections
remain session choices and are not overwritten on a subsequent plan read.

Activity sharing defaults to off. Pause history, recent searches and Watch
Together visibility keep their existing independent effects. Appearance motion
is one device value (`follow` or `reduce`); `appearance.reduceMotion` is retired.
Region has separate word and formatting locales, date order, first weekday and
binary/decimal size units. The device alone determines the time zone.

The exported client-core `preference-effects.ts` facade projects subtitle style,
audio selection, appearance, region and reader defaults. Actual renderer support
belongs to each platform; the facade does not claim unsupported OS capabilities.
Before Phase 8, the 15 Reading values are read and written only by Settings,
through `readingPreferences` and `readingPreferencesPatch`. They make no reader
effect claim. The pure patch helper validates only submitted facade properties
against the server's registry; `null` clears the override. Revision and transport
remain with `PreferencesReader`.

`/v1/listening/preferences` shares the profile revision and projects audiobook
speed and sleep timer. These and the shared revision are its only fields. Music
plays at recorded speed, and albums and audiobook parts continue independently
of the video next-episode setting. Neither behavior is a listening preference.
`music.defaultSpeed` is retired and is never persisted. Other music effects and
quality fields remain owned by the music registry producer.
