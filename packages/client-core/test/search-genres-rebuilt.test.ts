import test from 'node:test';
import assert from 'node:assert/strict';
import {readSearchGenres, searchGenreBrowseRequest} from '../src/search-genres.ts';
import {libraryItemQuery} from '../src/library-browse.ts';
import {readBrowseCapabilitiesResponse} from '../src/query/browse-reads.ts';
import {BrowseSession} from '../src/browse-session.ts';

const answer = (posters?: boolean) => ({genres: [
  {value: 'tag_a', label: 'Action', cover: {backdrop: '/v1/images/a'}, count: 12, ...(posters ? {posters: [{poster: '/v1/images/a1'}, {poster: '/v1/images/a2'}]} : {})},
  {value: 'tag_d', label: 'Drama', count: 3, ...(posters ? {posters: []} : {})},
  {value: 'tag_w', label: 'Western', cover: {backdrop: '/v1/images/w'}, count: 1, ...(posters ? {posters: [{poster: '/v1/images/w1'}]} : {})},
]});
const api = (body: unknown, paths: string[] = []) => ({request: async <T>(path: string) => {paths.push(path); return body as T;}});

test('Search genres read the rebuilt server: every genre by name with its count, and each slot keeps the genres with its art', async () => {
  const paths: string[] = [];
  const all = await readSearchGenres(api(answer(), paths) as never, {limit: 50});
  assert.equal(paths[0], '/v1/genres?counts=true');
  assert.deepEqual(all.genres.map(genre => [genre.value, genre.label, genre.count, genre.backdrop?.path]),
    [['tag_a', 'Action', 12, '/v1/images/a'], ['tag_d', 'Drama', 3, undefined], ['tag_w', 'Western', 1, '/v1/images/w']]);
  assert.equal(all.hasGenres, true);
  // TV's and the web's backdrop tiles: only genres with a backdrop, up to the limit.
  assert.deepEqual((await readSearchGenres(api(answer()) as never, {preview: 'backdrop', limit: 1})).genres.map(genre => genre.value), ['tag_a']);
  // The phone's two-poster tiles: while the server sends no posters, the genres with a backdrop (a stopgap); once it does, only those with two.
  assert.deepEqual((await readSearchGenres(api(answer()) as never, {preview: 'covers', limit: 6})).genres.map(genre => genre.value), ['tag_a', 'tag_w']);
  const covers = await readSearchGenres(api(answer(true)) as never, {preview: 'covers', limit: 6});
  assert.deepEqual(covers.genres.map(genre => [genre.value, genre.covers.map(art => art.path)]), [['tag_a', ['/v1/images/a1', '/v1/images/a2']]]);
});

test('the All genres list hands the read out a page at a time', async () => {
  const first = await readSearchGenres(api(answer()) as never, {limit: 2});
  assert.deepEqual([first.genres.map(genre => genre.value), first.nextCursor], [['tag_a', 'tag_d'], 'o2']);
  const rest = await readSearchGenres(api(answer()) as never, {limit: 2, cursor: first.nextCursor});
  assert.deepEqual([rest.genres.map(genre => genre.value), rest.nextCursor], [['tag_w'], undefined]);
});

test('an unreadable genre answer is refused, so only that section fails', async () => {
  await assert.rejects(readSearchGenres(api({items: []}) as never, {}), /couldn't read/);
});

test('a genre opens its films and shows across every library: GET /v1/items?tag=&kind=movie,show', async () => {
  const request = searchGenreBrowseRequest('tag_a');
  const capabilities = await readBrowseCapabilitiesResponse({request: async () => {throw new Error('no read')}}, '*', request.pivot) as {pivots: {id: string}[]; library: {id: string}};
  assert.deepEqual([capabilities.library.id, capabilities.pivots.map(pivot => pivot.id)], ['*', ['video-titles']]);
  assert.deepEqual(libraryItemQuery({id: '*'}, request.pivot, [request.query!], request.sort!), {kind: 'movie,show', tag: 'tag_a', sort: 'title', order: 'asc'});
  const asked: string[] = [];
  const session = new BrowseSession({query: {libraryId: '*', pivot: request.pivot, capabilities: {} as never, nodes: [request.query!], sorts: [...request.sort!]},
    current: () => true, item: entry => entry, request: async (path: string) => {
      asked.push(path);
      const url = new URL(path, 'http://server');
      if (url.pathname === '/v1/facets') return {facet: 'letter', values: []};
      return {items: [{id: 'm1', kind: 'movie', title: 'Heat', libraryId: 'lib_f', images: {}, state: {}}, {id: 's1', kind: 'show', title: 'Justified', libraryId: 'lib_s', images: {}, state: {}}]};
    }});
  const page = await session.fetchPage(0, 2, new AbortController().signal);
  assert.deepEqual(page.items.map(entry => [entry.id, entry.kind, entry.libraryId]), [['m1', 'movie', 'lib_f'], ['s1', 'show', 'lib_s']]);
  assert.ok(asked.some(path => path.startsWith('/v1/items?kind=movie%2Cshow&tag=tag_a&sort=title&order=asc')), asked.join(' | '));
});
