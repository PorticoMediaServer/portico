# Backups

What the server does, from `server-next/internal/backup`. Routes: `/v1/admin/backups`
(`server-next/API.md`, Console API).

## What a backup holds

One `.tar.gz` in the backup folder, with a small `.json` beside it that describes it. It holds:

- the database;
- the key files that open what the database keeps sealed;
- the pictures people uploaded, and downloaded subtitles;
- with `includeArtwork` in the `backups` settings, the pictures Portico could download again.

It never holds media, recordings, the cache, logs or conversions.

The database is copied a few pages at a time behind people's writes, so a backup runs while the
server is in use.

## When

Scheduled backups run at the owner's hour on the owner's days (3 am every night by default), and
the newest scheduled ones are kept (7 by default, 1 to 365). **Back up now** makes one that is
not counted and stays until the owner deletes it. Pausing background work pauses scheduled
backups too.

## Protection

With a passphrase set, backups are encrypted: bounded, authenticated records with a required
final record, so a damaged, reordered or cut-short file is refused. Unattended backups keep only
a salted key derived from the passphrase, never the passphrase itself. A new passphrase applies
to later backups only, and old ones still need their old phrase.

Each backup's description carries a mark made with a key that stays in this server's data folder
and is never in a backup. A backup without this server's mark, or changed since it was made, is
not refused: restoring onto a new machine after a loss is what backups are for. The owner is
warned and must confirm.

## Restore

1. **Check.** The archive is checked whole before anything changes. It may hold only a database,
   key files, pictures and subtitles under the names Portico gives them, and its database must
   pass a full check. A backup that fails any part is refused whole.
2. **Stage.** The backup is unpacked once into the data folder; the database that was checked is
   the file put in place. Nothing in use changes until the next start, which must come within an
   hour, or the staged restore is dropped.
3. **Swap.** At that start, before the database opens, the swap runs under a journal. The current
   database and the key files the backup replaces move into `restore-previous`, and the backup's
   move in. A stop at any point ends with either the backup in place or the original back. If the
   restored database will not open, the original is put back.
4. **Sign in again.** Every sign-in is ended once the restored database opens.

A restore takes the owner signed in (never a key), not on a TV, with their password or PIN again.
The previous state stays in `restore-previous` until the next restore.
