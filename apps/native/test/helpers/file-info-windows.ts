import {FileInfoSession} from '../../../../packages/client-core/src/file-info-session.ts';
import type {FileInfoOpening} from '../../../../packages/client-core/src/file-info-pages.ts';

/** Frozen FileInfo wire fixtures: the envelopes the server sends for one opening of one movie. */
export const opening: FileInfoOpening = {token: 'o1', scope: {serverId: 'server', viewerId: 'viewer'}, target: {libraryId: 'library', itemId: 'movie'}, revision: {catalog: 2, viewer: 3}};
const fence = {serverId: 'server', libraryId: 'library', itemId: 'movie', viewerFence: 'viewer'};
export const envelope = (kind: string, queryId: string, over: Record<string, unknown> = {}, window: Record<string, unknown> = {}) => ({
  scope: fence, detail: {scope: fence, revision: opening.revision}, basis: 'b1', kind, window: {queryId, limit: 40, returned: 1, ...window}, ...over});
export const viewerSurface = {fileCount: 3, eligibleFileCount: 3, groupCount: 2, canSplit: false, hasDiscColumns: false};
export const ownerSurface = {...viewerSurface, canSplit: true, manage: {delete: true, split: true, merge: true}};
export const groupRow = (ordinal: number, over: Record<string, unknown> = {}) => ({id: `g${ordinal}`, headOccurrence: `occ-g${ordinal}`, ordinal, memberCount: 1, container: 'mkv',
  sizeBytes: 2147483648, durationMs: 600000, video: {codec: 'hevc', width: 3840, height: 2160, dynamicRange: 'hdr10'}, audioCount: 0, subtitleCount: 0, ...over});
export const memberRow = (occurrence: string, id: string, over: Record<string, unknown> = {}) => ({occurrence, itemId: 'movie', id, size: 1024, container: 'mkv', duration: 300, available: true, partIndex: 0, audioCount: 0, subtitleCount: 0, ...over});
export const groupsPage = (queryId: string, surface: Record<string, unknown>, groups: unknown[], window: Record<string, unknown> = {}) =>
  envelope('file-info-groups', queryId, {surface, groups}, {returned: groups.length, ...window});
export const headTracksPage = (queryId: string, group: string, lane: 'audio' | 'subtitles', tracks: unknown[], window: Record<string, unknown> = {}, total = tracks.length) =>
  envelope('file-info-head-tracks', queryId, {lane, groupId: group, occurrence: `occ-${group}`, total, tracks}, {returned: tracks.length, ...window});
export const membersPage = (queryId: string, group: string, members: unknown[], window: Record<string, unknown> = {}) =>
  envelope('file-info-members', queryId, {groupId: group, total: members.length, members}, {returned: members.length, ...window});

export type RawReply = Readonly<{status: number; headers: Record<string, string>; body?: unknown}>;
export const reply = (body: unknown, status = 200): RawReply => ({status, headers: {}, body});
/** A FileInfoSession over a fake `requestRaw`: `respond` answers each GET by path, DELETE gives the lease back. */
export function fakeSession(respond: (path: string) => unknown, over: {opening?: FileInfoOpening; limit?: number} = {}) {
  const calls: string[] = [];
  const session = new FileInfoSession({opening: over.opening ?? opening, isCurrent: () => true, limit: over.limit,
    errorFromResponse: raw => new Error(`status ${raw.status}`),
    api: {requestRaw(path, method) { calls.push(`${method} ${path}`); return Promise.resolve(method === 'DELETE' ? reply(undefined, 204) : reply(respond(path))); }}});
  return {session, calls};
}
