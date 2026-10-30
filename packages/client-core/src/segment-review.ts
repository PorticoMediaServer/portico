/**
 * The owner's intro and credits review: the server's detection status, a library's shows and films,
 * one title's detections by season, approving a season at once, measuring a title again, and single
 * marker edits through the title analysis marker route with the targets the review carries.
 * Parsers keep what they can read: a row in an unknown state or of an unknown kind is left out.
 */
import {routePath} from '../../contracts/src/route-paths.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {keepReadable} from './tolerant.ts';

export type SegmentMode = 'off' | 'standard' | 'thorough';
export type SegmentChoice = 'server' | SegmentMode;
export type SegmentCounts = Readonly<{pending: number; complete: number; failed: number; unsupported: number}>;
export type SegmentMarkerCounts = Readonly<{automatic: number; suggested: number; approved: number; dismissed: number}>;
export type SegmentLibrary = Readonly<{libraryId: string; name: string; itemKind: 'episode' | 'movie'; choice: SegmentChoice; mode: SegmentMode; files: SegmentCounts}>;
export type SegmentFailure = Readonly<{itemId: string; title: string; state: string; errorCode: string; updatedMS: number}>;
export type SegmentStatus = Readonly<{mode: SegmentMode; libraries: readonly SegmentLibrary[]; files: SegmentCounts; showsWaiting: number; catchUpComplete: boolean; lastFullPassMS: number; failures: readonly SegmentFailure[]}>;
export type SegmentTitleSummary = Readonly<{id: string; title: string; kind: 'show' | 'movie'; files: SegmentCounts; markers: SegmentMarkerCounts}>;
export type SegmentTitlePage = Readonly<{libraryId: string; titles: readonly SegmentTitleSummary[]; nextCursor: string}>;
export type SegmentKind = 'intro' | 'recap' | 'credits' | 'commercial' | 'outro';
/** Where a marker came from: other episodes' audio, a chapter title, the film's picture, the owner, or an earlier detector. */
export type SegmentBasis = 'season' | 'chapter' | 'picture' | 'owner' | 'earlier';
export type SegmentReviewMarker = Readonly<{id: string; kind: SegmentKind; startUS: string; endUS: string; confidence: number; provenance: string; basis: SegmentBasis; approved: boolean; edited: boolean; automaticSafe: boolean; viewerVisible: boolean}>;
export type SegmentReviewEntry = Readonly<{itemId: string; title: string; number: number; durationUS: string; state: string; errorCode: string; sourceId: string; sourceRevision: string; mappingRevision: string; markerRevision: number; markers: readonly SegmentReviewMarker[]}>;
export type SegmentReviewSeason = Readonly<{number: number; title: string; entries: readonly SegmentReviewEntry[]}>;
export type SegmentTitleReview = Readonly<{id: string; title: string; kind: 'show' | 'movie'; libraryId: string; mode: SegmentMode; seasons: readonly SegmentReviewSeason[]}>;

