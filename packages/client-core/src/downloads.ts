import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {routePrefix} from "../../contracts/src/routes.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {wireDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {serviceText} from './presentation/service-text.ts';
import type {MessageId} from '../../i18n/src/index.ts';
import {firstOfEach, keepReadable, noteSkipped, oneOf, oneOfOr} from './tolerant.ts';
/**
 * Wire shapes for offline downloads (the `*_download_*` operations of
 * `server/api/portico.openapi.json`; `server/api/downloads.md` explains the model).
 *
 * Parsers only. A download decides whether a client may play bytes with no
 * server in reach, so nothing here is trusted by shape alone: every field is
 * checked and every collection is bounded. What verifies or unlocks a file
 * (hashes, sizes, grants, receipts, keys, revocations, revisions, cursors,
 * limits) is exact, and a response that breaks it is rejected. What is only
 * shown is read forgivingly, because a server may be newer than this app: one
 * unreadable row of a list is left out and the rest is kept.
 */

export type DownloadState = 'queued'|'running'|'ready'|'paused'|'failed'|'unavailable'|'cancelled'|'expired';
export type DownloadAction = 'pause'|'resume'|'cancel'|'retry'|'remove';
export type DownloadOrigin = 'item'|'items'|'container'|'next';
export type DownloadArtifactKind = ''|'source'|'prepared';

export const downloadStates: readonly DownloadState[] = Object.freeze(['queued','running','ready','paused','failed','unavailable','cancelled','expired'] as const);
export const downloadActions: readonly DownloadAction[] = Object.freeze(['pause','resume','cancel','retry','remove'] as const);
/** The reasons this app has its own words for. A newer server may publish another code; it is kept when it is shaped like one. */
export const downloadReasons: readonly string[] = Object.freeze([
  '','downloads_not_allowed','item_deleted','source_unavailable','source_changed','optimized_version_unavailable',
  'optimization_failed','storage_full','artifact_changed','verification_failed','retention_expired','cancelled',
  'removed','preparation_failed','account_disabled','revoked','unknown_receipt','unknown_preparation','several_files','transcoding_disabled',
]);
/** The quality that is the untouched file. Every other quality is whatever the server's menu
 * (`download-options`) lists: the server decides what it can produce, so a client keeps no list
 * of its own and a quality the server learns to make appears without a client change. */
export const originalQuality = 'original';
/** What a download is of, as the server last knew it: enough to list it, group it under its show,
 * album or book, and put it in order, with no further request. `onServer` is false once the title
 * has left the server's library; the copy on a device is unaffected. */
export type DownloadTitle = Readonly<{
  kind: string; title: string; year: number; durationSeconds: number;
  parentId: string; parentTitle: string; groupId: string; groupTitle: string;
  groupNumber: number|null; number: number|null; artist: string; onServer: boolean;
}>;

export type DownloadArtifact = Readonly<{kind: DownloadArtifactKind; ref: string; sha256: string; bytes: number; estimated: boolean; container: string; contentType: string; fileName: string}>;
export type DownloadProgress = Readonly<{bytesDone: number; bytesTotal: number; bytesTotalEstimated: boolean; percent: number; etaSeconds: number|null}>;
export type DownloadPreparation = Readonly<{
  id: string; versionId?:string; itemId: string; libraryId: string; profileId: string; title: DownloadTitle; quality: string; origin: DownloadOrigin;
  batchId: string; state: DownloadState; reason: string; artifact: DownloadArtifact; progress: DownloadProgress;
  actions: readonly DownloadAction[]; revision: number; createdAt: string; updatedAt: string; readyAt: string; expiresAt: string;
}>;
export type DownloadRejection = Readonly<{itemId: string; code: string}>;
export type DownloadBatch = Readonly<{batchId: string; items: readonly DownloadPreparation[]; rejected: readonly DownloadRejection[]; accepted: number; duplicate: boolean}>;
export type DownloadPage = Readonly<{items: readonly DownloadPreparation[]; nextCursor: string}>;
/** The address a download's bytes are fetched from. It stays good while it is used or renewed;
 * `renewAfterSeconds` is when to renew a transfer that is still unfinished. Renewing (`renew` with
 * this `token`) keeps the same address, so a transfer the device's own downloader is holding
 * carries on under it. */
export type DownloadGrant = Readonly<{preparationId: string; itemId: string; url: string; token: string; issuedAt: string; expiresAt: string; renewAfterSeconds: number; renewed: boolean; artifact: DownloadArtifact}>;
/** One file that travels with a download; use the server's relative bundle path
 * under the download grant, independently of live subtitle media addresses. */
export type DownloadBundleFile = Readonly<{path: string; contentType: string}>;
export type DownloadBundleSubtitle = Readonly<{id: string; revision: number; language: string; title: string; default: boolean; forced: boolean; files: Readonly<Record<string, DownloadBundleFile>>;
  /** An image track's line count and how many are forced (PGS, VobSub), as `/lines` answers it while streaming. */
  lines?: Readonly<{forced: number; total: number}>}>;
export type DownloadBundleChapter = Readonly<{index: number; title: string; startSeconds: number; endSeconds: number; /** The chapter's thumbnail, when the library makes them. */ image?: DownloadBundleFile}>;
/** The file's scrubbing previews: sprite sheets (`tiles`, in order) of frames every `intervalSeconds`. */
export type DownloadBundleTrickplay = Readonly<{id: string; tileWidth: number; tileHeight: number; columns: number; rows: number; intervalSeconds: number; frameCount: number; durationSeconds: number; sourceOffsetSeconds: number; thumbnails: DownloadBundleFile; tiles: readonly DownloadBundleFile[]}>;
export type DownloadBundleMarker = Readonly<{kind: string; startSeconds: number; endSeconds: number; automatic: boolean}>;
export type DownloadBundleAudio = Readonly<{index: number; language: string; title: string; codec: string; channels: number; default: boolean}>;
/** Everything a download needs besides its media file, so the title can be listed, shown and
 * played properly with no server in reach. */
