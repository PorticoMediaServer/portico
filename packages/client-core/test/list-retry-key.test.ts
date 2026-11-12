// From review desk B on #2911. The web title page's picker, the web library selection's picker and native Search
// wrap their transport as request-only for their own checks; they now hand the add-to sheet the app's header-sending
// transport beside it (`withRequestRaw`), so an addition names its Idempotency-Key and a Retry after a lost answer
// does not put a title in a playlist twice (06 rule 24 lets a playlist hold one twice).
import test from 'node:test';
import assert from 'node:assert/strict';
import {HomeSelectionDestinations} from '../src/home-selection/destinations.ts';
import {withRequestRaw} from '../src/home-selection/list-destinations.ts';

test('Retry after a lost answer does not put the title in a playlist twice, from a picker that wraps its transport request-only', async () => {
  const playlist: string[] = [], keys = new Map<string, unknown>();
  let lose = true;
  const row = () => ({id: 'pl', kind: 'playlist', name: 'Road trip', owner: {id: 'me', name: 'Me'}, role: 'owner', count: playlist.length, covers: [], revision: String(playlist.length + 1),
    createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z'});
  // The server: a playlist may hold a title twice (rule 24), and only an Idempotency-Key makes a repeat harmless.
  const apply = (body: {items: string[]}, key?: string) => {
    if (key && keys.has(key)) return keys.get(key);
    playlist.push(...body.items);
    const answer = {list: row(), added: body.items.length, already: 0};
    if (key) keys.set(key, answer);
    return answer;
  };
  const transport = {async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    if (method === 'GET') return (path.includes('/v1/lists/pl') ? row() : {items: [row()]}) as T;
    const answer = apply(body as {items: string[]});
    if (lose) {lose = false; throw Object.assign(new Error('The connection was lost.'), {code: 'network_error'});}
    return answer as T;
  }, async requestRaw(_path: string, _method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) {
    const answer = apply(options.body as {items: string[]}, options.headers?.['Idempotency-Key']);
    if (lose) {lose = false; throw Object.assign(new Error('The connection was lost.'), {code: 'network_error'});}
    return {status: 200, headers: {}, body: answer};
  }};
  // As entry-picker.ts builds it: its own request-only wrapper, with the app's header-sending transport beside it.
  const api = withRequestRaw({request: transport.request}, transport.requestRaw.bind(transport) as never, () => {});
  const target = {mode: 'explicit', items: [{item: {id: 'movie_1', kind: 'movie'}}], total: 1, summary: {}} as never;
  let state: {phase: string} = {phase: ''};
  const sheet = new HomeSelectionDestinations({api: api as never, events: undefined as never, requestId: async () => 'op-1'}, target, 'playlist', new AbortController().signal, () => true, value => {state = value;});
  await sheet.load();
  await sheet.choose('pl');
  assert.equal(state.phase, 'uncertain', 'the lost answer is shown as not known');
  await sheet.retry();
  assert.equal(state.phase, 'added');
  assert.deepEqual(playlist, ['movie_1'], 'the title went into the playlist twice');
});
