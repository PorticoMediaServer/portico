// Listening tests written against the old server's `/v1/listening/preferences` keep pinning the same behaviour: the
// service now reads and saves the two settings in the profile's settings document (`/v1/preferences`), and this asks
// the fixture with the old path and document, then answers as the rebuilt server would. Only test code uses this.
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
    if (!path.startsWith('/v1/preferences')) return request(path, body, method, signal);
    if (method === 'PATCH') {
      const values = (body as {values: Record<string, unknown>}).values;
      return asSettings(await request('/v1/listening/preferences', {revision, bookRate: values[speed], passoutMinutes: values[timer]}, 'PUT', signal));
    }
    return asSettings(await request('/v1/listening/preferences', undefined, 'GET', signal));
  };
}
