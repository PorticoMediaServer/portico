# Building a metadata editor

The server publishes everything a metadata editor needs. A third-party client
does not need to know which fields exist for a movie, which artwork roles a show
publishes, or which provider matches an album: it reads one document and renders
from it.

All of these routes require server owner authority and answer `401` otherwise.
Nothing here is cached: responses carry `Cache-Control: no-store`.

## 1. Read the state

```
GET /v1/metadata/{kind}/{id}
```

`kind` is one of `item`, `show`, `season`, `album`, `artist`, `book`. The
response carries, in one read:

- `schema` — the editable field registry for this target. Each entry gives the
  `field` name, a `label`, a `group` (`general`, `numbers`, `text`, `identity`),
  a `type` (`text`, `multiline`, `integer`, `number`, `date`, `enum`, `list`),
  the bounds the server enforces (`maxLength`, `min`, `max`, `allowed`), and
  whether the field can be edited in bulk. Build the form from this list. It
  differs per kind, and for items it differs per item kind: a movie has
  `edition` and `studio`, an episode has `seasonNumber` and `network`, a song has
  `trackNumber`.
- `snapshot.fields` — for each field: the published `value`, the
  `automaticValue` a provider or the scanner last supplied, whether it is
  `locked`, and its `source` (`manual`, `provider:<name>`, `scanner`, or
  `automatic` when the field is empty). A `list` field also carries `values`,
  the parsed array; `value` is the same array as canonical JSON text.
- `canMakeEpisodeThumbnail` — optional, true only when the current runtime and the exact episode video source support explicit picture generation. Absence is unavailable; this fact grants no authority and the command still checks owner, source and revision.
- `artworkRoles` — the roles this kind publishes, for the artwork tab and for
  uploads.
- `artwork.candidates` / `snapshot.artwork` — the images on offer and the ones
  currently selected. A candidate's `previewUrl`, when present, is a path this
  server serves; provider URLs are never published.
- `candidates` — identity matches, with `title`, `subtitle`, `year`, `provider`
  and, where the image has already been fetched, a proxied `previewUrl`.
- `revision` — the value fence. Keep it; every write needs it.

## 2. Edit

```
POST /v1/metadata/{kind}/{id}
{"expectedRevision": "…", "action": "edit", "fields": {…}}
```

Each entry in `fields` is one of:

- `{"value": "PG-13"}` — set the value and lock the field.
- `{"values": ["Noir", "Rewatch"]}` — set a `list` field and lock it.
- `{"value": ""}` — clear the field. An empty number clears it; it never becomes
  zero.
- `{"useAutomatic": true}` — restore the provider or scanner value and release
  the lock.
- `{"locked": false}` — change only the lock, keeping the current value.

A locked field survives a refresh: when a provider or the scanner later rewrites
it, the server records their value as `automaticValue` and puts the owner's value
back. That is why `useAutomatic` always has something to restore.

Genres and people are relationships, not fields: use `action: edit_relationships`
with `role: "genre"` or `role: "credit"`.

To search for a different match:

```
POST /v1/metadata/{kind}/{id}
{"expectedRevision": "…", "action": "search", "provider": "tmdb",
 "query": "The Thin Man", "year": 1934}
```

`query` and `year` override the scanned title and year for a screen-engine search only.
For non-screen engines, omit both fields; nonempty query or nonzero year is refused.
MusicBrainz search/retry uses existing scanned metadata rather than an owner-supplied query.
`provider` picks the engine where more than one applies: movies and episodes use
the screen engine's enabled providers, shows use their library’s selected screen provider,
albums and songs use musicbrainz, and artists and books have no remote matcher
and refuse `search`.

## 3. Handle conflicts

Every write carries `expectedRevision`. If another owner, a scan or a
publication changed the entity since the read, the write is refused:

```
409 {"error": {"code": "metadata_conflict", "retryable": false}}
```

Nothing was written. Re-read the state and retry quietly when the intended
values have not changed; ask the owner to review a real competing edit. Candidate
progress has a separate revision from editable values, so a new candidate alone
should not interrupt a value edit.

On success the response is the full state after the change, including a new
`revision`. Use it directly; there is no need for a second read.

## 4. Bulk edit

```
POST /v1/metadata/bulk
```

Up to 200 targets, all of one kind, each with its own `expectedRevision`. Only
fields whose registry entry says `"bulk": true` are accepted. Lists are
add/remove, never replace, so items keep the tags they do not share. `genres`
takes the same add/remove shape. `lockEdited` defaults to `true`.

Each target is applied in its own transaction, so the response always reports
every target and a partial failure is a `200`:

```json
{"operationId": "…", "updated": 2, "failed": 1,
 "results": [
   {"kind": "item", "id": "a", "ok": true, "revision": "…"},
   {"kind": "item", "id": "b", "ok": false, "code": "metadata_conflict",
    "message": "Metadata changed. Reload and review your decision."},
   {"kind": "item", "id": "c", "ok": true, "revision": "…"}]}
```

Show the failures; the applied targets are applied. Replaying the same
`operationId` returns the stored receipt rather than editing anything again, so a
retry after a dropped connection is safe. A different body under the same
`operationId` is refused.

## 5. Artwork

```
POST   /v1/metadata/{kind}/{id}/art/{role}/upload
DELETE /v1/metadata/{kind}/{id}/art/{role}/upload/{candidateId}?expectedRevision=…
```

`multipart/form-data`, image in `file`, `expectedRevision` as a form field, at
most 10 MiB, JPEG, PNG or WebP determined from the bytes rather than the declared
type. `role` must be one of the state's `artworkRoles`. The upload is stored in
the server's own artwork store, becomes a candidate with provider `upload`, and
is selected and locked for that role. Anything else that was being fetched for
that role is stalled so it cannot overwrite it. `415 unsupported_artwork` means
the bytes were not an accepted image or were too large.

Withdrawing an upload falls back to the best remaining candidate for that role,
or clears the role when there is none. Only `upload` candidates can be withdrawn.

Provider candidates are selected, not uploaded: `action: select_artwork` with the
candidate id, and `action: preview_artwork` to have the server fetch a preview
first.
