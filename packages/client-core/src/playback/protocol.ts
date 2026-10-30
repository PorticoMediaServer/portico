/** Schema-derived TypeScript projections. Runtime data still requires validation.
 * Sole wire authority: services/server/api/playback-v2.openapi.json, PR-01 draft3.
 * Contract32 O01–O04, R01/R06; generated field/enum/nullability parity, not a new API.
 */
export const PLAYBACK_SCHEMA_SHA256 = "02042af675ebd263f48a37155fc9bd87dd81a514e639611971cacdfca9832555" as const;

export type ID = string;
/** Canonical decimal integer 0..9223372036854775807. Semantic bound enforced in addition to pattern; no sign, whitespace, leading zero or exponent. */
export type UInt64 = string;
/** Canonical decimal integer 1..9223372036854775807; never converted through floating point. */
export type PositiveCounter = string;
/** Integer microseconds -9223372036854775807..9223372036854775807. Negative source origins permitted; no negative zero. */
export type SignedUs = string;
/** UTC RFC3339 wall clock; not a media position. */
export type Timestamp = string;
export type Version = "2.0";
export type ControllerScope = { readonly controllerId: ID; readonly controllerEpoch: ID };
export type RequestIdentity = { readonly controllerId: ID; readonly controllerEpoch: ID; readonly commandLaneId: ID; readonly requestId: ID };
export type Command = { readonly controllerId: ID; readonly controllerEpoch: ID; readonly commandLaneId: ID; readonly sequence: PositiveCounter; readonly requestId: ID };
/** Both presentation values are null before a presentation exists; otherwise both required. Desired snapshots compare every value, not just the logical revision. */
export type Revisions = { readonly playbackRevision: PositiveCounter; readonly ownershipRevision: PositiveCounter; readonly presentationGeneration: (PositiveCounter) | (null); readonly presentationRevision: (PositiveCounter) | (null) };
/** Logical item microseconds. Null edition/part is an explicit server-default preference on create; accepted plans supply concrete context where relevant. */
export type TimelineContext = { readonly itemId: ID; readonly editionId: (ID) | (null); readonly partId: (ID) | (null); readonly mappingId: (ID) | (null) };
export type Position = { readonly context: TimelineContext; readonly positionUs: UInt64 };
export type Seek = { readonly seekCommandId: ID; readonly target: Position };
/** Automatic requires both IDs null; explicit requires sourceId and optionally pins sourceVersionId. Server verifies source membership/access; no path or URL accepted. */
export type SourceSelection = { readonly mode: "automatic" | "explicit"; readonly sourceId: (ID) | (null); readonly sourceVersionId: (ID) | (null) };
/** Explicit requires a stable version-scoped trackId. Automatic/off require null. Audio off is valid only for media with video; subtitle off is always expressible. */
export type TrackSelection = { readonly mode: "automatic" | "explicit" | "off"; readonly trackId: (ID) | (null) };
export type SubtitleSelection = { readonly track: TrackSelection; readonly renderMode: "automatic" | "external_text" | "styled" | "embedded" | "burn_in" };
/** Original never silently lowers fidelity. Limited requires at least one limit. Original requires all limits null and allowLossyConversion false. Automatic operates within explicit limits/fidelity constraints. */
export type Quality = { readonly mode: "automatic" | "original" | "limited"; readonly maxBitrateBps: (number) | (null); readonly maxWidth: (number) | (null); readonly maxHeight: (number) | (null); readonly allowLossyConversion: boolean; readonly allowHDRToSDR: boolean };
/** Full snapshot, never a delta. Retain an unacknowledged seek in later Pause. Same seek ID cannot change target. Observed position is separate; acknowledged seek retained in this snapshot does not execute again. */
export type DesiredState = { readonly context: TimelineContext; readonly source: SourceSelection; readonly audio: TrackSelection; readonly subtitles: SubtitleSelection; readonly quality: Quality; readonly state: "playing" | "paused"; readonly rate: Rational; readonly seek: (Seek) | (null); readonly offerSelection: ({ readonly offersRevision: ID; readonly alternativeId: ID }) | (null) };
/** Client evidence only. Empty supported lists mean no declared support; null limits are unknown, not unlimited. Server may prepare/reject unknown tuples. Codec strings never become command arguments. */
export type CodecCapability = { readonly codec: string; readonly profiles: readonly (string)[]; readonly levels: readonly (string)[]; readonly bitDepths: readonly (number)[]; readonly maxWidth: (number) | (null); readonly maxHeight: (number) | (null); readonly maxFrameRate: (Rational) | (null); readonly dynamicRanges: readonly ("sdr" | "hdr10" | "hlg" | "dolby_vision")[]; readonly maxChannels: (number) | (null); readonly channelLayouts: readonly (string)[]; readonly sampleRatesHz: readonly (number)[] };
/** Support is scoped to this transport/container and full codec conditions, never combined as independent global codec/container lists. */
export type TransportCapability = { readonly transport: "original_http" | "hls_ts" | "hls_fmp4"; readonly containers: readonly (string)[]; readonly video: readonly (CodecCapability)[]; readonly audio: readonly (CodecCapability)[]; readonly byteRanges: boolean; readonly audioTrackSwitching: "none" | "same_presentation" | "replacement"; readonly subtitleRenderers: readonly ("external_text" | "styled" | "embedded" | "bitmap")[] };
/** Server intersects untrusted bounded claims with source facts and policy. AVPlayer remains the native engine; this public API also admits independent clients. Contradictions are reported through observations. */
export type Capabilities = { readonly profileVersion: ID; readonly engine: string; readonly engineVersion: string; readonly os: string; readonly osVersion: string; readonly outputRouteId: ID; readonly outputRouteRevision: PositiveCounter; readonly transports: readonly (TransportCapability)[]; readonly candidatePreparation: "bounded_media" | "serial_attach"; readonly backgroundControl: boolean; readonly evidence: "declared" | "runtime_measured" };
/** Safe public diagnostic fields only. Unknown future error codes are treated as non-automatically-retryable and displayed safely; clients never assume a new code means success. */
export type Failure = { readonly code: "unsupported_version" | "invalid_request" | "unauthorized" | "access_revoked" | "controller_retired" | "receipt_expired" | "receipt_not_found" | "identity_conflict" | "sequence_stale" | "revision_conflict" | "lane_occupied" | "owner_account_cap" | "owner_server_cap" | "transcoding_disabled" | "unknown_source_facts" | "unsupported_tuple" | "source_changed" | "source_unavailable" | "actual_allocation_failure" | "encoder_failure" | "delivery_http_failure" | "decoder_contradiction" | "seek_mismatch" | "network_stall" | "lease_expired" | "candidate_expired" | "operation_unavailable" | "not_found" | "internal_error" | "timeline_unmapped"; readonly stage: "validation" | "authority" | "ordering" | "admission" | "negotiation" | "source" | "queue" | "production" | "delivery" | "engine" | "recovery" | "cleanup"; readonly message: string; readonly retryable: boolean; readonly action: "retry_same_request" | "lookup_receipt" | "reauthorize" | "reconcile" | "replan" | "prepare" | "user_choice" | "terminal" | "none"; readonly request: (RequestIdentity) | (null); readonly playbackId: (ID) | (null); readonly presentationId: (ID) | (null); readonly currentRevisions: (Revisions) | (null) };
/** A lane is permanently bound on accepted Create, including after terminal playback. Independent Create always needs a fresh lane; explicit related item replacement may transfer ownership in a bound lane. */
export type Watermark = { readonly commandLaneId: ID; readonly highestSeen: UInt64; readonly highestAccepted: UInt64; readonly boundPlaybackId: (ID) | (null); readonly status: "fresh" | "bound" | "retired" };
/** Immutable original disposition, not current status. Never contains usable media credentials. Replays cannot renew/schedule/activate. Identity and digest evidence survives body pruning. */
export type Receipt = { readonly protocolVersion: Version; readonly request: RequestIdentity; readonly sequence: PositiveCounter; readonly operation: "create" | "attach" | "intent" | "prepare_replacement" | "commit_replacement" | "handoff" | "stop" | "commit_handoff" | "cancel_handoff"; readonly payloadDigest: string; readonly disposition: "accepted" | "rejected" | "cancelled"; readonly playbackId: (ID) | (null); readonly acceptedRevision: (PositiveCounter) | (null); readonly error: (Failure) | (null); readonly recordedAt: Timestamp; readonly bodyExpiresAt: Timestamp; readonly statusUrl: string; readonly receiptUrl: string; readonly pollAfterMs: number };
/** Targeted cancellation result/tombstone, not an intent receipt. An accepted original receipt remains immutable; current read exposes cancellation. No intent watermark advancement. */
export type Cancellation = { readonly protocolVersion: Version; readonly request: RequestIdentity; readonly disposition: "cancelled"; readonly playbackId: (ID) | (null); readonly recordedAt: Timestamp };
/** Independent create requires fresh lane and replaces=null. Explicit different-item slot transfer creates a new occurrence but leaves old active while candidate prepares; commit transfers slot and fences source. Desired seek null means resume policy; explicit start uses a seek ID. */
export type CreateRequest = { readonly protocolVersion: Version; readonly command: Command; readonly desired: DesiredState; readonly capabilities: Capabilities; readonly replaces: ({ readonly playbackId: ID; readonly expected: Revisions }) | (null) };
export type IntentRequest = { readonly protocolVersion: Version; readonly command: Command; readonly expected: Revisions; readonly desired: DesiredState; readonly capabilities: Capabilities };
/** Stops logical occurrence and fences media before progress/reclamation. Identical accepted receipt replay succeeds even after termination. Fresh stale revision reconciles; never blindly rebases an obsolete command. */
export type StopRequest = { readonly protocolVersion: Version; readonly command: Command; readonly expected: Revisions };
/** Original identity only; no new current-intent sequence. Can precede original request. Only cancels occurrence created by that exact original Create; it cannot cancel an unrelated occurrence or act as cancellation of arbitrary future state commands. */
export type CancelRequest = { readonly protocolVersion: Version; readonly request: RequestIdentity };
/** Positive reduced rational: numerator/denominator coprime, each <=9223372036854775807. No floating point normalization. */
export type Rational = { readonly numerator: PositiveCounter; readonly denominator: PositiveCounter };
