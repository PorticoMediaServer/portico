# server-next: the rules

This folder is Portico's server. Git history preserves the previous implementation. These rules are the whole
standard. A change that needs to break one says so in its pull request.

## The test for everything

For every table, route, mechanism, test, document and CI job, ask **"what would a user lose
without this?"**, never "is it safe to remove?". Prefer the boring, conventional design that a
contributor would recognise on first read. If in doubt, leave it out.

## Data

1. **One main database** (`main.db`, SQLite in WAL mode), plus two throwaway files that can be
   deleted and rebuilt at any time: `search.db` and `guide.db`. Artwork, transcodes and
   recordings are files on disk.
2. **One writer.** All writes go through one connection, with people's actions ahead of
   background work. Background work commits in small batches and always keeps a minimum share.
   Reads use a pool and never wait for the writer.
3. **One request is one transaction.** If it committed, it happened. Every commit to the main
   database is synced to disk before the reply is sent.
4. **Writes are safe to repeat.** `PUT`, `PATCH` and `DELETE` set a state, so sending one twice
   changes nothing. A `POST` that creates something takes an `Idempotency-Key`.
5. **Running state lives in memory**: playback sessions, streams, transcodes, watch-together
   rooms, undo offers. A table is for what a person would miss after a restart.
6. **No triggers. No table that is derived from other tables** unless a measured query needs
   it, and then it is rebuilt by a job and the server works without it.
7. **Constraints.** Every reference is a foreign key with an index. Every identity is unique.
   Required columns are `NOT NULL`. Value lists (`CHECK x IN (...)`) only on small tables; the
   large tables use integer codes checked in the one Go function that inserts into them, so a
   new media type is never a table rewrite.
8. **Only `internal/db` runs SQL.** No other package holds a transaction or a connection.
9. **Changing the schema.** Before the first release, edit the schema files in place and do
   nothing else: `upgrades` in `internal/db/upgrade.go` stays empty and a database from an
   earlier build is deleted. At the first release, the two schema files are saved, joined, as
   `internal/db/testdata/released/1.sql`. From then on every schema change is two edits in
   one pull request: the schema files, so a new database is made right, and one new step at
   the end of `upgrades`, so an existing one is brought to the same shape (a new column goes
   last in its table in both, and an index is spelled the same in both); a test fails when
   the two disagree.
   - A step's SQL must cost the same at any library size: a new table, a new column on a
     table that is not STRICT, a change to a small table. An index over existing columns of
     a large table may go there too when that is simpler; the accepted cost is one start
     after an upgrade about a second longer at a million titles.
   - Anything else that reads or writes every row of a large table (filling a column, a
     check) goes in the step's background part, in batches of about 2,000 rows. The code
     must work while that is half done, and from the step on every write must keep the new
     column right itself: the background part only fills what was there before.
   - A step may rely on every earlier step being finished, background part included: a
     start that has a newer step to apply finishes earlier background work first.
   - When a new column's empty value already means something, the half-done state is not
     safe to serve, and the step sets `beforeServing`: its background part is then finished
     while the server starts, at every start until it is done, still in batches and still
     carrying on after a kill. The worked example is `items.labels`, the labels that hide a
     title from a profile that has them blocked, where empty means "nothing hides this
     title". Had it arrived after the first release, its step would be: the column and its
     index in the quick part, the rebuild of every title's labels (2,000 titles to a batch)
     as the background part, and `beforeServing` set, because a title not yet filled would be
     shown to a profile it is hidden from. The price is a longer start, once, so it is only
     for a wrong answer a person could be harmed by; a column that is merely incomplete
     while it fills stays ordinary background work.
   - A released step is never edited, moved or removed. Nothing goes back a version.
     Rebuilding a table is not supported yet.
   - Before an upgrade the database is copied beside itself. One copy is kept, from before
     the latest upgrade; an older one is removed when a later upgrade's steps have all been
     applied, and an upgrade cut short keeps its copy and makes no second.
   - A backup or a restore is checked with `db.CheckSchema`, which compares tables by their
     columns, not their text, and accepts a database from an older version.

## API

10. The API is designed first, from the product definition. Reads answer or fail; they never
    say "preparing". Slow writes return a job. Starting playback may say "preparing".
11. One list route, one detail route, one state route. A new screen is not a reason for a new
    route, and a new feature is not a reason for a new table, until the design owner is asked.
12. One error format, opaque ids and cursors, times as RFC 3339 on the wire, durations and
    positions in whole milliseconds. `ETag` and `If-Match` only where two people or devices
    edit the same document (settings, metadata, shared lists, queues).

Feature PRs do not commit `packages/contracts/src/next/api.ts` or `server-next/api/openapi.json`; the merge desk regenerates them on main in one PR after each batch of merges.

## Code

13. The existing product is a reference, never a gate. Read it, judge it, then reimplement or
    copy code across on purpose, one package at a time. No test says "matches the old output".
14. Requirements are targets for normal use. Hard gates are for data integrity, security and
    crash safety.
15. Say the reason in a plain sentence. No ticket numbers or work-lane codes in comments or
    names, no version suffix on the only version of a thing, and none of the words the budget
    test refuses.

## Budgets (enforced by `budget_test.go`; only Justin raises one)

| Budget | Limit |
|---|---|
| Tables | 90 |
| Triggers | 0 |
| `CHECK` constraints | 250 |
| One source file | 1,000 lines |
| One line | 200 characters |
| Data-layer production code (`internal/db`) | 80,000 lines |
| Whole server production code | 400,000 lines |
| API operations | 450 |
