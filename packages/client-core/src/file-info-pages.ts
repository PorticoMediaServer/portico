import {parseDiscMediaFacts, type DiscMediaFacts} from './disc-media-facts.ts';
import type {DetailScope, DetailTarget, FileInfoDiscTitle, TitleFile} from './detail.ts';
import {FileInfoWindowError, type FileInfoWindowFacts} from './file-info-windows.ts';
import type {PreparationResponse, PreparingRead} from './read-preparation.ts';

/** The accepted Detail a FileInfo opening was made from, and its opaque `fileInfoOpening`.
 * Every lane echoes that Detail unchanged; a window carrying another revision belongs to
 * another opening and is refused. A newer Detail is a new opening, never adopted silently. */
export type FileInfoOpening = Readonly<{
  token: string; scope: DetailScope; target: DetailTarget; revision: Readonly<{catalog: number; viewer: number}>;
}>;
export type FileInfoManage = Readonly<{delete: boolean; split: boolean; merge: boolean}>;
/** Facts of the complete accepted surface, never of the resident window. */
export type FileInfoSurface = Readonly<{
  fileCount: number; eligibleFileCount: number; groupCount: number;
  canSplit: boolean; hasDiscColumns: boolean; ownerDiscOccurrence?: string; manage?: FileInfoManage;
}>;
export type FileInfoVideo = NonNullable<TitleFile['video']> & Readonly<{profile?: string}>;
/** A byte count: `value` when it is a safe JS integer, otherwise only the server's exact decimal. */
export type FileInfoBytes = Readonly<{value?: number; exact?: string}>;
/** One exact file occurrence. It grants nothing: every command revalidates current rights.
 * `duration` (seconds) is absent when the server could not represent it (`unrepresentable`). */
export type FileInfoFileRow = Readonly<{
  occurrence: string; itemId: string; id: string; path?: string; size: FileInfoBytes; container: string;
  duration?: number; durationUnrepresentable?: true; bitRate?: number; available: boolean; partIndex: number; edition?: string;
  video?: FileInfoVideo; disc?: DiscMediaFacts; audioCount: number; subtitleCount: number;
}>;
/** One version: the exact Options grouping, from its accepted original head. */
export type FileInfoGroupRow = Readonly<{
  id: string; headOccurrence: string; ordinal: number; memberCount: number; edition?: string; container: string;
  size: FileInfoBytes; durationMs?: number; durationUnrepresentable?: true; video?: FileInfoVideo; disc?: DiscMediaFacts;
  audioCount: number; subtitleCount: number;
}>;
export type FileInfoTrackRow = Readonly<{
  ordinal: number; codec: string; language?: string; title?: string; channels?: number; channelLayout?: string; objectAudio?: string;
  bitRate?: number; default?: boolean; forced?: boolean; external?: boolean; profile?: string; sampleRate?: number; bitDepth?: number;
  commentary?: boolean; hearingImpaired?: boolean;
}>;
/** An offered track of a version's head. Its id names the exact accepted head track; it is not a playback grant. */
export type FileInfoHeadTrackRow = FileInfoTrackRow & Readonly<{id: string}>;
export type FileInfoSourceRow = Readonly<{
  occurrence: string; itemId: string; id: string; container: string; videoCodec: string; audioCodec: string;
  width: number; height: number; duration?: number; durationUnrepresentable?: true;
}>;
type Basis = Readonly<{basis: string; window: FileInfoWindowFacts}>;
export type FileInfoFilesPage = Basis & Readonly<{itemKind: string; surface: FileInfoSurface; files: readonly FileInfoFileRow[]}>;
export type FileInfoGroupsPage = Basis & Readonly<{surface: FileInfoSurface; groups: readonly FileInfoGroupRow[]}>;
export type FileInfoMembersPage = Basis & Readonly<{groupId: string; total: number; members: readonly FileInfoFileRow[]}>;
export type FileInfoTrackLane = 'audio' | 'subtitles';
export type FileInfoTracksPage = Basis & Readonly<{lane: FileInfoTrackLane; occurrence: string; total: number; tracks: readonly FileInfoTrackRow[]}>;
export type FileInfoHeadTracksPage = Basis & Readonly<{lane: FileInfoTrackLane; groupId: string; occurrence: string; total: number; tracks: readonly FileInfoHeadTrackRow[]}>;
export type FileInfoSourcesPage = Basis & Readonly<{total: number; sources: readonly FileInfoSourceRow[]}>;
export type FileInfoDiscPage = Basis & Readonly<{occurrence: string; discKind: 'bluray' | 'dvd'; total: number; titles: readonly FileInfoDiscTitle[]}>;