export type DownloadBundle = Readonly<{
  publication?: string;
  preparationId: string; itemId: string; quality: string; qualityLabel: string; title: DownloadTitle; overview: string;
  media: Readonly<{container: string; videoCodec: string; audioCodec: string; width: number; height: number}>;
  artwork: readonly Readonly<{role: string; path: string; contentType: string}>[];
  chapters: readonly DownloadBundleChapter[]; markers: readonly DownloadBundleMarker[];
  audio: readonly DownloadBundleAudio[]; subtitles: readonly DownloadBundleSubtitle[];
  trickplay?: DownloadBundleTrickplay;
}>;
export type DownloadQualityOption = Readonly<{
  quality: string; label: string; kind: 'source'|'optimized'; targetDisplayHeight: number;
  maxVideoBitrateBps: number; maxAudioBitrateBps: number; estimatedBytes: number; estimated: boolean;
  available: boolean; reason: string; requiresPreparation: boolean;sameAsOriginal?:boolean;dataGBPerHour?:number;effectiveQuality?:string;
  preparedProfileId: string; preparedVersionId: string; preparationId: string; preparationState: string;
}>;
export type DownloadSource = Readonly<{container: string; videoCodec: string; audioCodec: string; height: number; durationSeconds: number; bytes: number; available: boolean}>;
export type DownloadStorage = Readonly<{maxPreparedBytes: number; committedBytes: number; remainingBytes: number|null; retentionDays: number}>;
export type DownloadOptionsView = Readonly<{
  itemId: string; kind: string; source: DownloadSource; options: readonly DownloadQualityOption[];
  policy: Readonly<{allowDownloads: boolean; reason: string}>; storage: DownloadStorage;
}>;
export type DownloadPublicationReceipt = Readonly<{sourceId: string; revision: string; format: 'epub' | 'pdf' | 'cbz'; documentSha256: string}>;
export type ReceiptClaims = Readonly<{
  publication?: DownloadPublicationReceipt;
  kind: 'portico.download-receipt'; version: '1'; receiptId: string; keyId: string;
  viewer: Readonly<{authority: string; accountId: string; profileId: string; serverId: string}>;
  itemId: string; preparationId: string; quality: string; qualityLabel: string;
  artifact: Readonly<{sha256: string; bytes: number}>; issuedAt: string; expiresAt: string;
}>;
export type DownloadReceipt = Readonly<{receiptId: string; algorithm: 'ed25519'; keyId: string; payload: string; signature: string; claims: ReceiptClaims; revision: number}>;
/** `titleOnServer` accompanies a receipt: false means the title has left the server's library
 * (the copy still plays). It is null on a refusal. */
export type ReceiptOutcome = Readonly<{preparationId: string; receiptId: string; outcome: 'issued'|'renewed'|'refused'; code: string; receipt: DownloadReceipt|null; titleOnServer: boolean|null;
  /** Where the viewer stands with the title on the server now (sent with a renewal), or null. */
  viewing: Readonly<{positionSeconds: number; watched: boolean}>|null}>;
export type ReceiptKey = Readonly<{id: string; algorithm: 'ed25519'; publicKey: string; createdAt: string; retiredAt: string}>;
export type DownloadRevocation = Readonly<{receiptId: string; itemId: string; profileId: string; reason: string; revokedAt: string; sequence: number}>;
export type DownloadRevocationPage = Readonly<{items: readonly DownloadRevocation[]; nextCursor: string; asOf: string}>;
/** One play of a downloaded copy, as the device last saw it. A device reports one entry per play
 * (`playId` names it and stays the same while later positions of it are reported), not one per
 * title. Whether a play that stopped short counts as watched is the server's decision, by the
 * viewer's own settings; `ended` only says the file played to its end. */
export type DeferredProgressEntry = Readonly<{playId: string; itemId: string; positionSeconds: number; ended?: boolean; observedAt: string}>;
export type DeferredProgressOutcome = 'applied'|'history_paused'|'stale_observation'|'superseded_online'|'item_deleted'|'invalid_entry'|'observation_in_future';
/** What the server now holds for the title (`positionSeconds`, `watched`), whatever became of the entry. */
export type DeferredProgressResult = Readonly<{playId: string; itemId: string; outcome: DeferredProgressOutcome; positionSeconds: number; watched: boolean; observedAt: string}>;
export type DeferredProgressReceipt = Readonly<{applied: number; conflicts: number; entries: readonly DeferredProgressResult[]}>;
export type DownloadUsage = Readonly<{
  profileId: string; profileBytes: number; profileCount: number; serverBytes: number; serverCount: number;
  distinctArtifactBytes: number; maxPreparedBytes: number; remainingBytes: number|null; retentionDays: number;
  profilesNextCursor?:string; profiles: readonly Readonly<{profileId: string;profileKey?:string;authority?:string;accountId?:string;accountName?:string;profileName?:string;storedBytes?:number; bytes: number; count: number}>[];
}>;
/** One quality a download may be asked for when there is no single file to read a menu for (a
 * whole show, season, album, book or playlist). */
export type DownloadQualityChoice = Readonly<{quality: string; label: string; kind: 'source'|'optimized'; targetDisplayHeight: number}>;
export type DownloadSettings = Readonly<{maxPreparedBytes: number; retentionDays: number; revision: number; qualities: readonly DownloadQualityChoice[]}>;

const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,160}$/.test(v);
const text = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const count = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) >= 0;
const digest = (v: unknown): v is string => typeof v === 'string' && /^[0-9a-f]{64}$/.test(v);
/** A published code (a reason, a quality rung) this app may not know yet: short and lower-case, never free text. */
const code = (v: unknown): v is string => typeof v === 'string' && /^[a-z0-9_-]{1,64}$/.test(v);
const reasonCode = (v: unknown): v is string => v === '' || code(v);
const base64url = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && v.length <= max && /^[A-Za-z0-9_-]+$/.test(v);
/** An empty instant means "not yet" and is published as an absent field. */
const instant = (v: unknown, optional = false): v is string => typeof v === 'string' && (optional && v === '' || /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(v) && Number.isFinite(Date.parse(v)));

function invalid(): never { throw new Error(unreadableServerResponse); }
function array(v: unknown, max: number): unknown[] { if (!Array.isArray(v) || v.length > max) invalid(); return v; }
function nullableCount(v: unknown): number|null { if (v === null || v === undefined) return null; if (!count(v)) invalid(); return v; }

const seconds = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0;
function optionalCount(v: unknown): number|null { if (v === undefined || v === null) return null; if (!Number.isSafeInteger(v)) invalid(); return v as number; }

