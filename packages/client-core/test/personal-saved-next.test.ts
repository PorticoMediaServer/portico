// Saved's History on the rebuilt server (area 6 rules 18, 19, 21): what PersonalSavedService reads and sends.
import test from 'node:test';
import assert from 'node:assert/strict';
import {PersonalSavedService} from '../src/personal-saved.ts';

const scope = {serverId: 'server', viewerId: '["local","account","profile"]'};
const item = (id: string) => ({id, kind: 'movie', title: 'Film ' + id, libraryId: 'films', state: {positionMs: 0, watched: true, playCount: 1, favorite: false, inList: false, notInterested: false}});
function server(pages: Record<string, unknown> = {}) {
  const calls: {path: string; method: string}[] = [];
  const api = {request: async <T>(path: string, method = 'GET'): Promise<T> => {
    calls.push({path, method});
    const url = new URL(path, 'https://x');
    if (method === 'GET' && url.pathname === '/v1/history') return (pages[url.searchParams.get('cursor') ?? ''] ?? {items: []}) as T;
    if (method === 'DELETE' && url.pathname === '/v1/history') return undefined as T;
    throw new Error(`unexpected ${method} ${path}`);
  }};
  return {api, calls};
}
const service = (api: unknown) => new PersonalSavedService(api as never, scope, async () => '00000000-0000-4000-8000-000000000001');

test('History lists the profile\'s finished plays, newest first, for the period chosen, and pages on', async () => {
  const s = server({'': {items: [{id: 'h1', playedAt: '2026-11-12T09:00:00Z', watchedMs: 5_400_000, kind: 'movie', title: 'Film a', item: item('a')}], nextCursor: 'next'},
    next: {items: [{id: 'h2', playedAt: '2026-11-11T09:00:00Z', kind: 'movie', title: 'Film b', item: item('b')}]}});
  const saved = service(s.api);
  await saved.select({view: 'history', period: '7d'} as never);
  const shot = saved.getSnapshot();
  assert.equal(shot.error, null);
  assert.deepEqual(shot.entries.map(entry => [entry.id, entry.media?.id, entry.updatedAt, entry.completed, entry.positionSeconds]), [['h1', 'a', '2026-11-12T09:00:00Z', true, 5400]]);
  const asked = new URL(s.calls[0]!.path, 'https://x').searchParams;
  assert.deepEqual([asked.get('period'), asked.get('limit')], ['7d', '40']);
  await saved.next();
  assert.deepEqual(saved.getSnapshot().entries.map(entry => entry.id), ['h2']);
  assert.equal(new URL(s.calls.at(-1)!.path, 'https://x').searchParams.get('cursor'), 'next');
  saved.dispose();
});

test('a title removed from the server stays in History by name and cannot be opened to play', async () => {
  const s = server({'': {items: [{id: 'h1', playedAt: '2026-11-12T09:00:00Z', kind: 'movie', title: 'A film that went'}]}});
  const saved = service(s.api);
  await saved.select({view: 'history'} as never);
  const media = saved.getSnapshot().entries[0]!.media!;
  assert.deepEqual([media.title, media.missing, media.available, 'playback' in media], ['A film that went', true, false, false]);
  saved.dispose();
});

test('Clear history empties History on the server and reads it again', async () => {
  const s = server({'': {items: [{id: 'h1', playedAt: '2026-11-12T09:00:00Z', kind: 'movie', title: 'Film a', item: item('a')}]}});
  const saved = service(s.api);
  await saved.select({view: 'history'} as never);
  assert.equal(await saved.mutate({action: 'clear-history'}), true);
  assert.deepEqual(s.calls.filter(call => call.method === 'DELETE').map(call => call.path), ['/v1/history']);
  assert.ok(s.calls.filter(call => call.method === 'GET').length >= 2, 'History is read again after it is cleared');
  saved.dispose();
});
