# 8. Routes for tools: the title query and "these paths changed"

Portico's screens are composed by the server. Two routes
exist for tools that are not screens.

## The title query

`GET /v1/titles (portico.openapi.json, operationId query_titles)` — scope
`read`. It lists the titles of **one kind** in **one library**, a page at a
time, or finds a title by its id at a metadata provider. The caller's library
access and content restrictions are applied inside the query, as everywhere
else: a page is never filtered after it is cut.

It answers only what an index can answer:

| Parameter | Values |
|---|---|
| `library` | A library id from `GET /v1/libraries (portico.openapi.json, operationId get_libraries)`. Required for a list. |
| `kind` | `movie`, `show`, `season`, `episode`, `artist`, `album`, `song`, `book`, `author`, `collection`. Required for a list. |
| `sort` | `title` (default), `added`, `year`. |
| `order` | `asc` (default), `desc`. |
| `yearFrom`, `yearTo` | Only with `sort=year`. |
| `addedAfter`, `addedBefore` | RFC 3339 times; only with `sort=added`. |
| `limit` | 1–100, default 50. |
| `cursor` | `nextCursor` from the previous page. |
| `provider`, `providerId` | A lookup instead of a list: `tmdb`, `tvdb`, `imdb` or `anilist`, and the id there. `library` and `kind` may narrow it. |

A range is accepted only on the key being sorted by; anything else, an unknown
parameter, or a repeated parameter is `400 invalid_request` naming the field.

```json
{
  "items": [
    {
      "id": "kq3Jm0Zr1yQ8b2nWcP4d5A",
      "kind": "movie",
      "title": "The Matrix",
      "libraryId": "movies",
      "year": 1999,
      "addedAt": "2026-10-01T18:22:04.000Z",
      "available": true,
      "durationMs": 8160000,
      "rating": 8.2,
      "posterUrl": "/v1/metadata/item/kq3Jm0Zr1yQ8b2nWcP4d5A/art/poster?v=3f9a&w=400",
      "providerIds": {"tmdb": "603", "imdb": "tt0133093"}
    }
  ],
  "limit": 50,
  "nextCursor": "eyJrZXlzIjpbInRoZSBtYXRyaXgiXX0.c2ln"
}
```