export function parseDownloadTitle(wire: unknown): DownloadTitle {
  const raw: unknown = wireDurations(wire, {durationSeconds: 'durationMs'});
  if (!object(raw) || !text(raw.kind ?? '', 64) || !text(raw.title ?? '', 1024) || typeof raw.onServer !== 'boolean') invalid();
  for (const field of ['parentTitle','groupTitle','artist'] as const) if (!text(raw[field] ?? '', 1024)) invalid();
  for (const field of ['parentId','groupId'] as const) if (raw[field] !== undefined && raw[field] !== '' && !id(raw[field])) invalid();
  if (raw.year !== undefined && !count(raw.year)) invalid();
  if (raw.durationSeconds !== undefined && !seconds(raw.durationSeconds)) invalid();
  return Object.freeze({
    kind: String(raw.kind ?? ''), title: String(raw.title ?? ''), year: Number(raw.year ?? 0), durationSeconds: Number(raw.durationSeconds ?? 0),
    parentId: String(raw.parentId ?? ''), parentTitle: String(raw.parentTitle ?? ''), groupId: String(raw.groupId ?? ''), groupTitle: String(raw.groupTitle ?? ''),
    groupNumber: optionalCount(raw.groupNumber), number: optionalCount(raw.number), artist: String(raw.artist ?? ''), onServer: raw.onServer,
  });
}

function artifact(v: unknown): DownloadArtifact {
  if (!object(v) || !['','source','prepared'].includes(String(v.kind ?? '')) || !count(v.bytes) || typeof v.estimated !== 'boolean') invalid();
  const sha = v.sha256 ?? '';
  if (sha !== '' && !digest(sha)) invalid();
  for (const field of ['ref','container','contentType','fileName'] as const) if (!text(v[field] ?? '', 256)) invalid();
  // An exact size is only credible once the bytes exist; an artifact with a
  // digest and no size, or a size it calls estimated, is not a ready artifact.
  if (sha !== '' && (v.estimated || v.bytes < 1)) invalid();
  return Object.freeze({kind: String(v.kind ?? '') as DownloadArtifactKind, ref: String(v.ref ?? ''), sha256: String(sha), bytes: v.bytes, estimated: v.estimated, container: String(v.container ?? ''), contentType: String(v.contentType ?? ''), fileName: String(v.fileName ?? '')});
}

function progress(v: unknown): DownloadProgress {
  if (!object(v) || !count(v.bytesDone) || !count(v.bytesTotal) || typeof v.bytesTotalEstimated !== 'boolean') invalid();
  if (typeof v.percent !== 'number' || !Number.isFinite(v.percent) || v.percent < 0 || v.percent > 100) invalid();
  // CD-26: an estimate can be overtaken; reject overshoot only for exact totals.
  if (v.bytesTotal > 0 && v.bytesDone > v.bytesTotal && v.bytesTotalEstimated === false) invalid();
  const eta = v.etaMs === null || v.etaMs === undefined ? null : count(v.etaMs) ? Math.round(v.etaMs / 1000) : invalid();
  return Object.freeze({bytesDone: v.bytesDone, bytesTotal: v.bytesTotal, bytesTotalEstimated: v.bytesTotalEstimated, percent: v.percent, etaSeconds: eta});
}
/** Progress is only shown. When it cannot be read the row is kept and shows none, rather than the row being lost with its actions. */
const noProgress: DownloadProgress = Object.freeze({bytesDone: 0, bytesTotal: 0, bytesTotalEstimated: true, percent: 0, etaSeconds: null});
const noTitle: DownloadTitle = Object.freeze({kind: '', title: '', year: 0, durationSeconds: 0, parentId: '', parentTitle: '', groupId: '', groupTitle: '', groupNumber: null, number: null, artist: '', onServer: true});
/** A claim whose title cannot be read is still a claim: it is listed, without a name. */
function shownTitle(v: unknown): DownloadTitle {
  try { return parseDownloadTitle(v); } catch { noteSkipped('downloads', 'preparation.title'); return noTitle; }
}
function shownProgress(v: unknown): DownloadProgress {
  try { return progress(v); } catch { noteSkipped('downloads', 'preparation.progress'); return noProgress; }
}

export function parseDownloadPreparation(raw: unknown): DownloadPreparation {
  if (!object(raw) || !id(raw.id) || !id(raw.itemId) || !id(raw.libraryId) || !id(raw.profileId)) invalid();
  if (!code(raw.quality)) invalid();
  // No state stands in for one this app does not know, so that row is refused; a list leaves out only that row.
  if (!downloadStates.includes(String(raw.state) as DownloadState)) invalid();
  if (!reasonCode(raw.reason ?? '')) invalid();
  if (!count(raw.revision) || raw.revision < 1) invalid();
  if (!instant(raw.createdAt) || !instant(raw.updatedAt) || !instant(raw.readyAt ?? '', true) || !instant(raw.expiresAt ?? '', true)) invalid();
  if (raw.batchId !== undefined && !text(raw.batchId, 128)) invalid();
  const offered = array(raw.actions, 5);
  const state = String(raw.state) as DownloadState;
  const bytes = artifact(raw.artifact);
  // A ready claim must name bytes a client can verify; anything else must not
  // pretend to, so a client cannot be talked into trusting an unhashed file.
  if (state === 'ready' ? bytes.sha256 === '' || bytes.kind === '' : false) invalid();
  // An action this app does not know, or one offered twice, is left out: the row then offers less, never more.
  const actions = firstOfEach('downloads', 'preparation.actions', keepReadable('downloads', 'preparation.actions', offered, v => oneOf(v, downloadActions)), v => v);
  return Object.freeze({...(id(raw.versionId)?{versionId:raw.versionId}:{}),
    id: raw.id, itemId: raw.itemId, libraryId: raw.libraryId, profileId: raw.profileId, title: shownTitle(raw.title),
    quality: raw.quality, origin: oneOfOr('downloads', 'preparation.origin', raw.origin, ['item','items','container','next'] as const, 'item'), batchId: String(raw.batchId ?? ''),
    state, reason: String(raw.reason ?? ''), artifact: bytes, progress: shownProgress(raw.progress),
    actions: Object.freeze(actions), revision: raw.revision,
    createdAt: raw.createdAt, updatedAt: raw.updatedAt, readyAt: String(raw.readyAt ?? ''), expiresAt: String(raw.expiresAt ?? ''),
  });
}

