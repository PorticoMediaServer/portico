import {FileInfoSession} from '../../../../packages/client-core/src/file-info-session.ts';

/** The rebuilt server's title and privileged file answers, shared by the real panel fixtures. */
export const item = {id: 'movie', kind: 'movie', versions: [
  {id: 'g1', container: 'mkv', size: 2048, durationMs: 60000, parts: [{size: 1024, durationMs: 30000, streams: [
    {index: 0, kind: 'video', codec: 'hevc', width: 3840, height: 1608, hdr: 'hdr10'},
    {index: 1, kind: 'audio', codec: 'truehd', language: 'en', channels: 8},
    {index: 2, kind: 'subtitle', codec: 'srt', language: 'en', hearingImpaired: true}]}, {size: 1024, durationMs: 30000, streams: []}]},
  {id: 'g2', container: 'mkv', size: 1024, durationMs: 60000, parts: [{size: 1024, durationMs: 60000, streams: []}]},
]};
export const locations = {versions: item.versions.map(v => ({id: v.id, files: v.parts.map((p, i) => ({...p, container: v.container, path: `/private/${v.id}-${i}.mkv`}))}))};
export const offered = {itemId: item.id, versions: item.versions.map(v => ({id: v.id,
  audio: [{id: v.id + ':audio', codec: 'truehd', language: 'en', channels: 8}],
  subtitles: [{id: v.id + ':sub', format: 'srt', language: 'en', source: 'sidecar', sdh: true}]}))};
export function fakeSession(role = 'owner') {
  const calls: string[] = [];
  const session = new FileInfoSession({itemId: item.id, role, isCurrent: () => true,
    api: {async request<T>(path: string, method = 'GET') {calls.push(`${method} ${path}`); return (path.endsWith('/files') ? locations : path.endsWith('/playback-options') ? offered : item) as T;}}});
  return {session, calls};
}
