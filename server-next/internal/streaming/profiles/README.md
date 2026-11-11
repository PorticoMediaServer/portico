# Device profiles

One file per device profile, and a case file beside each.

- `<name>.json` is a capability document (the one in `API.md`, "Device capability document")
  under `document`, with a `source` note that says where its facts came from: an app's
  table, general knowledge not yet confirmed on a device, or a capture from a named device on
  a date. Keep the note honest; a draft must not read as a measurement.
- `<name>.cases.json` lists the 25 kinds of file in the matrix
  (`declared_profiles_test.go`), in order, with how each is expected to reach that device:
  `route` (`original`, `copy_remux`, `audio_conversion`, `video_conversion` or `refused`),
  the `audio` codec sent, the `engine` chosen when the document has two players, and
  `channels`, `height` or `frameRate` where that matters. `note` says why, for the reader.

The server plans from two of these itself: `baseline.json`, for a device that has published
nothing it can read, and `airplay.json`. The rest are recorded copies of what the apps
send, held equal to the apps' tables by
`packages/client-core/test/declared-profile-golden.test.ts`.

## Adding a platform

1. Write its profile file. If its app exists, write its table in
   `packages/client-core/src/client-profile.ts`, add it to
   `packages/client-core/test/declared-profile-fixtures.ts`, and run that package's golden
   test once with `WRITE_PROFILES=1` to write the file.
2. Write its case file: 25 rows, what a person on that device should get.
3. Run `go test -run 'TestClientDeclaredTablesDriveDeliveryMatrix' ./internal/streaming`.

Nothing in planning looks at a platform's name, so there is no rule to add. A player the
platform carries besides its own (mpv) is a second entry under `engines` in the same
document.