export function parseDownloadBatch(raw: unknown): DownloadBatch {
  if (!object(raw) || !text(raw.batchId, 128) || !count(raw.accepted) || typeof raw.duplicate !== 'boolean') invalid();
  const sent = array(raw.items, 100);
  const items = firstOfEach('download-batch', 'items', keepReadable('download-batch', 'items', sent, v => parseDownloadPreparation(v)), v => v.id);
  const rejected = keepReadable('download-batch', 'rejected', array(raw.rejected, 100), v => {
    if (!object(v) || !id(v.itemId) || !code(v.code)) invalid();
    return Object.freeze({itemId: v.itemId, code: v.code});
  });
  // The server cannot have accepted more than it sent rows for; a larger figure reads as the rows it sent.
  if (raw.accepted > sent.length) noteSkipped('download-batch', 'accepted');
  return Object.freeze({batchId: raw.batchId, items: Object.freeze(items), rejected: Object.freeze(rejected), accepted: Math.min(raw.accepted, sent.length), duplicate: raw.duplicate});
}

export function parseDownloadPage(raw: unknown): DownloadPage {
  if (!object(raw) || !text(raw.nextCursor ?? '', 512)) invalid();
  const items = firstOfEach('downloads', 'items', keepReadable('downloads', 'items', array(raw.items, 200), v => parseDownloadPreparation(v)), v => v.id);
  return Object.freeze({items: Object.freeze(items), nextCursor: String(raw.nextCursor ?? '')});
}

export function parseDownloadGrant(raw: unknown): DownloadGrant {
  if (!object(raw) || !id(raw.preparationId) || !id(raw.itemId) || !base64url(raw.token, 200)) invalid();
  // The URL is a path this server published. A client must never construct one,
  // and an absolute URL here would be an attempt to redirect the transfer.
  if (typeof raw.url !== 'string' || !raw.url.startsWith(routePrefix("get_download_artifact", 3)) || raw.url.includes('..')) invalid();
  if (raw.url !== encodedRoutePath("get_download_artifact", {"grant": raw.token})) invalid();
  if (!instant(raw.issuedAt) || !instant(raw.expiresAt) || !count(raw.renewAfterMs) || raw.renewAfterMs < 1000 || typeof raw.renewed !== 'boolean') invalid();
  if (Date.parse(raw.expiresAt) <= Date.parse(raw.issuedAt)) invalid();
  const bytes = artifact(raw.artifact);
  if (bytes.sha256 === '') invalid();
  return Object.freeze({preparationId: raw.preparationId, itemId: raw.itemId, url: raw.url, token: raw.token, issuedAt: raw.issuedAt, expiresAt: raw.expiresAt, renewAfterSeconds: raw.renewAfterMs / 1000, renewed: raw.renewed, artifact: bytes});
}

/** A path relative to a grant's address: plain segments, nothing that could leave it. */
const relativePath = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]+(\/[A-Za-z0-9_.-]+){1,3}$/.test(v) && !v.includes('..');
function bundleFile(raw: unknown): DownloadBundleFile {
  if (!object(raw) || !relativePath(raw.path) || !text(raw.contentType ?? '', 128)) invalid();
  return Object.freeze({path: raw.path, contentType: String(raw.contentType ?? '')});
}

/** A chapter's thumbnail; one that does not read leaves the chapter without it. */
function chapterImage(v: unknown): {image?: DownloadBundleFile} {
  if (v === undefined) return {};
  try { return {image: bundleFile(v)}; } catch { return {}; }
}

