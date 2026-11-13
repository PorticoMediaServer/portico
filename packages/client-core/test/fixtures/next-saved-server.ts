// A rebuilt server for Saved tests: My List, a playlist of three entries, its shares, and every request recorded.
// Only test code uses this.
export type Call = {path: string; method: string; body?: any; headers?: Record<string, string>};
export const title = (id: string, extra: Record<string, unknown> = {}) => ({id, kind: 'movie', title: 'Title ' + id, libraryId: 'films', ...extra});
export const list = (extra: Record<string, unknown> = {}) => ({id: 'pl', kind: 'playlist', name: 'Road trip', description: 'For the car', owner: {id: 'me', name: 'Sam'}, role: 'owner', count: 3, covers: [], revision: '7',
  createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-02T00:00:00Z', ...extra});
/** An answer that refuses: what `requestRaw` hands back as a status, and `request` throws. */
export const refused = (status: number, code: string, message = 'This list changed. Reload and try again.') => ({refused: {status, body: {error: {code, message}}}});
const refusalOf = (value: unknown) => (value as {refused?: {status: number; body: unknown}} | undefined)?.refused;
/** A rebuilt server: My List, a playlist of three entries, its shares, and every edit recorded. */
export function nextSavedServer(answers: Partial<Record<string, (call: Call, url: URL) => unknown>> = {}) {
  const calls: Call[] = [];
  // The profile's pins setting (area 6 rule 38), as GET and PATCH /v1/preferences keep it.
  const pins: string[] = [];
  const answer = (call: Call) => {
    const url = new URL(call.path, 'https://x'), key = call.method + ' ' + url.pathname;
    if (answers[key]) return answers[key]!(call, url);
    if (key === 'GET /v1/preferences') return {effective: {pins: [...pins]}};
    if (key === 'PATCH /v1/preferences') { pins.splice(0, pins.length, ...((call.body as {values: {pins: string[]}}).values.pins)); return {effective: {pins: [...pins]}}; }
    if (key === 'GET /v1/items' && url.searchParams.get('limit') === '1' && url.searchParams.get('total') === 'true') return {items: [], total: 5};
    if (key === 'GET /v1/items' && url.searchParams.get('list') === 'pl') {
      const all = [title('a', {entryId: 'e1'}), title('b', {entryId: 'e2'}), title('a', {entryId: 'e3'})];
      return {items: url.searchParams.get('order') === 'desc' ? [all[2]] : all, total: 3};
    }
    if (key === 'GET /v1/items') return {items: [title('one'), title('two')], total: 2};
    if (key === 'GET /v1/lists/pl') return list();
    if (key === 'GET /v1/lists/pl/shares') return {shares: [{profileId: 'kid', name: 'Kid', person: 'Sam', canEdit: false}], people: [{profileId: 'kid', name: 'Kid'}, {profileId: 'gran', name: 'Gran', person: 'Gran'}]};
    if (key === 'GET /v1/lists') return {items: [list(), list({id: 'theirs', role: 'viewer', name: 'Shared'})]};
    if (key === 'POST /v1/lists/pl/entries') return {list: list({revision: '8'}), added: 1, already: 0};
    return list({revision: '8'});
  };
  const api = {
    request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
      const call = {path, method, body}; calls.push(call); const value = answer(call), no = refusalOf(value);
      if (no) throw Object.assign(new Error('refused'), {status: no.status, code: (no.body as any).error.code});
      return value as T;
    },
    requestRaw: async (path: string, method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) => {
      const call = {path, method, body: options.body, headers: options.headers}; calls.push(call);
      const value = answer(call), no = refusalOf(value);
      return no ? {status: no.status, headers: {}, body: no.body} : {status: 200, headers: {}, body: value};
    },
  };
  return {api, calls, pins};
}
