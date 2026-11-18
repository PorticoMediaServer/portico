import {canonicalQualityLadder} from '../../contracts/src/types.ts';
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
 * (hashes, sizes, grants, revisions, cursors,
 * limits) is exact, and a response that breaks it is rejected. What is only
 * shown is read forgivingly, because a server may be newer than this app: one
 * unreadable row of a list is left out and the rest is kept.
 */

export type DownloadState = 'queued'|'running'|'ready'|'paused'|'failed'|'unavailable'|'cancelled'|'expired'|'received';
export type DownloadAction = 'pause'|'resume'|'cancel'|'retry'|'remove';
export type DownloadOrigin = 'item'|'items'|'container'|'next';
export type DownloadArtifactKind = ''|'source'|'prepared';

export const downloadStates: readonly DownloadState[] = Object.freeze(['queued','running','ready','paused','failed','unavailable','cancelled','expired','received'] as const);
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
  id: string; downloadId?: string; versionId?:string; itemId: string; libraryId: string; profileId: string; title: DownloadTitle; quality: string; origin: DownloadOrigin;
  batchId: string; state: DownloadState; reason: string; artifact: DownloadArtifact; progress: DownloadProgress;
  actions: readonly DownloadAction[]; revision: number; createdAt: string; updatedAt: string; readyAt: string; expiresAt: string;
}>;
export type DownloadRejection = Readonly<{itemId: string; code: string}>;
export type DownloadBatch = Readonly<{batchId: string; items: readonly DownloadPreparation[]; rejected: readonly DownloadRejection[]; accepted: number; duplicate: boolean}>;
/** The address a download's bytes are fetched from. It stays good while it is used or renewed;
 * `renewAfterSeconds` is when to renew a transfer that is still unfinished. Renewing (`renew` with
 * this `token`) keeps the same address, so a transfer the device's own downloader is holding
 * carries on under it. */
export type DownloadGrantPart = Readonly<{url: string; artifact: DownloadArtifact; offsetSeconds?: number; durationSeconds?: number; startSeconds?: number; endSeconds?: number}>;
export type DownloadGrant = Readonly<{preparationId: string; itemId: string; url: string; token: string; issuedAt: string; expiresAt: string; renewAfterSeconds: number; renewed: boolean; artifact: DownloadArtifact; files?: readonly DownloadGrantPart[]}>;
/** One file that travels with a download; use the server's relative bundle path
 * under the download grant, independently of live subtitle media addresses. */
export type DownloadBundleFile = Readonly<{path: string; contentType: string}>;
export type DownloadBundleSubtitle = Readonly<{id: string; revision: number; language: string; title: string; default: boolean; forced: boolean; files: Readonly<Record<string, DownloadBundleFile>>; partFiles?: readonly Readonly<{path: string; offsetSeconds: number}>[];
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
  // Server-next supplies one menu for titles and containers. Keep its real choices in the existing view model.
  if (raw.items !== undefined) {
    if (!count(raw.titles) || !text(raw.default, 64) || raw.roomBytes !== undefined && !count(raw.roomBytes)) invalid();
    const choices = array(raw.items, 16).map(value => {
      if (!object(value) || !code(value.quality) || !text(value.label, 128) || !count(value.sizeBytes) || typeof value.estimated !== 'boolean' || typeof value.prepared !== 'boolean' || typeof value.available !== 'boolean' || !text(value.reason ?? '', 1024)) invalid();
      return Object.freeze({quality: value.quality, label: value.label, kind: value.quality === 'original' ? 'source' as const : 'optimized' as const,
        estimatedBytes: value.sizeBytes, estimated: value.estimated, available: value.available, reason: String(value.reason ?? ''), requiresPreparation: value.prepared,
        targetDisplayHeight: 0, maxVideoBitrateBps: 0, maxAudioBitrateBps: 0, preparedProfileId: '', preparedVersionId: '', preparationId: '', preparationState: ''});
    });
    if (!choices.length) invalid();
    choices.sort((a, b) => Number(b.quality === raw.default) - Number(a.quality === raw.default));
    const original = choices.find(o => o.quality === 'original');
    return Object.freeze({itemId, kind: raw.kind, options: Object.freeze(choices), policy: Object.freeze({allowDownloads: true, reason: ''}),
      source: Object.freeze({container: '', videoCodec: '', audioCodec: '', height: 0, durationSeconds: 0, bytes: original?.estimatedBytes ?? 0, available: original?.available ?? false}),
      storage: Object.freeze({remainingBytes: raw.roomBytes === undefined ? null : raw.roomBytes as number, maxPreparedBytes: 0, committedBytes: 0, retentionDays: 0})});
  }
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

export type DownloadProfileRevocation=Readonly<{claims:number;receipts:number;more:boolean}>;
export function parseDownloadProfileRevocation(raw:unknown):DownloadProfileRevocation{
 if(!object(raw)||!count(raw.claims)||raw.claims>100||!count(raw.receipts)||raw.receipts>100||typeof raw.more!=='boolean')invalid();
 return Object.freeze({claims:raw.claims,receipts:raw.receipts,more:raw.more});
}
