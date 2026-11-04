import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeTitleController} from '../src-next/screens/watch/title-controller.ts';

// A film's More like this is its recommendations (`/v1/items/{id}/recommendations`), as web reads it; Detail is read
// without its embedded rows, and a failed recommendations read falls back once to them (web's rule).
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const settle = async () => {for (let i = 0; i < 30; i++) await tick();};
const card = (id: string) => ({id, kind: 'movie', title: id, libraryId: 'films', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}});
const row = (id: string, title: string, entries: string[], nextCursor = '', revision = 1) => ({
  id, viewerFence: 'viewer', title, kind: 'recommendations', artworkShape: 'poster', endpoint: 'item-recommendations',
  privacySensitivity: 'public', policyState: 'current', priority: 1, required: false, hideable: true, reorderable: false, critical: false, cursorCapable: !!nextCursor,
  entries: entries.map(card), total: entries.length + (nextCursor ? 1 : 0), start: 0, limit: entries.length, hasMore: !!nextCursor, nextCursor, revision: {catalog: revision, viewer: 1},
});
const detail = (related: boolean) => ({scope: {serverId: 'server', libraryId: 'films', itemId: 'film', viewerFence: 'viewer'}, revision: {catalog: 1, viewer: 1},
  item: {id: 'film', libraryId: 'films', kind: 'movie', title: 'Film', available: true, durationMs: 1000, progressMs: 0},
  personal: {watchlisted: false, favorite: false, watched: false, rating: null, revision: 1, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: []},
  actions: [],
  ...(related ? {related: {version: 1, libraryId: 'films', libraryName: 'Films', rows: [{id: 'genre:local:drama', relation: 'genre', provider: 'local', evidenceId: 'drama', heading: 'More drama', entries: [{...card('embedded-1'), playback: undefined}]}]}} : {})});

function fixture(recommendations: 'ok' | 'fail' | 'moved') {
  const paths: string[] = [];
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)); paths.push(url.pathname + url.search);
    const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status});
    if (url.pathname === '/v1/items/film/detail') return json(detail(url.searchParams.get('related') === 'all'));
    if (url.pathname === '/v1/items/film/recommendations') {
      if (recommendations === 'fail') return json({error: {code: 'unavailable'}}, 503);
      const revision = recommendations === 'moved' ? 2 : 1;
      return json({itemId: 'film', revision: {catalog: revision, viewer: 1}, rows: [row('more-like', 'More like Film', ['alien', 'film', 'arrival'], 'page-2', revision)]});
    }
    if (url.pathname === '/v1/items/film/recommendations/rows/more-like') return json({...row('more-like', 'More like Film', ['brazil']), start: 2});
    return json({error: {code: 'not_found'}}, 404);
  });
  const controller = new NativeTitleController({api, scope: {serverId: 'server', viewerId: 'viewer'}, entry: {id: 'film', libraryId: 'films', kind: 'movie', title: 'Film'} as never,
    authority, current: () => authority, subscribeCurrent: () => () => {}, requestId: async () => 'request', playback: {start: async () => {}}});
  return {controller, paths};
}

test('a film reads Detail without its embedded rows and draws its recommendations, continued to their end', async () => {
  const f = fixture('ok');
  try {
    await f.controller.open(); await settle();
    assert.ok(f.paths.includes('/v1/items/film/detail?related=none'), 'Detail without its embedded rows');
    assert.equal(f.paths.some(path => path.includes('related=all')), false);
    const rows = f.controller.getSnapshot().recommendationRows;
    assert.deepEqual(rows.map(value => value.id), ['more-like']);
    assert.equal(rows[0]!.heading, 'More like Film');
    assert.deepEqual(rows[0]!.entries.map(entry => entry.id), ['alien', 'arrival'], 'the film itself is never recommended to itself');
    f.controller.moreRelated('more-like', f.controller.getSnapshot().related['more-like']!); await settle();
    assert.ok(f.paths.some(path => path.startsWith('/v1/items/film/recommendations/rows/more-like?') && path.includes('cursor=page-2')));
    assert.deepEqual(f.controller.getSnapshot().related['more-like']!.entries.map(entry => entry.id), ['alien', 'arrival', 'brazil']);
  } finally {f.controller.dispose();}
});

for (const [name, mode] of [['fails', 'fail'], ['answers for another revision', 'moved']] as const) {
  test(`a film whose recommendations read ${name} falls back once to Detail's embedded rows`, async () => {
    const f = fixture(mode);
    try {
      await f.controller.open(); await settle();
      assert.ok(f.paths.includes('/v1/items/film/detail?related=all'), 'Detail read again with its rows');
      assert.equal(f.paths.filter(path => path.startsWith('/v1/items/film/recommendations?')).length, 1, 'once, no loop');
      assert.deepEqual(f.controller.getSnapshot().recommendationRows, []);
      assert.deepEqual(f.controller.getSnapshot().detail?.data?.related?.rows.map(value => value.id), ['genre:local:drama']);
    } finally {f.controller.dispose();}
  });
}