export function parseDownloadBundle(raw: unknown, preparationId: string): DownloadBundle {
  if (object(raw) && raw.publication !== undefined && (!text(raw.publication, 2 << 20) || unescape(encodeURIComponent(raw.publication)).length > 2 << 20)) invalid();
  if (!object(raw) || raw.preparationId !== preparationId || !id(raw.itemId) || !id(raw.quality) || !text(raw.qualityLabel ?? '', 128) || !text(raw.overview ?? '', 65536)) invalid();
  const media = raw.media;
  if (!object(media) || !text(media.container ?? '', 64) || !text(media.videoCodec ?? '', 64) || !text(media.audioCodec ?? '', 64)) invalid();
  for (const field of ['width','height'] as const) if (media[field] !== undefined && !count(media[field])) invalid();
  const artwork = array(raw.artwork, 16).map(v => {
    if (!object(v) || !id(v.role)) invalid();
    const file = bundleFile(v);
    return Object.freeze({role: v.role, path: file.path, contentType: file.contentType});
  });
  const chapters = keepReadable('downloads', 'bundle.chapters', array(raw.chapters, 2000), wire => {
    const v: unknown = wireDurations(wire, {startSeconds: 'startMs', endSeconds: 'endMs'});
    if (!object(v) || !count(v.index) || !text(v.title ?? '', 1024) || !seconds(v.startSeconds) || !seconds(v.endSeconds) || v.endSeconds <= v.startSeconds) invalid();
    return Object.freeze({index: v.index, title: String(v.title ?? ''), startSeconds: v.startSeconds, endSeconds: v.endSeconds, ...chapterImage(v.image)});
  });
  // Previews are an extra: a set that does not read is left out, the rest of the bundle stands.
  let trickplay: DownloadBundleTrickplay | undefined;
  if (raw.trickplay !== undefined) {
    trickplay = keepReadable('downloads', 'bundle.trickplay', [raw.trickplay], wire => {
      const v: unknown = wireDurations(wire, {intervalSeconds: 'intervalMs', durationSeconds: 'durationMs', sourceOffsetSeconds: 'sourceOffsetMs'});
      if (!object(v) || !id(v.id) || ![v.tileWidth, v.tileHeight, v.columns, v.rows, v.frameCount].every(n => count(n) && n > 0) || !seconds(v.intervalSeconds) || !(v.intervalSeconds as number > 0) || !seconds(v.durationSeconds) || !seconds(v.sourceOffsetSeconds)) invalid();
      const tiles = array(v.tiles, 128).map(bundleFile);
      if (!tiles.length || tiles.length < Math.ceil((v.frameCount as number) / ((v.columns as number) * (v.rows as number)))) invalid();
      return Object.freeze({id: v.id as string, tileWidth: v.tileWidth as number, tileHeight: v.tileHeight as number, columns: v.columns as number, rows: v.rows as number, intervalSeconds: v.intervalSeconds as number, frameCount: v.frameCount as number, durationSeconds: v.durationSeconds as number, sourceOffsetSeconds: v.sourceOffsetSeconds as number, thumbnails: bundleFile(v.thumbnails), tiles: Object.freeze(tiles)});
    })[0];
  }
  const markers = keepReadable('downloads', 'bundle.markers', array(raw.markers, 256), wire => {
    const v: unknown = wireDurations(wire, {startSeconds: 'startMs', endSeconds: 'endMs'});
    if (!object(v) || !id(v.kind) || !seconds(v.startSeconds) || !seconds(v.endSeconds) || v.endSeconds <= v.startSeconds || typeof v.automatic !== 'boolean') invalid();
    return Object.freeze({kind: v.kind, startSeconds: v.startSeconds, endSeconds: v.endSeconds, automatic: v.automatic});
  });
  const audio = keepReadable('downloads', 'bundle.audio', array(raw.audio, 64), v => {
    if (!object(v) || !count(v.index) || !text(v.language ?? '', 64) || !text(v.title ?? '', 512) || !text(v.codec ?? '', 64) || !count(v.channels) || typeof v.default !== 'boolean') invalid();
    return Object.freeze({index: v.index, language: String(v.language ?? ''), title: String(v.title ?? ''), codec: String(v.codec ?? ''), channels: v.channels, default: v.default});
  });
  const subtitles = array(raw.subtitles, 256).map(v => {
    if (!object(v) || !id(v.id) || !count(v.revision) || v.revision < 1 || !text(v.language ?? '', 64) || !text(v.title ?? '', 512) || typeof v.default !== 'boolean' || typeof v.forced !== 'boolean' || !object(v.files)) invalid();
    const files: Record<string, DownloadBundleFile> = {};
    const forms = Object.keys(v.files);
    // A styled track brings its script, its plain-text reading and up to sixteen fonts.
    if (forms.length > 24) invalid();
    for (const form of forms) { if (!/^[a-z0-9]{1,16}$/.test(form)) invalid(); files[form] = bundleFile(v.files[form]); }
    const lines = object(v.lines) && count(v.lines.forced) && count(v.lines.total) && v.lines.forced <= v.lines.total ? Object.freeze({forced: v.lines.forced, total: v.lines.total}) : undefined;
    return Object.freeze({id: v.id, revision: v.revision, language: String(v.language ?? ''), title: String(v.title ?? ''), default: v.default, forced: v.forced, files: Object.freeze(files), ...(lines ? {lines} : {})});
  });
  return Object.freeze({
    ...(typeof raw.publication === 'string' ? {publication: raw.publication} : {}),
    preparationId, itemId: raw.itemId, quality: raw.quality, qualityLabel: String(raw.qualityLabel ?? ''), title: parseDownloadTitle(raw.title), overview: String(raw.overview ?? ''),
    media: Object.freeze({container: String(media.container ?? ''), videoCodec: String(media.videoCodec ?? ''), audioCodec: String(media.audioCodec ?? ''), width: Number(media.width ?? 0), height: Number(media.height ?? 0)}),
    artwork: Object.freeze(artwork), chapters: Object.freeze(chapters), markers: Object.freeze(markers), audio: Object.freeze(audio), subtitles: Object.freeze(subtitles),
    ...(trickplay ? {trickplay} : {}),
  });
}

function qualityOption(raw: unknown): DownloadQualityOption {
  // The server's menu is the list: a quality it offers is offered, and is sent back when chosen.
  if (!object(raw) || !code(raw.quality) || !text(raw.label, 128)) invalid();
  if (!['source','optimized'].includes(String(raw.kind))) invalid();
  if (!count(raw.estimatedBytes) || typeof raw.estimated !== 'boolean' || typeof raw.available !== 'boolean' || typeof raw.requiresPreparation !== 'boolean') invalid();
  if (!reasonCode(raw.reason ?? '')) invalid();
  for (const field of ['targetDisplayHeight','maxVideoBitrateBps','maxAudioBitrateBps'] as const) if (raw[field] !== undefined && !count(raw[field])) invalid();
  for (const field of ['preparedProfileId','preparedVersionId','preparationId'] as const) if (raw[field] !== undefined && raw[field] !== '' && !id(raw[field])) invalid();
  // An unavailable option owes the client a reason; an available one has none.
  if (raw.available === (String(raw.reason ?? '') !== '')) invalid();
  // The state of a preparation already under way is a hint; one this app does not know is left out.
  const preparationState = oneOf(raw.preparationState, downloadStates) ?? '';
  if (!preparationState && raw.preparationState !== undefined && raw.preparationState !== '') noteSkipped('download-options', 'option.preparationState');
  return Object.freeze({
    quality: String(raw.quality), label: raw.label,sameAsOriginal:raw.sameAsOriginal===true,dataGBPerHour:typeof raw.dataGBPerHour==='number'?raw.dataGBPerHour:undefined,effectiveQuality:typeof raw.effectiveQuality==='string'?raw.effectiveQuality:undefined, kind: String(raw.kind) as 'source'|'optimized',
    targetDisplayHeight: Number(raw.targetDisplayHeight ?? 0), maxVideoBitrateBps: Number(raw.maxVideoBitrateBps ?? 0), maxAudioBitrateBps: Number(raw.maxAudioBitrateBps ?? 0),
    estimatedBytes: raw.estimatedBytes, estimated: raw.estimated, available: raw.available, reason: String(raw.reason ?? ''),
    requiresPreparation: raw.requiresPreparation, preparedProfileId: String(raw.preparedProfileId ?? ''), preparedVersionId: String(raw.preparedVersionId ?? ''),
    preparationId: String(raw.preparationId ?? ''), preparationState,
  });
}

function storage(raw: unknown): DownloadStorage {
  if (!object(raw) || !count(raw.maxPreparedBytes) || !count(raw.committedBytes) || !count(raw.retentionDays) || raw.retentionDays < 1) invalid();
  const remaining = nullableCount(raw.remainingBytes);
  // 0 means unlimited, and an unlimited ceiling has no remaining figure.
  if (raw.maxPreparedBytes === 0 ? remaining !== null : remaining === null) invalid();
  return Object.freeze({maxPreparedBytes: raw.maxPreparedBytes, committedBytes: raw.committedBytes, remainingBytes: remaining, retentionDays: raw.retentionDays});
}

