# Playback reporting and overload recovery

The active typed playback API uses `POST /v1/playback/sessions/{id}/timeline`.
Clients send increasing report sequences and follow the server's
`Report-Every-Ms` response header for periodic reports. Immediate reports for
seeks and playback state changes remain supported.

Timeline admission allows ten reports per second with a burst of twenty per
authenticated device. The bucket belongs to the server, authority, account and
verified device together. Changing profiles, access tokens or playback session
IDs does not create another bucket. Normal periodic reporting (usually one
report per ten seconds) and ordinary manual controls are well below this limit.
The check follows authentication and device verification and precedes timeline
persistence. It does not make a cached credential sufficient for authorization.

Excess reports receive `429 rate_limited` and `Retry-After: 1`. A rejected report
does not change progress, playback state or the session lease. Wait for the retry
delay, then retry the same report sequence and body. A timeout or lost response
can occur after the server accepted a report, so do not rewrite an attempted
sequence. Serialize reports and retain state changes and terminal observations;
coalesce periodic progress only while it is still unsent. Avoid parallel retries
and retain enough time to renew before lease expiry. Successful reports continue
returning the server's reporting cadence.

Duplicate and stale reports never rewind progress or replace fresher evidence.
Their lease renewal is coalesced when an existing lease already has sufficient
time remaining; genuine fresh reports continue applying immediately. Renewal is
still performed before the lease expires, including when configured leases are
shorter than the reporting cadence.

General request admission also bounds the number of active and waiting requests.
An overloaded lane responds with `503 server_busy`, `retryable: true`, and
`Retry-After: 1`. Clients should pause and retry rather than increase concurrency.
A change the database writer refused before it began answers `503
writer_capacity` with `Retry-After` from 1 to 30 seconds; nothing was saved, and
the same request (with the same `Idempotency-Key`) should be sent again.
Short API responses have a rolling write budget: a client that makes no response
progress for fifteen seconds releases the request instead of occupying a lane
indefinitely. Media transfers and event streams retain their own streaming
deadlines; this response budget does not limit playback duration.

These controls bound this reporting endpoint and each admission lane. They do
not impose a complete quota on authenticated device creation, playback session
creation or all API mutations. Distinct approved devices still have separate
request fairness identities; refreshed credentials for the same verified device
retain its identity. A hostile deployment may need broader account and source
limits in addition to these controls.
