# API contract notes

## Notification `readAt`

For the schema-v1 notification inbox, an individually read notice retains its exact individual-read timestamp. A notice covered only by read-all reports the timestamp of the current covering read-all. A later covering read-all may move that bulk-only timestamp forward. An unread notice reports `null`.

For example, read-all at t1 covers A; B then arrives; read-all at t2 covers both. A and B report t2. A notice C individually read at tc continues to report tc. Bulk-only `readAt` is not an immutable first-read event.

This is the approved pre-release contract for the schema-v1 cutover. The generated `GetNotificationsInboxResponse` field remains `string | null`.