export type SegmentApi = Readonly<{request<T = unknown>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

export const segmentKinds: readonly SegmentKind[] = ['intro', 'recap', 'credits', 'commercial', 'outro'];
const modes: readonly SegmentMode[] = ['off', 'standard', 'thorough'];
const choices: readonly SegmentChoice[] = ['server', 'off', 'standard', 'thorough'];
const bases: readonly SegmentBasis[] = ['season', 'chapter', 'picture', 'owner', 'earlier'];

const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max;
const filled = (v: unknown, max = 512): v is string => text(v, max) && v.length > 0;
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const us = (v: unknown): v is string => typeof v === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(v) && Number.isSafeInteger(Number(v));
function fail(): never { throw new Error(unreadableServerResponse); }
function one<T extends string>(v: unknown, allowed: readonly T[]): T { if (!allowed.includes(v as T)) fail(); return v as T; }
function list(v: unknown, max: number): unknown[] { if (!Array.isArray(v) || v.length > max) fail(); return v; }

/** Admin answers arrive as {protocolVersion, serverId, result}. */
function result(raw: unknown): Record<string, unknown> {
  if (!object(raw) || !filled(raw.protocolVersion, 32) || !filled(raw.serverId, 256) || !object(raw.result)) fail();
  return raw.result;
}

function counts(v: unknown): SegmentCounts {
  if (!object(v) || !count(v.pending) || !count(v.complete) || !count(v.failed) || !count(v.unsupported)) fail();
  return Object.freeze({pending: v.pending, complete: v.complete, failed: v.failed, unsupported: v.unsupported});
}

function markerCounts(v: unknown): SegmentMarkerCounts {
  if (!object(v) || !count(v.automatic) || !count(v.suggested) || !count(v.approved) || !count(v.dismissed)) fail();
  return Object.freeze({automatic: v.automatic, suggested: v.suggested, approved: v.approved, dismissed: v.dismissed});
}

export function parseSegmentStatus(raw: unknown): SegmentStatus {
  const v = result(raw);
  if (!count(v.showsWaiting) || typeof v.catchUpComplete !== 'boolean' || !count(v.lastFullPassMS)) fail();
  const libraries = keepReadable('segments', 'libraries', list(v.libraries, 256), l => {
    if (!object(l) || !filled(l.libraryId, 256) || !text(l.name, 512)) fail();
    return Object.freeze({libraryId: l.libraryId, name: l.name, itemKind: one(l.itemKind, ['episode', 'movie'] as const), choice: one(l.choice, choices), mode: one(l.mode, modes), files: counts(l.files)});
  });
  const failures = keepReadable('segments', 'failures', list(v.failures, 32), f => {
    if (!object(f) || !text(f.itemId, 256) || !text(f.title, 1024) || !filled(f.state, 32) || !text(f.errorCode, 128) || !count(f.updatedMS)) fail();
    return Object.freeze({itemId: f.itemId, title: f.title, state: f.state, errorCode: f.errorCode, updatedMS: f.updatedMS});
  });
  return Object.freeze({mode: one(v.mode, modes), libraries: Object.freeze(libraries), files: counts(v.files), showsWaiting: v.showsWaiting, catchUpComplete: v.catchUpComplete, lastFullPassMS: v.lastFullPassMS, failures: Object.freeze(failures)});
}

export function parseSegmentTitles(raw: unknown): SegmentTitlePage {
  const v = result(raw);
  if (!filled(v.libraryId, 256) || !text(v.nextCursor, 8192)) fail();
  const titles = keepReadable('segments', 'titles', list(v.titles, 100), t => {
    if (!object(t) || !filled(t.id, 256) || !text(t.title, 1024)) fail();
    return Object.freeze({id: t.id, title: t.title, kind: one(t.kind, ['show', 'movie'] as const), files: counts(t.files), markers: markerCounts(t.markers)});
  });
  return Object.freeze({libraryId: v.libraryId, titles: Object.freeze(titles), nextCursor: v.nextCursor});
}

function parseMarker(m: unknown, duration: number): SegmentReviewMarker {
  if (!object(m) || !filled(m.id, 128) || !us(m.startUS) || !us(m.endUS) || Number(m.endUS) <= Number(m.startUS) || Number(m.endUS) > duration || typeof m.confidence !== 'number' || !(m.confidence >= 0 && m.confidence <= 1) || !text(m.provenance, 512)) fail();
  if (typeof m.approved !== 'boolean' || typeof m.edited !== 'boolean' || typeof m.automaticSafe !== 'boolean' || typeof m.viewerVisible !== 'boolean') fail();
  return Object.freeze({id: m.id, kind: one(m.kind, segmentKinds), startUS: m.startUS, endUS: m.endUS, confidence: m.confidence, provenance: m.provenance, basis: one(m.basis, bases), approved: m.approved, edited: m.edited, automaticSafe: m.automaticSafe, viewerVisible: m.viewerVisible});
}

export function parseSegmentTitle(raw: unknown): SegmentTitleReview {
  const v = result(raw);
  if (!filled(v.id, 256) || !text(v.title, 1024) || !filled(v.libraryId, 256)) fail();
  const seasons = list(v.seasons, 512).map(s => {
    if (!object(s) || typeof s.number !== 'number' || !Number.isSafeInteger(s.number) || s.number < -1 || !text(s.title, 512)) fail();
    const entries = keepReadable('segments', 'entries', list(s.entries, 4096), e => {
      if (!object(e) || !filled(e.itemId, 256) || !text(e.title, 1024) || typeof e.number !== 'number' || !filled(e.state, 32) || !text(e.errorCode, 128)) fail();
      if (!text(e.sourceId, 256) || !text(e.sourceRevision, 256) || !text(e.mappingRevision, 256) || !count(e.markerRevision) || !(e.durationUS === '' || us(e.durationUS))) fail();
      const duration = e.durationUS === '' ? 0 : Number(e.durationUS);
      const markers = keepReadable('segments', 'markers', list(e.markers, 512), m => parseMarker(m, duration));
      return Object.freeze({itemId: e.itemId, title: e.title, number: e.number, durationUS: e.durationUS, state: e.state, errorCode: e.errorCode, sourceId: e.sourceId, sourceRevision: e.sourceRevision, mappingRevision: e.mappingRevision, markerRevision: e.markerRevision, markers: Object.freeze(markers)});
    });
    return Object.freeze({number: s.number, title: s.title, entries: Object.freeze(entries)});
  });
  return Object.freeze({id: v.id, title: v.title, kind: one(v.kind, ['show', 'movie'] as const), libraryId: v.libraryId, mode: one(v.mode, modes), seasons: Object.freeze(seasons)});
}

export const readSegmentStatus = async (api: SegmentApi, signal?: AbortSignal) => parseSegmentStatus(await api.request(routePath('get_admin_segments'), 'GET', undefined, signal));
export function readSegmentTitles(api: SegmentApi, libraryId: string, cursor = '', signal?: AbortSignal): Promise<SegmentTitlePage> {
  const q = new URLSearchParams({libraryId});
  if (cursor) q.set('cursor', cursor);
  return api.request(routePath('get_admin_segments_titles') + '?' + q, 'GET', undefined, signal).then(parseSegmentTitles);
}
export const readSegmentTitle = async (api: SegmentApi, id: string, signal?: AbortSignal) => parseSegmentTitle(await api.request(routePath('get_admin_segments_titles_id', {id}), 'GET', undefined, signal));

function changed(raw: unknown): number {
  const v = result(raw);
  if (!count(v.changed)) fail();
  return v.changed;
}

/** Approve a title's detections of these kinds: one season, or every season when season is omitted. */
export const approveSegments = async (api: SegmentApi, id: string, kinds: readonly SegmentKind[], season?: number) => changed(await api.request(routePath('post_admin_segments_titles_id_approve', {id}), 'POST', season === undefined ? {kinds} : {kinds, season}));
/** Measure a title's files again. The owner's approvals, adjustments and dismissals are kept. */
export const redetectSegments = async (api: SegmentApi, id: string) => changed(await api.request(routePath('post_admin_segments_titles_id_redetect', {id}), 'POST', {}));

export type SegmentEdit = Readonly<{action: 'create' | 'edit' | 'approve' | 'dismiss'; markerId?: string; kind?: SegmentKind; startUS?: string; endUS?: string}>;
let sequence = 0;
export const segmentRequestId = (): string => globalThis.crypto?.randomUUID?.() ?? `segment-${Date.now()}-${++sequence}-${Math.random().toString(36).slice(2)}`;

/** One marker change on one entry, fenced to the source and marker revision the review read. */
export function editSegmentMarker(api: SegmentApi, entry: SegmentReviewEntry, edit: SegmentEdit, requestId: string = segmentRequestId()): Promise<unknown> {
  if (!entry.sourceId || !entry.sourceRevision || !entry.mappingRevision) return Promise.reject(new Error(unreadableServerResponse));
  return api.request(routePath('post_items_id_analysis_markers', {id: entry.itemId}), 'POST', {
    sourceId: entry.sourceId, sourceRevision: entry.sourceRevision, mappingRevision: entry.mappingRevision, sessionId: '', generation: 0,
    expectedRevision: entry.markerRevision, requestId, action: edit.action, markerId: edit.markerId ?? '', kind: edit.kind ?? '', title: '', startUS: edit.startUS ?? '', endUS: edit.endUS ?? '',
  });
}

/** A position as the review shows it: m:ss.s (h:mm:ss.s past an hour). */
export function segmentClock(us: string): string {
  if (!/^(0|[1-9][0-9]{0,18})$/.test(us)) return '';
  const tenths = Math.round(Number(us) / 100000);
  const s = Math.floor(tenths / 10), frac = tenths % 10;
  const h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60, sec = s % 60;
  const tail = `${String(sec).padStart(2, '0')}.${frac}`;
  return h ? `${h}:${String(m).padStart(2, '0')}:${tail}` : `${m}:${tail}`;
}

/** Reads what an owner typed for a position (1:23, 1:23.4, 0:01:23.4 or 83.4) as microseconds, or null. */
export function parseSegmentClock(text: string): string | null {
  const v = text.trim();
  const match = /^(?:(\d{1,3}):)?(?:(\d{1,3}):)?(\d{1,6})(?:\.(\d{1,3}))?$/.exec(v);
  if (!match) return null;
  const parts = [match[1], match[2]].filter((x): x is string => x !== undefined).map(Number);
  const seconds = Number(match[3]);
  if (parts.length && seconds >= 60) return null;
  if (parts.length === 2 && parts[1]! >= 60) return null;
  const whole = (parts.length === 2 ? parts[0]! * 3600 + parts[1]! * 60 : parts.length === 1 ? parts[0]! * 60 : 0) + seconds;
  const us = whole * 1e6 + Number((match[4] ?? '').padEnd(3, '0')) * 1000;
  return Number.isSafeInteger(us) ? String(us) : null;
}