export function parseDownloadOptions(raw: unknown, itemId: string): DownloadOptionsView {
  if (!object(raw) || raw.itemId !== itemId || !text(raw.kind, 64)) invalid();
  const source = wireDurations(raw.source,{durationSeconds:"durationMs"});
  if (!object(source) || !text(source.container ?? '', 64) || !count(source.bytes) || typeof source.available !== 'boolean') invalid();
  if (typeof source.durationSeconds !== 'number' || !Number.isFinite(source.durationSeconds) || source.durationSeconds < 0) invalid();
  for (const field of ['videoCodec','audioCodec'] as const) if (source[field] !== undefined && !text(source[field], 64)) invalid();
  if (source.height !== undefined && !count(source.height)) invalid();
  const policy = raw.policy;
  if (!object(policy) || typeof policy.allowDownloads !== 'boolean' || !reasonCode(policy.reason ?? '')) invalid();
  if (policy.allowDownloads === (String(policy.reason ?? '') !== '')) invalid();
  const options = firstOfEach('download-options', 'options', keepReadable('download-options', 'options', array(raw.options, 16), v => qualityOption(v)), v => v.quality);
  // Nothing readable to offer is not an answer a person can act on.
  if (!options.length) invalid();
  return Object.freeze({
    itemId, kind: raw.kind,
    source: Object.freeze({container: String(source.container ?? ''), videoCodec: String(source.videoCodec ?? ''), audioCodec: String(source.audioCodec ?? ''), height: Number(source.height ?? 0), durationSeconds: source.durationSeconds, bytes: source.bytes, available: source.available}),
    options: Object.freeze(options),
    policy: Object.freeze({allowDownloads: policy.allowDownloads, reason: String(policy.reason ?? '')}),
    storage: storage(raw.storage),
  });
}

/**
 * Decodes a receipt envelope and returns the claims plus the exact bytes that
 * were signed. Verification itself is a platform Ed25519 operation on
 * `signedBytes` against the key whose id is `claims.keyId`; this function does
 * the parsing and the shape checks, and refuses anything inconsistent.
 */
export function decodeDownloadReceipt(raw: unknown): {receipt: DownloadReceipt; signedBytes: Uint8Array; signatureBytes: Uint8Array} {
  if (!object(raw) || raw.algorithm !== 'ed25519' || !id(raw.receiptId) || !id(raw.keyId)) invalid();
  if (!base64url(raw.payload, 16384) || !base64url(raw.signature, 128)) invalid();
  if (!count(raw.revision) || raw.revision < 1) invalid();
  const signedBytes = decodeBase64url(raw.payload);
  const signatureBytes = decodeBase64url(raw.signature);
  if (signatureBytes.length !== 64) invalid();
  let decoded: unknown;
  try { decoded = JSON.parse(new TextDecoder().decode(signedBytes)); } catch { invalid(); }
  const claims = parseReceiptClaims(decoded);
  // The envelope may not disagree with the signed document about anything.
  if (claims.receiptId !== raw.receiptId || claims.keyId !== raw.keyId) invalid();
  const parsed = object(raw.claims) ? parseReceiptClaims(raw.claims) : claims;
  if (JSON.stringify(parsed) !== JSON.stringify(claims)) invalid();
  return {receipt: Object.freeze({receiptId: raw.receiptId, algorithm: 'ed25519', keyId: raw.keyId, payload: raw.payload, signature: raw.signature, claims, revision: raw.revision}), signedBytes, signatureBytes};
}

function publicationReceipt(raw: unknown): DownloadPublicationReceipt {
  if (!object(raw) || !id(raw.sourceId) || raw.sourceId.length > 128 || !digest(raw.revision) || !digest(raw.documentSha256)) invalid();
  const format = raw.format;
  if (format !== 'epub' && format !== 'pdf' && format !== 'cbz') invalid();
  return Object.freeze({sourceId: raw.sourceId, revision: raw.revision, format, documentSha256: raw.documentSha256});
}
export function parseReceiptClaims(raw: unknown): ReceiptClaims {
  if (!object(raw) || raw.kind !== 'portico.download-receipt' || raw.version !== '1') invalid();
  if (!id(raw.receiptId) || !id(raw.keyId) || !id(raw.itemId) || !id(raw.preparationId)) invalid();
  if (!id(raw.quality) || !text(raw.qualityLabel ?? '', 128)) invalid();
  if (!instant(raw.issuedAt) || !instant(raw.expiresAt) || Date.parse(raw.expiresAt) <= Date.parse(raw.issuedAt)) invalid();
  const viewer = raw.viewer;
  if (!object(viewer) || !['local','hosted'].includes(String(viewer.authority)) || !id(viewer.accountId) || !id(viewer.profileId) || !id(viewer.serverId)) invalid();
  const publication = raw.publication === undefined ? undefined : publicationReceipt(raw.publication);
  const bytes = raw.artifact;
  if (!object(bytes) || !digest(bytes.sha256) || !count(bytes.bytes) || bytes.bytes < 1) invalid();
  return Object.freeze({
    kind: 'portico.download-receipt', version: '1', receiptId: raw.receiptId, keyId: raw.keyId,
    ...(publication ? {publication} : {}),
    viewer: Object.freeze({authority: String(viewer.authority), accountId: viewer.accountId, profileId: viewer.profileId, serverId: viewer.serverId}),
    itemId: raw.itemId, preparationId: raw.preparationId, quality: String(raw.quality), qualityLabel: String(raw.qualityLabel ?? ''),
    artifact: Object.freeze({sha256: bytes.sha256, bytes: bytes.bytes}), issuedAt: raw.issuedAt, expiresAt: raw.expiresAt,
  });
}

/**
 * Whether a receipt still authorizes offline playback for this viewer at `now`.
 * A signature check is required as well; this is the half a client can get
 * wrong by forgetting, so it is one function.
 */