`available` is `false` while every file of the title is missing from disk.
Details, files and playback are the title's own routes (`GET
/v1/items/{id}/detail (portico.openapi.json, operationId getItemDetail)`).

**Walking a whole library.** Follow `nextCursor` until it is absent. The cursor
seeks to the last row you saw; it is not tied to the library's revision, so the
walk finishes even while a scan is adding titles. A title added behind the
cursor is not seen on this walk, one added ahead of it is. With unchanged sort keys, titles are not returned
twice. A concurrent edit can move a title across the cursor; reconcile your
index by stable title ID. A cursor belongs to the query and the caller that made it and lasts 30
minutes (`400 invalid_cursor` or `cursor_expired` if unusable). To pick up what changed since a walk,
ask for `sort=added&order=desc` and stop at the first title you already have.

"Do you have this film?" is one request:

```
GET /v1/titles?provider=tmdb&providerId=603
```

## These paths changed

`POST /v1/libraries/paths-changed (portico.openapi.json, operationId
report_changed_paths)` — scope `library`, owner's key. It is the call a
download manager makes after it imports, upgrades or deletes a file, in place
of asking for a scan of the whole library.

```json
{
  "paths": [
    {"path": "/media/tv/Severance/Season 02/Severance - S02E01.mkv", "change": "added"},
    {"path": "/media/films/Old Film (1999)", "change": "removed"}
  ]
}
```

- `path` is absolute and written **the way the Portico server sees it**. If the
  tool runs in another container or on another machine, map its paths to the
  server's first.
- `change` is optional (`added`, `changed`, `removed`). The scan looks at the
  folder either way.
- Up to 200 paths per request.

The answer (`202`) says what will happen for each path:

```json
{
  "paths": [
    {"path": "/media/tv/Severance/Season 02/Severance - S02E01.mkv", "status": "queued", "libraryId": "tv", "sourceId": "tv", "folder": "Severance/Season 02"},
    {"path": "/media/films/Old Film (1999)", "status": "queued", "libraryId": "films", "sourceId": "films", "folder": "Old Film (1999)"}
  ],
  "jobs": [
    {"id": "9gk2", "sourceId": "tv", "status": "queued", "phase": "inventory", "processed": 0, "analyzed": 0, "warnings": 0, "scanMode": "incremental"}
  ]
}
```

| `status` | Meaning |
|---|---|
| `queued` | The path is inside a library folder; `folder` is the folder that will be scanned, relative to it (`.` is the library folder itself). |
| `outside_libraries` | No library folder contains this path. Nothing is scanned. Check the path mapping. |
| `invalid` | Not an absolute path, or too long. |

What the scan does:

- It lists `folder` and everything under it, and nothing else. A path Portico
  has not seen before is scanned from the nearest folder above it that Portico
  already knows, which is where a new folder or file is found.
- A reported folder that has been deleted can no longer be listed, so the scan
  looks from the folder above it, where it can see that the folder is gone.
- New and changed files are matched as in any scan. A file that is gone from a
  scanned folder is marked missing, exactly as a full scan would mark it; its
  title stays.
- Nothing outside the reported folders is judged, so a file missing elsewhere is
  not noticed by this scan.
- If the library folder is already being scanned, the report is kept and
  scanned when that scan ends; `jobs` then names the scan in progress. Reports
  for the same folder made twice are one report.

Follow progress with `GET /v1/admin/ingestion/jobs/{id} (portico.openapi.json,
operationId getIngestionJob)`, or subscribe to the `scan.finished` webhook
(`09-webhooks.md`).

### Sonarr and Radarr

Use Settings → Connect → Custom Script on the application host. Portico does
not parse Sonarr/Radarr's own webhook payload. Configure the script for import,
upgrade and deletion events available in your installed version. Use a separate
Portico owner API key with only `library`. Match mount paths between the tools
and Portico, or translate paths before sending them.

Save the following as an executable shell script (requires `curl` and `jq`):

```sh
#!/bin/sh
set -eu
# Supply these through the process environment or a private sourced file.
: "${PORTICO:?Set the reachable Portico origin}"
: "${PORTICO_KEY:?Set a Portico key with library scope}"
if [ "${sonarr_eventtype:-${radarr_eventtype:-}}" = Test ]; then
  exit 0
fi
path=${sonarr_series_path:-${radarr_movie_path:-}}
[ -n "$path" ] || { echo 'No imported series or movie path supplied' >&2; exit 1; }
# Map $path here if the two applications see different mount paths.
jq -nc --arg path "$path" '{paths:[{path:$path}]}' |
  curl -fsS "$PORTICO/v1/libraries/paths-changed" \
    -H "Authorization: Bearer $PORTICO_KEY" -H 'Content-Type: application/json' \
    --data-binary @-
```

Sonarr supplies `sonarr_series_path`; Radarr supplies `radarr_movie_path`.
Reporting the containing series/movie folder also catches upgrades and removals,
without trusting a deleted file still exists. JSON encoding with `jq --arg`
handles quotes and backslashes in names. Check the 202 body's per-path status;
a successful HTTP response with `outside_libraries` did not schedule that path.
A Test connection checks that the script can run; it intentionally does not scan.
Use a real import/delete to verify the mapped path is queued, then check scan
progress or a `scan.finished` webhook. These are examples of the Custom Script
environment; event availability depends on the installed Sonarr/Radarr version.

The upstream [Sonarr Custom Script source](https://github.com/Sonarr/Sonarr/blob/develop/src/NzbDrone.Core/Notifications/CustomScript/CustomScript.cs)
and [Radarr Custom Script source](https://github.com/Radarr/Radarr/blob/develop/src/NzbDrone.Core/Notifications/CustomScript/CustomScript.cs)
define the series/movie path and Test event variables. Check the documentation
for your installed version when selecting triggers.

Give the key the `library` scope only.

## Request examples

Create a movie library with the owner's default metadata provider:

<!-- example: create-library file=portico.openapi.json operationId=post_libraries direction=request schema=@request /v1/libraries post -->
```json
{"name":"Movies","kind":"movie","path":"/media/movies"}
```

Report a changed file without starting a scan of every library:

<!-- example: paths-changed file=portico.openapi.json operationId=report_changed_paths direction=request schema=@request /v1/libraries/paths-changed post -->
```json
{"paths":[{"path":"/media/movies/Arrival (2016)/Arrival.mkv","change":"changed"}]}
```
