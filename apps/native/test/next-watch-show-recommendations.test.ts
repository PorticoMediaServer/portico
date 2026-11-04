import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {NativeTitleController} from '../src-next/screens/watch/title-controller.ts';

// N4-8: a show or season page's More like this is the show's recommendations, as on web.
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const card = (id: string) => ({id, kind: 'show', title: id, libraryId: 'library', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}});
const row = (id: string, title: string, entries: string[], options: {nextCursor?: string; titleText?: {code: string; fallback: string}; revision?: number} = {}) => ({
  id, viewerFence: 'viewer', title, ...(options.titleText ? {titleText: options.titleText} : {}), kind: 'recommendations', artworkShape: 'poster', endpoint: 'show-recommendations',
  privacySensitivity: 'public', policyState: 'current', priority: 1, required: false, hideable: true, reorderable: false, critical: false, cursorCapable: !!options.nextCursor,
  entries: entries.map(card), total: entries.length + (options.nextCursor ? 1 : 0), start: 0, limit: entries.length, hasMore: !!options.nextCursor, nextCursor: options.nextCursor ?? '',
  revision: {catalog: options.revision ?? 1, viewer: 1},
});

function fixture(kind: 'show' | 'season' | 'movie', recommendationRevision = 1) {
  const paths: string[] = [];
  const episode = (id: string) => ({id, kind: 'episode', title: id, libraryId: 'library', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}});
  const api = new HttpLocalApi('https://portico.test', '', async input => {
    const url = new URL(String(input)); paths.push(url.pathname + url.search);
    const json = (value: unknown) => new Response(JSON.stringify(value), {status: 200});
    if (url.pathname === '/v1/shows/show/recommendations') return json({showId: 'show', revision: {catalog: recommendationRevision, viewer: 1}, rows: [
      row('more-like', 'More like Actual show', ['severance', 'silo'], {nextCursor: 'page-2', revision: recommendationRevision}),
      row('creator', 'From its creator', ['show', 'dark-matter'], {titleText: {code: 'next.controls.moreLikeThis', fallback: 'From its creator'}, revision: recommendationRevision}),
      row('empty', 'Nothing left', ['show'], {revision: recommendationRevision}),
    ]});
    if (url.pathname === '/v1/shows/show/recommendations/rows/more-like') return json({...row('more-like', 'More like Actual show', ['slow-horses']), start: 2});
    if (url.pathname.startsWith('/v1/items/')) return new Response(JSON.stringify({error: {code: 'not_found'}}), {status: 404});
    const selectedSeason = url.searchParams.get('selectedSeasonId') ?? url.searchParams.get('seasonId') ?? 's1';
    const revision = {catalog: 1, viewer: 1};
    return json({scope: {serverId: 'server', libraryId: 'library', viewerFence: 'viewer'}, revision,
      show: {id: 'show', libraryId: 'library', title: 'Actual show', libraryKind: 'tv', providerMatchStatus: 'matched'}, selected: {seasonId: selectedSeason},
      seasons: ['s1', 's2'].map((id, number) => ({id, showId: 'show', title: id, number: number + 1})), seasonTotalCount: 2, nextSeasonCursor: '',
      episodes: {scope: {serverId: 'server', libraryId: 'library', libraryKind: 'tv', viewerFence: 'viewer', view: 'season', entityId: selectedSeason}, revision,
        query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'}, sections: [{id: 'episodes', type: 'list', entries: [episode('e1')], totalCount: 1, nextCursor: ''}]},
    });
  });
  const id = kind === 'season' ? 's2' : kind === 'movie' ? 'movie' : 'show';
  const controller = new NativeTitleController({api, scope: {serverId: 'server', viewerId: 'viewer'}, entry: {id, libraryId: 'library', kind, title: id} as never,
    authority, current: () => authority, subscribeCurrent: () => () => {}, requestId: async () => 'request', playback: {start: async () => {}}});
  return {controller, paths, reads: () => paths.filter(path => path.startsWith('/v1/shows/show/recommendations?'))};
}
const settle = async () => {for (let i = 0; i < 20; i++) await tick();};

test('a show page draws the show\'s recommendation rows under More like this, named by the server, and continues a row to its end', async () => {
  const f = fixture('show');
  try {
    await f.controller.open(); await settle();
    const rows = f.controller.getSnapshot().recommendationRows;
    assert.deepEqual(rows.map(value => value.id), ['more-like', 'creator'], 'every row in the server\'s order; one with only the show itself is left out');
    assert.equal(rows[0]!.heading, 'More like Actual show');
    assert.equal(rows[1]!.heading, 'More like this', 'a row\'s own words come from the catalogue');
    assert.deepEqual(rows[1]!.entries.map(entry => entry.id), ['dark-matter'], 'the show itself is never recommended to itself');
    assert.equal(f.reads().length, 1);
    const first = f.controller.getSnapshot().related['more-like']!;
    assert.equal(first.hasMore, true);
    f.controller.moreRelated('more-like', first); await settle();
    assert.ok(f.paths.some(path => path.startsWith('/v1/shows/show/recommendations/rows/more-like?') && path.includes('cursor=page-2')), 'continued through the show\'s row route');
    assert.deepEqual(f.controller.getSnapshot().related['more-like']!.entries.map(entry => entry.id), ['severance', 'silo', 'slow-horses']);
    await f.controller.season('s2'); await settle();
    assert.equal(f.reads().length, 1, 'a season change keeps the show\'s rows');
  } finally {f.controller.dispose();}
  assert.deepEqual(f.controller.getSnapshot().recommendationRows, []);
});

test('a season page draws its show\'s rows; an answer from another revision is not the page\'s; a film never reads them', async () => {
  const season = fixture('season');
  try {
    await season.controller.open(); await settle();
    assert.deepEqual(season.controller.getSnapshot().recommendationRows.map(value => value.id), ['more-like', 'creator']);
  } finally {season.controller.dispose();}
  const moved = fixture('show', 2);
  try {
    await moved.controller.open(); await settle();
    assert.equal(moved.reads().length, 1);
    assert.deepEqual(moved.controller.getSnapshot().recommendationRows, []);
  } finally {moved.controller.dispose();}
  const film = fixture('movie');
  try {
    await film.controller.open().catch(() => {}); await settle();
    assert.equal(film.reads().length, 0);
  } finally {film.controller.dispose();}
});