export function receiptUsable(claims: ReceiptClaims, viewer: {authority: string; accountId: string; profileId: string; serverId: string}, now: Date, revoked: ReadonlySet<string> = new Set()): boolean {
  if (revoked.has(claims.receiptId)) return false;
  if (claims.viewer.authority !== viewer.authority || claims.viewer.accountId !== viewer.accountId) return false;
  if (claims.viewer.profileId !== viewer.profileId || claims.viewer.serverId !== viewer.serverId) return false;
  return now.getTime() < Date.parse(claims.expiresAt);
}

export function parseReceiptOutcomes(raw: unknown): readonly ReceiptOutcome[] {
  if (!object(raw)) invalid();
  const results = array(raw.results, 100).map(v => {
    if (!object(v) || !['issued','renewed','refused'].includes(String(v.outcome))) invalid();
    if (!downloadReasons.includes(String(v.code ?? ''))) invalid();
    if (v.preparationId !== undefined && v.preparationId !== '' && !id(v.preparationId)) invalid();
    if (v.receiptId !== undefined && v.receiptId !== '' && !id(v.receiptId)) invalid();
    const refused = v.outcome === 'refused';
    if (refused ? v.receipt != null || !v.code : v.receipt == null) invalid();
    if (v.titleOnServer !== undefined && typeof v.titleOnServer !== 'boolean') invalid();
    const viewing = v.viewing === undefined ? undefined : wireDurations(v.viewing, {positionSeconds: 'positionMs'});
    if (viewing !== undefined && (!object(viewing) || !seconds(viewing.positionSeconds) || typeof viewing.watched !== 'boolean')) invalid();
    return Object.freeze({
      preparationId: String(v.preparationId ?? ''), receiptId: String(v.receiptId ?? ''),
      outcome: String(v.outcome) as 'issued'|'renewed'|'refused', code: String(v.code ?? ''),
      receipt: refused ? null : decodeDownloadReceipt(v.receipt).receipt,
      titleOnServer: refused || v.titleOnServer === undefined ? null : v.titleOnServer,
      viewing: refused || !object(viewing) ? null : Object.freeze({positionSeconds: viewing.positionSeconds as number, watched: viewing.watched as boolean}),
    });
  });
  return Object.freeze(results);
}

export function parseReceiptKeys(raw: unknown): readonly ReceiptKey[] {
  if (!object(raw)) invalid();
  const keys = array(raw.keys, 32).map(v => {
    if (!object(v) || !id(v.id) || v.algorithm !== 'ed25519' || !base64url(v.publicKey, 64) || !instant(v.createdAt) || !instant(v.retiredAt ?? '', true)) invalid();
    if (decodeBase64url(v.publicKey).length !== 32) invalid();
    return Object.freeze({id: v.id, algorithm: 'ed25519' as const, publicKey: v.publicKey, createdAt: v.createdAt, retiredAt: String(v.retiredAt ?? '')});
  });
  if (new Set(keys.map(v => v.id)).size !== keys.length) invalid();
  return Object.freeze(keys);
}

export function parseRevocationPage(raw: unknown): DownloadRevocationPage {
  if (!object(raw) || !text(raw.nextCursor ?? '', 64) || !instant(raw.asOf)) invalid();
  const items = array(raw.items, 500).map(v => {
    if (!object(v) || !id(v.receiptId) || !id(v.itemId) || !id(v.profileId) || !instant(v.revokedAt)) invalid();
    // A withdrawal is honoured whatever its reason: a code this app has no words for is still a withdrawal.
    if (!code(v.reason)) invalid();
    if (!count(v.sequence) || v.sequence < 1) invalid();
    return Object.freeze({receiptId: v.receiptId, itemId: v.itemId, profileId: v.profileId, reason: v.reason, revokedAt: v.revokedAt, sequence: v.sequence});
  });
  // The cursor is a sequence, so the page must be ordered for it to mean
  // anything; an out-of-order page would silently lose revocations.
  for (let i = 1; i < items.length; i++) if (items[i].sequence <= items[i-1].sequence) invalid();
  if (String(raw.nextCursor ?? '') !== '' && items.length && String(raw.nextCursor) !== String(items[items.length-1].sequence)) invalid();
  return Object.freeze({items: Object.freeze(items), nextCursor: String(raw.nextCursor ?? ''), asOf: raw.asOf});
}

export function parseDeferredProgressReceipt(raw: unknown): DeferredProgressReceipt {
  if (!object(raw) || !count(raw.applied) || !count(raw.conflicts)) invalid();
  const outcomes: readonly string[] = ['applied','history_paused','stale_observation','superseded_online','item_deleted','invalid_entry','observation_in_future'];
  const entries = keepReadable('download-progress', 'entries', array(raw.entries, 100), v => {
    v=wireDurations(v,{positionSeconds:'positionMs'});
    // The server echoes whatever it was sent, including an entry it found unreadable, so the
    // identifiers of a refused entry are text, not necessarily ids.
    if (!object(v) || !text(v.itemId ?? '', 256) || !text(v.playId ?? '', 256) || !outcomes.includes(String(v.outcome)) || typeof v.watched !== 'boolean') invalid();
    if (typeof v.positionSeconds !== 'number' || !Number.isFinite(v.positionSeconds) || v.positionSeconds < 0) invalid();
    if (v.observedAt !== undefined && !text(v.observedAt, 64)) invalid();
    return Object.freeze({playId: String(v.playId ?? ''), itemId: String(v.itemId ?? ''), outcome: String(v.outcome) as DeferredProgressOutcome, positionSeconds: v.positionSeconds, watched: v.watched, observedAt: String(v.observedAt ?? '')});
  });
  // The counts are read off the entries this app could read, so they never claim more than the
  // entries show. A play recorded while the viewer has paused their history is neither applied
  // nor in conflict.
  const applied = entries.filter(v => v.outcome === 'applied').length;
  const conflicts = entries.filter(v => v.outcome !== 'applied' && v.outcome !== 'history_paused').length;
  if (applied !== raw.applied || conflicts !== raw.conflicts) noteSkipped('download-progress', 'counts');
  return Object.freeze({applied, conflicts, entries: Object.freeze(entries)});
}

