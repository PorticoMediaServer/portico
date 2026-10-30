# Backup consistency and recovery

New backups contain one newline-delimited `manifest.json`. Creation, validation
and restore read bounded records and directory batches rather than retaining the
file registry in memory. Each file has a length and SHA-256 hash, with a running
manifest chain and final checksum. There is no total manifest-size or file-count
ceiling in this format and no manifest chunk files.

Restore reports damaged manifest lines and byte regions and continues with
readable entries. A damaged chain alone does not discard a file whose payload
matches its file hash. Keys and configuration require matching payload hashes.
A readable database without a matching manifest hash is inspected and reported
as unverified; structural validation still applies. Before replacing live state,
restore completes a previous-state recovery copy and journals incremental work.

During backup, application-owned changes to Keys and `portico.toml` participate
in a process-local mutation fence. A writer active before the database copy, or
any admitted mutation before the final comparison, makes the attempt fail.
The carried authority path set and file bytes must also match stable per-file
observations of the live files. Only `.conf` files in `Keys/managed-storage` are
carried; allocation locks and temporary files are excluded. Other Keys files,
including cryptographic keys, remain part of the backup.

External editors and rclone do not participate in the process fence. Stable
per-file end comparisons can detect changed names and bytes, but do not establish
a single global instant across files or detect arbitrary external A→B→A edits.
Edit authority files with the server stopped when an exact cross-file snapshot
is required. A changed rclone configuration is checked by the same per-file rule;
its database reference is by filename. No filesystem snapshot guarantee is made.
The process fence has no persisted generation and makes no cross-restart claim;
interrupted attempts are retired through the existing scratch and hold cleanup.
