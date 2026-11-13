# Cast protocol

The receiver redeems POST /v1/cast/codes through POST /v1/cast/redeem, retaining only its own session. Refresh uses POST /v1/auth/refresh with an installation id and a saved request id. Only the paired SDK sender sees private status or may load/play/seek/change tracks. Other senders may pause or stop; a different profile asks before taking over. Every new attachment proves its viewer with a fresh code.

Playback starts with a stable Idempotency-Key, retries a 202 with the same body/key, reports PUT /v1/playback/sessions/{id}/progress, changes through PATCH, and finishes with plain DELETE plus an observed final position. Unanswered stops retain their original session and credential in the receiver's local stop store and respect Retry-After. A confirmed stop can be acknowledged again without ending a successor. No old server-clock policy or receipt lookup is used.

CAF receives only an opaque deck id resolved privately to the server's media address. Public Cast status carries no media grant, token or subtitle URL. The receiver embeds its own UI, catalogue, fonts and licence. No production dependency on server/ remains.