export function parseDownloadUsage(raw: unknown): DownloadUsage {
  if (!object(raw) || !id(raw.profileId)) invalid();
  if (!count(raw.profileBytes) || !count(raw.profileCount) || !count(raw.serverBytes) || !count(raw.serverCount) || !count(raw.distinctArtifactBytes) || !count(raw.maxPreparedBytes) || !count(raw.retentionDays)) invalid();
  if (raw.retentionDays < 1) invalid();
  if (raw.profileBytes > raw.serverBytes || raw.profileCount > raw.serverCount) invalid();
  const remaining = nullableCount(raw.remainingBytes);
  if (raw.maxPreparedBytes === 0 ? remaining !== null : remaining === null) invalid();
  const profiles = firstOfEach('download-usage', 'profiles', keepReadable('download-usage', 'profiles', array(raw.profiles, 256), v => {
    if (!object(v) || !id(v.profileId) || !count(v.bytes) || !count(v.count)) invalid();
    return Object.freeze({profileId:v.profileId,bytes:v.bytes,count:v.count,...(text(v.profileKey,1024)?{profileKey:v.profileKey}:{}),...(text(v.authority,32)?{authority:v.authority}:{}),...(text(v.accountId,160)?{accountId:v.accountId}:{}),...(text(v.accountName,512)?{accountName:v.accountName}:{}),...(text(v.profileName,512)?{profileName:v.profileName}:{}),...(count(v.storedBytes)?{storedBytes:v.storedBytes}: {})});
  }), v => v.profileKey??v.profileId);
  return Object.freeze({
    profileId: raw.profileId, profileBytes: raw.profileBytes, profileCount: raw.profileCount,
    serverBytes: raw.serverBytes, serverCount: raw.serverCount, distinctArtifactBytes: raw.distinctArtifactBytes,
    profilesNextCursor:text(raw.profilesNextCursor,1024)?raw.profilesNextCursor:"",maxPreparedBytes: raw.maxPreparedBytes, remainingBytes: remaining, retentionDays: raw.retentionDays, profiles: Object.freeze(profiles),
  });
}

export function parseDownloadSettings(raw: unknown): DownloadSettings {
  if (!object(raw) || !count(raw.maxPreparedBytes) || !count(raw.retentionDays) || !count(raw.revision)) invalid();
  if (raw.retentionDays < 1 || raw.retentionDays > 365 || raw.revision < 1) invalid();
  // The menu for a whole show or album. A choice this app cannot read is left out and the rest
  // are kept; the original is always there to ask for, so a menu that cannot be read at all, or
  // does not lead with it, is the original alone.
  const original: DownloadQualityChoice = Object.freeze({quality: originalQuality, label: 'Original', kind: 'source', targetDisplayHeight: 0});
  if (raw.qualities !== undefined && !Array.isArray(raw.qualities)) noteSkipped('download-settings', 'qualities');
  let qualities = firstOfEach('download-settings', 'qualities', keepReadable('download-settings', 'qualities', Array.isArray(raw.qualities) ? raw.qualities.slice(0, 32) : [], v => {
    if (!object(v) || !code(v.quality) || !text(v.label, 128) || !['source','optimized'].includes(String(v.kind))) invalid();
    if (v.targetDisplayHeight !== undefined && !count(v.targetDisplayHeight)) invalid();
    return Object.freeze({quality: v.quality, label: v.label, kind: String(v.kind) as 'source'|'optimized', targetDisplayHeight: Number(v.targetDisplayHeight ?? 0)});
  }), v => v.quality);
  if(qualities.length!==canonicalQualityLadder.length||qualities.some((v,i)=>v.quality!==canonicalQualityLadder[i].id))invalid();
  return Object.freeze({maxPreparedBytes: raw.maxPreparedBytes, retentionDays: raw.retentionDays, revision: raw.revision, qualities: Object.freeze(qualities)});
}

/** Validates one deferred observation before it is queued for a later send. */
export function validDeferredProgressEntry(v: DeferredProgressEntry): boolean {
  if (!id(v.playId) || !id(v.itemId) || !instant(v.observedAt)) return false;
  if (typeof v.positionSeconds !== 'number' || !Number.isFinite(v.positionSeconds) || v.positionSeconds < 0) return false;
  return v.ended === undefined || typeof v.ended === 'boolean';
}

/** How a download is named in a list: an episode under its show with its place in it, a song with
 * its artist, anything else by its own name. `subtitle` is the second line, '' when there is none. */
export function downloadTitleLines(title: DownloadTitle): Readonly<{title: string; subtitle: string}> {
  const two = (n: number) => String(n).padStart(2, '0');
  if (title.kind === 'episode' && title.parentTitle) {
    const place = title.groupNumber !== null && title.number !== null ? serviceText('downloads.title.episode', {season: title.groupNumber, episode: two(title.number)}) : '';
    return Object.freeze({title: title.title || place || title.parentTitle, subtitle: [title.parentTitle, title.title ? place : ''].filter(Boolean).join(' · ')});
  }
  if (title.kind === 'song' || title.kind === 'audiobook_file') return Object.freeze({title: title.title || title.parentTitle, subtitle: [title.artist, title.parentTitle].filter(Boolean).join(' · ')});
  return Object.freeze({title: title.title, subtitle: title.year > 0 ? String(title.year) : ''});
}

/** Whether an action the client is about to send is one the server published. */
export const downloadActionOffered = (preparation: DownloadPreparation, action: DownloadAction): boolean => preparation.actions.includes(action);

/** The sentence for a reason code, in the viewer's language. A code this app has no sentence for
 * (a newer server's) is said as its own words rather than left blank. */
export function downloadReasonMessage(reason: string): string {
  if (!reason) return '';
  if (!downloadReasons.includes(reason)) return reason.replace(/_/g, ' ');
  return serviceText(('downloads.reason.' + reason) as MessageId);
}

export const downloadBytes = (n: number): string => n < 1048576 ? `${Math.round(n/1024)} KiB` : n < 1073741824 ? `${(n/1048576).toFixed(1)} MiB` : `${(n/1073741824).toFixed(2)} GiB`;

function decodeBase64url(v: string): Uint8Array {
  const padded = v.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - v.length % 4) % 4);
  const binary = atob(padded);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

export type DownloadProfileRevocation=Readonly<{claims:number;receipts:number;more:boolean}>;
export function parseDownloadProfileRevocation(raw:unknown):DownloadProfileRevocation{
 if(!object(raw)||!count(raw.claims)||raw.claims>100||!count(raw.receipts)||raw.receipts>100||typeof raw.more!=='boolean')invalid();
 return Object.freeze({claims:raw.claims,receipts:raw.receipts,more:raw.more});
}