/** Supplements a command's own admission with the exact occurrence of the accepted opening. It
 * authorizes nothing by itself: the server rechecks owner, custody and selection live. */
export type FileInfoActionProof = Readonly<{opening: string; occurrence: string}>;
export const FILE_INFO_DEFAULT_LIMIT = 40, FILE_INFO_MAXIMUM_LIMIT = 100;
const obj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const opaque = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9._~-]{1,1024}$/.test(v);
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0;
const decimal = (v: unknown): v is string => typeof v === 'string' && /^(0|[1-9][0-9]{0,39})$/.test(v);
/** Short identifiers and codes keep their bound and control-character rule. */
const short = (v: unknown, max = 256): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
/** Legal stored text (paths, editions, profiles, track titles) arrives whole at any length; only NUL is refused. */
const text = (v: unknown): v is string => typeof v === 'string' && !v.includes('\x00');
function invalid(response?: PreparationResponse): never { throw new FileInfoWindowError('invalid_file_info_window', response); }

function rows(value: unknown, window: FileInfoWindowFacts, response?: PreparationResponse): unknown[] {
  if (!Array.isArray(value) || value.length !== window.returned) invalid(response);
  return value;
}
function windowFacts(value: unknown, basis: string, response?: PreparationResponse): FileInfoWindowFacts {
  if (!obj(value) || !opaque(value.queryId) || !count(value.limit) || value.limit < 1 || value.limit > FILE_INFO_MAXIMUM_LIMIT
    || !count(value.returned) || value.returned > value.limit) invalid(response);
  const cursor = (key: 'nextCursor' | 'previousCursor' | 'reloadCursor') => {
    const token = value[key];
    if (token === undefined || token === '') return undefined;
    if (!opaque(token)) invalid(response);
    return token;
  };
  const next = cursor('nextCursor'), previous = cursor('previousCursor'), reload = cursor('reloadCursor');
  return Object.freeze({queryId: value.queryId, basis, limit: value.limit, returned: value.returned, ...(next ? {next} : {}), ...(previous ? {previous} : {}), ...(reload ? {reload} : {})});
}
function scoped(value: unknown, opening: FileInfoOpening): boolean {
  return obj(value) && value.serverId === opening.scope.serverId && value.libraryId === opening.target.libraryId
    && value.itemId === opening.target.itemId && short(value.viewerFence) && !!value.viewerFence;
}
function sameDetail(value: unknown, opening: FileInfoOpening): boolean {
  return obj(value) && scoped(value.scope, opening) && obj(value.revision)
    && value.revision.catalog === opening.revision.catalog && value.revision.viewer === opening.revision.viewer;
}
/** The common envelope: this server, item and audience, the opening's own Detail, the lane's kind. */
function envelope(raw: unknown, opening: FileInfoOpening, kind: string, response?: PreparationResponse): Record<string, unknown> & Basis {
  if (!obj(raw) || raw.kind !== kind || !scoped(raw.scope, opening) || !sameDetail(raw.detail, opening) || !opaque(raw.basis)) invalid(response);
  return Object.assign({}, raw, {basis: raw.basis, window: windowFacts(raw.window, raw.basis, response)});
}
function total(page: Record<string, unknown> & Basis, response?: PreparationResponse): number {
  if (!count(page.total) || page.window.returned > page.total) invalid(response);
  return page.total;
}
function manage(value: unknown, response?: PreparationResponse): FileInfoManage | undefined {
  if (value === undefined) return undefined;
  if (!obj(value) || typeof value.delete !== 'boolean' || typeof value.split !== 'boolean' || typeof value.merge !== 'boolean') invalid(response);
  return Object.freeze({delete: value.delete, split: value.split, merge: value.merge});
}
function surface(value: unknown, response?: PreparationResponse): FileInfoSurface {
  if (!obj(value) || !count(value.fileCount) || !count(value.eligibleFileCount) || !count(value.groupCount)
    || typeof value.canSplit !== 'boolean' || typeof value.hasDiscColumns !== 'boolean'
    || value.ownerDiscOccurrence !== undefined && !occurrence(value.ownerDiscOccurrence)) invalid(response);
  const owner = manage(value.manage, response);
  // Owner-only facts without the owner's manage block mean the audience fence was not applied.
  if (!owner && (value.canSplit || value.ownerDiscOccurrence !== undefined)) invalid(response);
  return Object.freeze({fileCount: value.fileCount, eligibleFileCount: value.eligibleFileCount, groupCount: value.groupCount, canSplit: value.canSplit, hasDiscColumns: value.hasDiscColumns,
    ...(value.ownerDiscOccurrence !== undefined ? {ownerDiscOccurrence: value.ownerDiscOccurrence as string} : {}), ...(owner ? {manage: owner} : {})});
}
/** An opaque occurrence: one exact parent/child/link/asset of the opening. Compared, never parsed. */
export function occurrence(value: unknown): value is string {
  return short(value, 1024) && value.length > 0 && !/\s/.test(value);
}
/** Exactly one of a safe number or its exact decimal; absent when the server sends neither. */
function bytes(value: unknown, exact: unknown, response?: PreparationResponse): FileInfoBytes {
  if (value !== undefined && exact !== undefined || value !== undefined && !count(value) || exact !== undefined && !decimal(exact)) invalid(response);
  return Object.freeze(value !== undefined ? {value: value as number} : exact !== undefined ? {exact: exact as string} : {});
}
/** `durationState` is absent or `unrepresentable`, and an unrepresentable length is never sent. */
function length(value: unknown, state: unknown, read: (v: unknown) => boolean, response?: PreparationResponse): {value?: number; unrepresentable?: true} {
  if (state !== undefined && state !== 'unrepresentable') invalid(response);
  if (state === 'unrepresentable') { if (value !== undefined) invalid(response); return {unrepresentable: true}; }
  if (value === undefined) return {};
  if (!read(value)) invalid(response);
  return {value: value as number};
}
function video(value: unknown, response?: PreparationResponse): FileInfoVideo | undefined {
  if (value === undefined) return undefined;
  if (!obj(value) || !short(value.codec, 64) || !count(value.width) || !count(value.height) || value.profile !== undefined && !text(value.profile)) invalid(response);
  return Object.freeze({codec: value.codec, width: value.width, height: value.height,
    ...(short(value.dynamicRange, 32) && value.dynamicRange ? {dynamicRange: value.dynamicRange} : {}), ...(value.hdr10Plus === true ? {hdr10Plus: true} : {}),
    ...(finite(value.frameRate) && value.frameRate > 0 ? {frameRate: value.frameRate} : {}), ...(count(value.bitDepth) && value.bitDepth > 0 ? {bitDepth: value.bitDepth} : {}),
    ...(count(value.bitRate) && value.bitRate > 0 ? {bitRate: value.bitRate} : {}), ...(value.profile ? {profile: value.profile as string} : {})}) as FileInfoVideo;
}
function disc(value: unknown, owner: boolean, response?: PreparationResponse): DiscMediaFacts | undefined {
  if (value === undefined) return undefined;
  const facts = parseDiscMediaFacts(value);
  if (!owner || !facts) invalid(response);
  return facts;
}
function file(value: unknown, owner: boolean, response?: PreparationResponse): FileInfoFileRow {
  if (!obj(value) || !occurrence(value.occurrence) || !short(value.itemId) || !value.itemId || !short(value.id) || !value.id
    || !short(value.container ?? '', 64) || typeof value.available !== 'boolean' || !count(value.partIndex) || !count(value.audioCount) || !count(value.subtitleCount)
    || value.bitRate !== undefined && !count(value.bitRate)
    || value.path !== undefined && (!owner || !text(value.path) || !value.path) || value.edition !== undefined && !text(value.edition)) invalid(response);
  const size = bytes(value.size, value.sizeExact, response), seconds = length(value.duration, value.durationState, finite, response);
  const facts = video(value.video, response), held = disc(value.disc, owner, response);
  return Object.freeze({occurrence: value.occurrence, itemId: value.itemId, id: value.id, ...(value.path ? {path: value.path as string} : {}), size,
    container: (value.container ?? '') as string, ...(seconds.value !== undefined ? {duration: seconds.value} : {}), ...(seconds.unrepresentable ? {durationUnrepresentable: true as const} : {}),
    ...(value.bitRate ? {bitRate: value.bitRate as number} : {}), available: value.available, partIndex: value.partIndex, ...(value.edition ? {edition: value.edition as string} : {}),
    ...(facts ? {video: facts} : {}), ...(held ? {disc: held} : {}), audioCount: value.audioCount, subtitleCount: value.subtitleCount});
}
function group(value: unknown, owner: boolean, response?: PreparationResponse): FileInfoGroupRow {
  if (!obj(value) || !opaque(value.id) || !occurrence(value.headOccurrence) || !count(value.ordinal) || value.ordinal < 1 || !count(value.memberCount) || value.memberCount < 1
    || !short(value.container ?? '', 64) || !count(value.audioCount) || !count(value.subtitleCount) || value.edition !== undefined && !text(value.edition)) invalid(response);
  const size = bytes(value.sizeBytes, value.sizeBytesExact, response), ms = length(value.durationMs, value.durationState, count, response);
  const facts = video(value.video, response), held = disc(value.disc, owner, response);
  return Object.freeze({id: value.id, headOccurrence: value.headOccurrence, ordinal: value.ordinal, memberCount: value.memberCount, ...(value.edition ? {edition: value.edition as string} : {}),
    container: (value.container ?? '') as string, size, ...(ms.value !== undefined ? {durationMs: ms.value} : {}), ...(ms.unrepresentable ? {durationUnrepresentable: true as const} : {}),
    ...(facts ? {video: facts} : {}), ...(held ? {disc: held} : {}), audioCount: value.audioCount, subtitleCount: value.subtitleCount});
}
function track(value: unknown, response?: PreparationResponse): FileInfoTrackRow {
  if (!obj(value) || !count(value.ordinal) || value.ordinal < 1 || !short(value.codec ?? '', 64)
    || value.title !== undefined && !text(value.title) || value.profile !== undefined && !text(value.profile)) invalid(response);
  const label = (v: unknown, max: number) => short(v, max) && v ? v : undefined, positive = (v: unknown) => count(v) && v > 0 ? v : undefined, flag = (v: unknown) => v === true ? true : undefined;
  const fields = {language: label(value.language, 64), channelLayout: label(value.channelLayout, 64), objectAudio: label(value.objectAudio, 32), channels: positive(value.channels),
    bitRate: positive(value.bitRate), sampleRate: positive(value.sampleRate), bitDepth: positive(value.bitDepth), title: value.title ? value.title as string : undefined,
    profile: value.profile ? value.profile as string : undefined, default: flag(value.default), forced: flag(value.forced), external: flag(value.external),
    commentary: flag(value.commentary), hearingImpaired: flag(value.hearingImpaired)};
  return Object.freeze({ordinal: value.ordinal, codec: (value.codec ?? '') as string, ...Object.fromEntries(Object.entries(fields).filter(([, v]) => v !== undefined))}) as FileInfoTrackRow;
}
/** Ordinals climb strictly through a window: a gap is another window, a repeat is never valid. */
function ordered<T extends {ordinal: number}>(list: readonly T[], most: number, response?: PreparationResponse): readonly T[] {
  for (let i = 0; i < list.length; i++) if (list[i]!.ordinal > most || i && list[i]!.ordinal <= list[i - 1]!.ordinal) invalid(response);
  return Object.freeze(list);
}
function unique<T>(list: readonly T[], key: (value: T) => string, response?: PreparationResponse): readonly T[] {
  if (new Set(list.map(key)).size !== list.length) invalid(response);
  return Object.freeze(list);
}
function lane(value: unknown, response?: PreparationResponse): FileInfoTrackLane {
  if (value !== 'audio' && value !== 'subtitles') invalid(response);
  return value;
}

