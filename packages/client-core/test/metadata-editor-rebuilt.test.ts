/**
 * The metadata editor on the rebuilt server (definition area 4): what an owner would lose if any of these broke.
 * The title's document, pictures and History are read from the rebuilt routes; an edit names the revision it was
 * made from (If-Match) and sends only what changed; a change made over a newer one is refused, not overwritten; the
 * Matching tab searches, uses a match with its episode order, looks it up again and takes a wrong match off.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {MetadataRepairService} from '../src/metadata-repair.ts';

const scope = {serverId: 'server', viewerId: 'owner'}, target = {kind: 'show' as const, id: 'itm_1', libraryId: 'lib'};
const doc = (revision = '7') => ({itemId: 'itm_1', kind: 'show', revision,
  fields: {title: {value: 'Heat', source: 'provider', locked: false}, year: {value: 1995, source: 'provider', locked: false}, rating: {value: 8.3, source: 'provider', locked: false}},
  lists: {genres: {values: ['Crime'], source: 'provider', locked: false}, labels: {values: [], source: 'edit', locked: false}},
  credits: {values: [{personId: 'per_1', name: 'Al Pacino', role: 'actor', character: 'Hanna'}], source: 'provider', locked: false},
  match: {state: 'matched', locked: false, ids: {tmdb: '949', imdb: 'tt0113277'}, episodeOrder: 'aired'}});
const picture = (id: string, selected = false) => ({id, source: 'tmdb', url: `/v1/images/${id}`, selected, locked: false});

function server() {
  const calls: {path: string; method: string; body?: unknown; headers?: Record<string, string>}[] = [];
  let revision = '7', patchStatus = 200;
  const answer = (path: string, method: string): unknown => {
    if (path.startsWith('/v1/admin/items/itm_1/artwork?role=')) return {items: path.endsWith('poster') ? [picture('img_a', true), picture('img_b')] : []};
    if (path === '/v1/admin/items/itm_1/metadata/history') return {items: [{id: 'h_1', kind: 'fields_edited', at: '2026-11-16T12:00:00Z'}]};
    if (path === '/v1/admin/items/itm_1/metadata') return doc(revision);
    if (path.startsWith('/v1/admin/items/itm_1/matches')) return {items: [{provider: 'tmdb', id: '1396', title: 'Breaking Bad', year: 2008}]};
    if (method === 'POST' && path.endsWith('/refresh')) return {matched: true};
    return {itemId: 'itm_1'};
  };
  const api = {
    async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {calls.push({path, method, body}); return answer(path, method) as T;},
    async requestRaw(path: string, method: string, options: {headers?: Record<string, string>; body?: unknown}) {
      calls.push({path, method, body: options.body, headers: options.headers});
      if (patchStatus !== 200) return {status: patchStatus, body: {error: {code: patchStatus === 412 ? 'stale' : 'invalid_value', message: 'refused'}}};
      revision = '8'; return {status: 200, body: doc(revision)};
    },
  };
  return {api, calls, refuse: (status: number) => {patchStatus = status;}, writes: () => calls.filter(call => call.method !== 'GET')};
}

test('the editor reads the title, its pictures per role and its History from the rebuilt routes', async () => {
  const {api, calls} = server();
  const service = new MetadataRepairService({api, scope});
  await service.load(target);
  const data = service.getSnapshot().data!;
  assert.deepEqual(calls.map(call => call.path).sort(), ['/v1/admin/items/itm_1/artwork?role=backdrop', '/v1/admin/items/itm_1/artwork?role=banner',
    '/v1/admin/items/itm_1/artwork?role=logo', '/v1/admin/items/itm_1/artwork?role=poster', '/v1/admin/items/itm_1/metadata', '/v1/admin/items/itm_1/metadata/history']);
  assert.equal(data.revision, '7');
  assert.equal(data.snapshot.fields.title.value, 'Heat');
  assert.equal(data.snapshot.fields.title.source, 'tmdb', 'a provider value says which provider');
  assert.deepEqual(data.snapshot.fields.genres.values, ['Crime']);
  assert.equal(data.schema.some(spec => spec.field === 'rating'), false, 'the provider rating is not an editable field');
  assert.equal(data.snapshot.identity?.id, '949');
  assert.equal(data.snapshot.relationships[0].label, 'Al Pacino');
  assert.deepEqual(data.artwork.candidates.map(each => [each.id, each.current]), [['img_a', true], ['img_b', false]]);
  assert.deepEqual(data.history.map(entry => [entry.id, entry.trigger]), [['h_1', 'edit']]);
  service.dispose();
});

test('an edit names the revision it was made from and sends only what changed; a lock alone is a lock', async () => {
  const {api, calls} = server();
  const service = new MetadataRepairService({api, scope});
  await service.load(target);
  assert.equal(await service.command({action: 'edit', fields: {title: {value: 'Heat (1995)', locked: true}, year: {value: '1995', locked: true}, genres: {values: ['Crime', 'Thriller'], locked: true}}}), true);
  const patch = calls.find(call => call.method === 'PATCH')!;
  assert.equal(patch.path, '/v1/admin/items/itm_1/metadata');
  assert.equal(patch.headers?.['If-Match'], '"7"');
  assert.deepEqual(patch.body, {title: 'Heat (1995)', genres: ['Crime', 'Thriller'], lock: ['year']});
  assert.equal(service.getSnapshot().data!.revision, '8', 'the editor shows the title as the server now holds it');
  service.dispose();
});

test('a change made over a newer one is refused and overwrites nothing; a refused value keeps the editor open', async () => {
  const stale = server();
  let service = new MetadataRepairService({api: stale.api, scope});
  await service.load(target);
  stale.refuse(412);
  assert.equal(await service.command({action: 'edit', fields: {title: {value: 'Mine', locked: true}}}), false);
  assert.equal(service.getSnapshot().phase, 'conflict');
  service.dispose();
  const invalid = server();
  service = new MetadataRepairService({api: invalid.api, scope});
  await service.load(target);
  invalid.refuse(422);
  assert.equal(await service.command({action: 'edit', fields: {title: {value: 'x', locked: true}}}), false);
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.ok(service.getSnapshot().error);
  service.dispose();
});

test('Matching: search, use a match with its episode order, look it up again, and take a wrong match off', async () => {
  const {api, writes, calls} = server();
  const service = new MetadataRepairService({api, scope});
  await service.load(target);
  await service.command({action: 'search', query: 'Breaking Bad'});
  assert.ok(calls.some(call => call.path === '/v1/admin/items/itm_1/matches?q=Breaking+Bad'));
  assert.equal(service.getSnapshot().data!.candidates[0].title, 'Breaking Bad');
  await service.command({action: 'search', query: 'https://www.themoviedb.org/tv/1396'});
  assert.ok(calls.some(call => call.path === '/v1/admin/items/itm_1/matches?ref=https%3A%2F%2Fwww.themoviedb.org%2Ftv%2F1396'), 'an address is looked up as one');
  await service.command({action: 'identify', provider: 'tmdb', candidateId: '1396', order: 'dvd', episodeOffset: -100});
  await service.command({action: 'retry'});
  await service.command({action: 'keep_as_is'});
  assert.deepEqual(writes().map(call => [call.method, call.path, call.body]), [
    ['PUT', '/v1/admin/items/itm_1/match', {provider: 'tmdb', id: '1396', episodeOrder: 'dvd', episodeOffset: -100}],
    ['POST', '/v1/admin/items/itm_1/metadata/refresh', undefined],
    ['PUT', '/v1/admin/items/itm_1/match', {keepAsIs: true}]]);
  service.dispose();
});

test('Undo, Lock everything, and the picture choices go to their rebuilt routes', async () => {
  const {api, writes} = server();
  const service = new MetadataRepairService({api, scope});
  await service.load(target);
  await service.command({action: 'undo', historyId: 'h_1'});
  await service.command({action: 'lock_all'});
  await service.command({action: 'select_artwork', role: 'poster', candidateId: 'img_b'});
  await service.command({action: 'artwork_lock', role: 'poster', locked: false});
  await service.command({action: 'discover_artwork', role: 'poster'});
  assert.deepEqual(writes().map(call => [call.method, call.path, call.body]), [
    ['POST', '/v1/admin/items/itm_1/metadata/history/h_1/undo', undefined],
    ['PATCH', '/v1/admin/items/itm_1/metadata', {lockAll: true}],
    ['PUT', '/v1/admin/items/itm_1/artwork/poster', {imageId: 'img_b'}],
    ['PUT', '/v1/admin/items/itm_1/artwork/poster', {unlock: true}],
    ['POST', '/v1/admin/items/itm_1/artwork/find?role=poster', undefined]]);
  service.dispose();
});

test('artwork re-fetch keeps the server choice; making an episode picture waits, reloads it, and hides on no_decoder', async () => {
  const calls: string[] = [];
  let kind = 'movie', made = false, noDecoder = false, finish!: () => void;
  const gate = new Promise<void>(resolve => {finish = resolve;});
  const api = {async request<T>(path: string, method = 'GET'): Promise<T> {
    calls.push(`${method} ${path}`);
    if (path.endsWith('/artwork/make')) {
      if (noDecoder) throw {status: 503, code: 'no_decoder'};
      await gate; made = true;
    }
    if (path.endsWith('/metadata')) return {...doc(), kind} as T;
    if (path.includes('/artwork?role=')) return {items: [{...picture(made ? 'frame' : 'provider', true), source: made ? 'frame' : 'provider', locked: true}]} as T;
    return {items: []} as T;
  }};
  const service = new MetadataRepairService({api, scope});
  await service.load(target);
  assert.equal(service.getSnapshot().data!.canMakeEpisodeThumbnail, false);
  await service.command({action: 'refetch_artwork', role: 'poster'});
  assert.ok(calls.includes('POST /v1/admin/items/itm_1/artwork/refetch?role=poster'));
  assert.equal(service.getSnapshot().data!.snapshot.artwork[0].locked, true);
  kind = 'episode'; await service.load({...target, kind: 'item'});
  assert.equal(service.getSnapshot().data!.canMakeEpisodeThumbnail, true);
  const making = service.command({action: 'make_episode_thumbnail'});
  assert.equal(service.getSnapshot().phase, 'saving');
  finish(); await making;
  assert.ok(calls.includes('POST /v1/admin/items/itm_1/artwork/make'));
  assert.equal(service.getSnapshot().data!.snapshot.artwork[0].candidateId, 'frame');
  noDecoder = true;
  assert.equal(await service.command({action: 'make_episode_thumbnail'}), false);
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.equal(service.getSnapshot().error?.code, 'no_decoder');
  assert.equal(service.getSnapshot().data!.canMakeEpisodeThumbnail, false);
  await service.refresh(service.getSnapshot().data!);
  assert.equal(service.getSnapshot().data!.canMakeEpisodeThumbnail, false);
  service.dispose();
});
