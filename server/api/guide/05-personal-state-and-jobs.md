# 5. Personal state, jobs and downloads

Watched state, ratings, favourites, My List and history belong to the viewer's
profile. Reads and writes use live visibility rules. A key/token needs
`playback` for personal writes and `read` for catalogue reads.

## Item and container state

Read the item's detail for its current personal state. Then send
`PUT /v1/items/{id}/personal-state`:

```json
{"operationId":"watched-20261013-1","expectedRevision":41,"watched":true}
```

Use the actual returned revision. Other intents include favourite, My List,
rating and progress. A conflict returns current state for reconciliation;
missing required revision is 428. Replay an identical body with the same
operation ID after a lost response.

`PUT /v1/items/personal-state:batch` supports up to 200 item intents, with one
operation ID. Each row has its own outcome. `GET` and `PUT
/v1/containers/{kind}/{id}/personal-state` cover shows, seasons, albums and books.
A container mark applies to visible members present at its watermark; later
additions remain unwatched. A new container mark supersedes older item intent and clears progress; later
explicit item intent can supersede it. Container
undo and progress reset have separate routes in the registry contract.

`GET /v1/personal-history` and `POST /v1/personal-history/actions` read and change
the viewer's own history. This does not erase the owner's play history.

## Bulk jobs

`POST /v1/jobs` accepts an operation ID, command, selector and arguments.
Selectors choose explicit items, a container or a bounded browse query.
Commands include personal state, playlist/collection additions, metadata edit
and refresh. Membership is captured in the background; `totalKnown` is false
until that step finishes. Poll `GET /v1/jobs/{id}` and page its `/failures`.
A 202 receipt means accepted work, not completed work. Use the per-operation
schema and returned outcomes rather than assuming a partial failure rolled back
all rows. Source: `internal/httpapi/bulk_jobs.go` and the bulk-job implementation in `internal/catalog/`.

## Downloads

Use the [download lifecycle reference](../downloads.md). Request-style offline
work begins at `POST /v1/downloads/requests`; preparations and transfer grants
are separate resources. Original is the default. A playable offline copy needs
its media, receipt, title metadata, artwork and subtitle companions retained
locally. These assets are transferred before the server can tidy its preparation.
Revalidate access and send reconciled progress on reconnect. Client storage and
platform scheduling are the client's responsibility. Downloads may outlive
server preparation cleanup; an existing device copy is not an instruction to
keep its server preparation forever.