export function parseFileInfoFilesPage(raw: unknown, opening: FileInfoOpening, response?: PreparationResponse): FileInfoFilesPage {
  const page = envelope(raw, opening, 'file-info-files', response), facts = surface(page.surface, response);
  if (!short(page.itemKind, 64) || !page.itemKind) invalid(response);
  const files = unique(rows(page.files, page.window, response).map(value => file(value, !!facts.manage, response)), value => value.occurrence, response);
  return Object.freeze({basis: page.basis, window: page.window, itemKind: page.itemKind as string, surface: facts, files});
}
export function parseFileInfoGroupsPage(raw: unknown, opening: FileInfoOpening, response?: PreparationResponse): FileInfoGroupsPage {
  const page = envelope(raw, opening, 'file-info-groups', response), facts = surface(page.surface, response);
  const groups = ordered(rows(page.groups, page.window, response).map(value => group(value, !!facts.manage, response)), facts.groupCount, response);
  return Object.freeze({basis: page.basis, window: page.window, surface: facts, groups: unique(groups, value => value.id, response)});
}
export function parseFileInfoMembersPage(raw: unknown, opening: FileInfoOpening, groupId: string, owner: boolean, response?: PreparationResponse): FileInfoMembersPage {
  const page = envelope(raw, opening, 'file-info-members', response);
  if (page.groupId !== groupId) invalid(response);
  const members = unique(rows(page.members, page.window, response).map(value => file(value, owner, response)), value => value.occurrence, response);
  return Object.freeze({basis: page.basis, window: page.window, groupId, total: total(page, response), members});
}
/** Raw stored tracks of one exact occurrence (web reads them on a version's head). */
export function parseFileInfoTracksPage(raw: unknown, opening: FileInfoOpening, kind: FileInfoTrackLane, at: string, response?: PreparationResponse): FileInfoTracksPage {
  const page = envelope(raw, opening, 'file-info-tracks', response), all = total(page, response);
  if (lane(page.lane, response) !== kind || page.occurrence !== at) invalid(response);
  return Object.freeze({basis: page.basis, window: page.window, lane: kind, occurrence: at, total: all, tracks: ordered(rows(page.tracks, page.window, response).map(value => track(value, response)), all, response)});
}
/** The offered tracks of one version's head (native reads these). */
export function parseFileInfoHeadTracksPage(raw: unknown, opening: FileInfoOpening, kind: FileInfoTrackLane, groupId: string, response?: PreparationResponse): FileInfoHeadTracksPage {
  const page = envelope(raw, opening, 'file-info-head-tracks', response), all = total(page, response);
  if (lane(page.lane, response) !== kind || page.groupId !== groupId || !occurrence(page.occurrence)) invalid(response);
  const tracks = rows(page.tracks, page.window, response).map(value => {
    if (!obj(value) || !opaque(value.id)) invalid(response);
    return Object.freeze({...track(value, response), id: value.id}) as FileInfoHeadTrackRow;
  });
  return Object.freeze({basis: page.basis, window: page.window, lane: kind, groupId, occurrence: page.occurrence as string, total: all, tracks: unique(ordered(tracks, all, response), value => value.id, response)});
}
export function parseFileInfoSourcesPage(raw: unknown, opening: FileInfoOpening, response?: PreparationResponse): FileInfoSourcesPage {
  const page = envelope(raw, opening, 'file-info-sources', response);
  const sources = rows(page.sources, page.window, response).map(value => {
    if (!obj(value) || !occurrence(value.occurrence) || !short(value.itemId) || !value.itemId || !short(value.id) || !value.id
      || !short(value.container ?? '', 64) || !short(value.videoCodec ?? '', 64) || !short(value.audioCodec ?? '', 64) || !count(value.width) || !count(value.height)) invalid(response);
    const seconds = length(value.duration, value.durationState, finite, response);
    return Object.freeze({occurrence: value.occurrence, itemId: value.itemId, id: value.id, container: (value.container ?? '') as string, videoCodec: (value.videoCodec ?? '') as string,
      audioCodec: (value.audioCodec ?? '') as string, width: value.width, height: value.height,
      ...(seconds.value !== undefined ? {duration: seconds.value} : {}), ...(seconds.unrepresentable ? {durationUnrepresentable: true as const} : {})});
  });
  return Object.freeze({basis: page.basis, window: page.window, total: total(page, response), sources: unique(sources, value => value.occurrence, response)});
}
/** Owner only: the titles of the surface's first successful owner-disc file. */
export function parseFileInfoDiscPage(raw: unknown, opening: FileInfoOpening, at: string, response?: PreparationResponse): FileInfoDiscPage {
  const page = envelope(raw, opening, 'file-info-disc-titles', response);
  if (page.occurrence !== at || (page.discKind !== 'bluray' && page.discKind !== 'dvd')) invalid(response);
  const titles = rows(page.titles, page.window, response).map(value => {
    if (!obj(value) || !count(value.number) || value.number < 1 || !count(value.durationMs) || !count(value.chapters) || !count(value.angles)) invalid(response);
    return Object.freeze({number: value.number, durationMs: value.durationMs, chapters: value.chapters, angles: value.angles});
  });
  return Object.freeze({basis: page.basis, window: page.window, occurrence: at, discKind: page.discKind, total: total(page, response),
    titles: unique(titles, value => String(value.number), response)});
}
/** P2-29 amendment: `detail` may be absent only on the opening preparation, before any Detail
 * was accepted for the item (`opening`); every later preparation echoes the original Detail. */
export function parseFileInfoPreparing(raw: unknown, opening: FileInfoOpening, response?: PreparationResponse, detailMayBeAbsent = false): PreparingRead {
  if (!obj(raw) || raw.kind !== 'file-info-preparing' || raw.state !== 'preparing' || !opaque(raw.queryId) || !count(raw.retryAfterMs)
    || !(raw.detail === undefined && detailMayBeAbsent || sameDetail(raw.detail, opening))) invalid(response);
  return Object.freeze({queryId: raw.queryId, retryAfterMs: raw.retryAfterMs});
}
/** The handle of any ready window, read before its rows so a malformed page is still released. */
export function fileInfoReadyQueryId(raw: unknown, response?: PreparationResponse): string | undefined {
  if (!obj(raw) || !obj(raw.window) || !opaque(raw.window.queryId)) invalid(response);
  return raw.window.queryId;
}
/** A display figure for a byte count; an exact decimal past 2^53 is rounded only for display. */
export function fileInfoBytesValue(size: FileInfoBytes): number | undefined {
  return size.value ?? (size.exact !== undefined ? Number(size.exact) : undefined);
}
