// Listening tests written against the old server's `/v1/listening/preferences` keep pinning the same behaviour: the
// service now reads and saves the two settings in the profile's settings document (`/v1/preferences`), and this asks
// the fixture with the old path and document, then answers as the rebuilt server would. Only test code uses this.
// The same for the playing item's listening details: the service reads the item (`GET /v1/items/{id}`), and this
// asks the fixture's old `/v1/items/{id}/listening` context (and its old session chapters) and answers with the item
// they describe.
import type {ListeningRequest} from '../../src/listening.ts';

const speed = 'audiobooks.defaultSpeed', timer = 'playback.sleepTimerMinutes';
export function legacyListening(request: ListeningRequest, sent?: (path: string, body: unknown, method?: string) => void): ListeningRequest {
  let revision = 0;
  const asSettings = (raw: unknown) => {
    const data = (raw as {data?: {revision?: number; bookRate?: unknown; passoutMinutes?: unknown}} | undefined)?.data;
    if (!data) return raw;
    revision = data.revision ?? revision;
    return {deviceClass: 'web', deviceBound: false, registry: {fields: []}, documents: [], effective: {[speed]: data.bookRate, [timer]: data.passoutMinutes}, effectiveSource: {}};
  };
  return async (path, body, method, signal) => {
    sent?.(path, body, method);
    const item = /^\/v1\/items\/([^/?]+)$/.exec(path);
    if (item && (method ?? 'GET') === 'GET') {
      const raw = await request(`${path}/listening`, undefined, 'GET', signal) as {data?: Record<string, unknown>} | undefined;
      const c = raw?.data;
      if (!c) return raw;
      const kind = c.kind === 'song' ? 'track' : c.kind === 'audiobook_file' ? 'audiobook' : c.kind;
      // The fixture's old session chapters, where it has them, become the item's chapter marks.
      const old = await request('/v1/playback/sessions/session/chapters', undefined, 'GET', signal) as {chapters?: {title: string; startMs: number; endMs: number}[]; durationMs?: number} | undefined;
      const markers = Array.isArray(old?.chapters) ? old.chapters.map(ch => ({kind: 'chapter', title: ch.title, startMs: ch.startMs, endMs: ch.endMs})) : [];
      return {id: c.itemId, libraryId: c.libraryId, kind, ...(c.albumId ? {parentId: c.albumId} : {}), ...(c.artistId ? {rootId: c.artistId} : {}),
        versions: [{id: 'source', durationMs: old?.durationMs ?? 60000, parts: [{}], markers}]};
    }
    if (!path.startsWith('/v1/preferences')) return request(path, body, method, signal);
    if (method === 'PATCH') {
      const values = (body as {values: Record<string, unknown>}).values;
      return asSettings(await request('/v1/listening/preferences', {revision, bookRate: values[speed], passoutMinutes: values[timer]}, 'PUT', signal));
    }
    return asSettings(await request('/v1/listening/preferences', undefined, 'GET', signal));
  };
}
