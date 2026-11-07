# API contract notes

## Notification `readAt`

For the schema-v1 notification inbox, an individually read notice retains its exact individual-read timestamp. A notice covered only by read-all reports the timestamp of the current covering read-all. A later covering read-all may move that bulk-only timestamp forward. An unread notice reports `null`.

For example, read-all at t1 covers A; B then arrives; read-all at t2 covers both. A and B report t2. A notice C individually read at tc continues to report tc. Bulk-only `readAt` is not an immutable first-read event.

This is the approved pre-release contract for the schema-v1 cutover. The generated `GetNotificationsInboxResponse` field remains `string | null`.

Read-all excludes archived notices. An archived notice marked unread stays unread through read-all.

Coverage follows **sequence plus revision ordering**: a notice must be within the read-through sequence and must not have been restated after that read-all. Producer restatement resets older individual and bulk acknowledgments. A later individual unread overrides older bulk coverage; later eligible read-all overrides that unread. Individual read timestamps remain exact under subsequent bulk coverage.

A zero-sequence head supplies no bulk coverage, as does an absent head. A positive-sequence head with timestamp zero is uninitialized and requires resync rather than synthesizing a read time.
