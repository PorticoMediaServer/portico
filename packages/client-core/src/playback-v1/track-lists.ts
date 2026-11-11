import type {ItemSubtitles, LyricsPage, SubtitleOffset} from '../../../contracts/src/next/api.ts';
import type {ContentScope} from '../library-content.ts';
import type {LyricRevision, LyricsTarget, LyricsView} from '../lyrics.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

/** A title's subtitles (in its files or beside them), and the timing nudge this profile gave them. */
export const readItemSubtitles = (api: ServerRequester, itemId: string, signal?: AbortSignal): Promise<ItemSubtitles> =>
  callServer(api, 'GET /v1/items/{id}/subtitles', {id: itemId, signal});

/** Keeps a timing nudge for a title's subtitles, for this profile. Positive shows them later. */
export const saveSubtitleOffset = (api: ServerRequester, itemId: string, offsetMs: number, signal?: AbortSignal): Promise<SubtitleOffset> =>
  callServer(api, 'PUT /v1/items/{id}/subtitles/offset', {id: itemId, body: {offsetMs: Math.round(offsetMs)}, signal});

/** A song's lyrics: each set with its lines in order, timed or not. */
export const readItemLyrics = (api: ServerRequester, itemId: string, signal?: AbortSignal): Promise<LyricsPage> =>
  callServer(api, 'GET /v1/items/{id}/lyrics', {id: itemId, signal});

const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);

/**
 * The lyrics screen's view of a song's lyrics as the server lists them (`GET /v1/items/{id}/lyrics`):
 * the first set is the one shown. The server does not say how long the song is here, so a line can
 * be sought to anywhere in the first day of it.
 */
export function lyricsView(raw: unknown, scope: ContentScope, target: LyricsTarget): LyricsView {
  if (!obj(raw) || !Array.isArray(raw.items)) throw new Error('This server sent lyrics Portico couldn’t read.');
  const resources = raw.items.filter(obj).flatMap((v): LyricRevision[] => {
    if (typeof v.id !== 'string' || typeof v.text !== 'string' || !Array.isArray(v.lines)) return [];
    const format = v.format === 'lrc' ? 'lrc' as const : 'text' as const, offsetMs = typeof v.offsetMs === 'number' && Number.isSafeInteger(v.offsetMs) ? v.offsetMs : 0;
    const lines = v.lines.filter(obj).map(line => Object.freeze({atMs: format === 'lrc' && typeof line.atMs === 'number' ? line.atMs : null, text: typeof line.text === 'string' ? line.text : ''}));
    const timed = format === 'lrc' ? lines.filter(line => line.atMs !== null) : lines;
    if (!timed.length) return [];
    return [Object.freeze({id: v.id, revision: 1, scope: 'library', canManage: false, deleted: false, language: 'und', offsetMs: format === 'lrc' ? offsetMs : 0, format, digest: '',
      provenance: Object.freeze({origin: 'sidecar', providerId: '', label: '', rights: ''}), createdAt: '',
      document: Object.freeze({format, text: v.text, lines: Object.freeze(timed), embeddedOffsetMs: format === 'lrc' ? offsetMs : 0})})];
  });
  const source = Object.freeze({id: target.sourceId ?? target.itemId, version: '0'.repeat(64), startSeconds: 0, duration: 86400, sourceDuration: 86400, local: true});
  return Object.freeze({
    scope: Object.freeze({serverId: scope.serverId, libraryId: target.libraryId, itemId: target.itemId, viewerFence: 'profile', sessionId: target.sessionId ?? '', sessionGeneration: target.sessionGeneration ?? 0}),
    sources: Object.freeze([source]), source, resources: Object.freeze(resources),
    selection: Object.freeze({revision: 1, resource: resources[0] ?? null}),
    canShare: false, providers: Object.freeze({lrclib: false, configured: false, revision: 0}),
  });
}
