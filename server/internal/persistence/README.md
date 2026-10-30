# persistence

The state database is one SQLite file installed from one directly edited
pre-release baseline: `migrations/0001_baseline.sql`. There is no migration
runner, upgrade chain, automatic upgrade copy or conversion of old state.

Change the baseline directly and update all affected consumers together. Run
`python3 scripts/schema-sync.py` from the repository root to refresh its digest
and the known-object and known-column lists, then run persistence and affected
feature tests. A fresh install applies the entire baseline atomically and stores
its SHA256 digest under `schema_baseline_digest` in `configuration`. A matching database
reopens without applying schema DDL.

An existing database with a missing or different digest is refused, including
restore candidates, with this instruction:

> This database does not match this pre-release Portico baseline. Stop Portico,
> move aside or reset its state directory, then start Portico again.

Reset only disposable development state deliberately. Opening or inspecting a
mismatched database never wipes it, converts it, or creates an upgrade backup.
`schema_version` is a fixed diagnostic format marker for backup metadata; it is
not a numbered schema history. Exact baseline compatibility uses the digest.

## Kinds

`catalog_kinds` rows (see the baseline for flags):

| range  | use                                        |
|--------|--------------------------------------------|
| 1–13   | shipped kinds (movie … author)             |
| 14–49  | reserved for future media kinds (photo, …) |
| 50–89  | reserved for future containers and groups  |
| 90–99  | test fixtures only (never in a real install) |

Code asks the kind's flags (`playable`, `container`, `searchable`, `browsable`,
`listening`), never a list of kind numbers, unless it implements a feature that
belongs to one kind (movie categories, book series).

## Progress

Progress and history store a position with a unit (`0` milliseconds, `1`
pages, `2` fraction in millionths) and a generic `completed` flag, so a book
read by page and a photo album viewed by fraction need no new columns.

For external inspection, use a backup copy. An open transaction in another
program on the live database can delay writes and prevent WAL cleanup.
