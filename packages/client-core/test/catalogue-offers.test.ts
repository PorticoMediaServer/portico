import test from 'node:test';
import assert from 'node:assert/strict';
import {readCatalogueSelectionItem} from '../src/catalogue-selection.ts';
import {catalogueTitleCapabilities, wholeSelectionFromCounts, type CatalogueSelectionPorts} from '../src/catalogue-offers.ts';
import {selectionCapabilityNames} from '../src/selection-model/capabilities.ts';
import type {SelectionKind} from '../src/selection-model/types.ts';
import {BrowseSession} from '../src/browse-session.ts';
import {withQueries} from './fixtures/viewer-queries.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};
const portSets: CatalogueSelectionPorts[] = [
  {play: true, queue: true, myList: true, favorite: true, watched: true, playlist: true, collection: true, edit: true, delete: true},
  {play: true, queue: true, myList: true, favorite: true, watched: true, playlist: true, collection: true, edit: false, delete: true},
  {play: true},
  {},
];

// The whole selection counts each action as the sum of what each title would be granted picked alone, so the rule it
// uses must be the one a single pick gets: a leaf's from its own Detail read, a container's from the shared offer.
test('a title of each kind is granted alone exactly what the whole-selection rule says', async () => {
  for (const [wire, kind] of [['movie', 'movie'], ['episode', 'episode'], ['track', 'song']] as const) {
    for (const available of [true, false]) {
      for (const ports of portSets) {
        const api = {request: async <T>(path: string): Promise<T> => {
          assert.equal(path, '/v1/items/title');
          return {id: 'title', libraryId: 'lib', kind: wire, title: 'Title', state: {}, available} as T;
        }};
        const picked = await readCatalogueSelectionItem(withQueries({api, scope, requestId: async () => 'id', signal: new AbortController().signal, isCurrent: () => true}),
          {id: 'title', libraryId: 'lib', kind, title: 'Title'}, ports);
        assert.deepEqual(picked.capabilities, catalogueTitleCapabilities(kind, available, ports), `${kind} available=${available} ${JSON.stringify(ports)}`);
      }
    }
  }
  for (const kind of ['show', 'season', 'album', 'artist', 'book', 'collection'] as const) {
    for (const ports of portSets) {
      const api = {request: async <T>(): Promise<T> => {throw new Error('a container is not read');}};
      const picked = await readCatalogueSelectionItem(withQueries({api, scope, requestId: async () => 'id', signal: new AbortController().signal, isCurrent: () => true}),
        {id: 'title', libraryId: 'lib', kind, title: 'Title'}, ports);
      assert.deepEqual(picked.capabilities, catalogueTitleCapabilities(kind, true, ports), `${kind} ${JSON.stringify(ports)}`);
    }
  }
});

// The selection model refuses a window when more of its titles offer an action than the whole selection counts, or more
// lack it than the whole leaves uncounted. Every window of a list must pass: here, every subset of three films, one of
// them with its file missing, under each set of ports.
test('every window of picked titles fits the whole selection built from the server count', () => {
  const view = {libraryId: 'lib', pivot: 'movies', filter: null, sort: [], catalogRevision: 'reading-1'};
  const films = [true, true, false];
  for (const ports of portSets) {
    const whole = wholeSelectionFromCounts([{kind: 'movie', count: 3, available: 2}], view, ports)!;
    assert.equal(whole.total, 3);
    const type = whole.types[0]!;
    assert.deepEqual(type.libraries, [{libraryId: 'lib', count: 3}]);
    for (let mask = 1; mask < 8; mask++) {
      const window = films.filter((_, n) => mask & (1 << n)).map(available => catalogueTitleCapabilities('movie', available, ports)!);
      for (const name of selectionCapabilityNames) {
        const offered = window.filter(item => item[name]).length;
        assert(offered <= type.capabilities[name] && window.length - offered <= type.count - type.capabilities[name], `${name} ${mask} ${JSON.stringify(ports)}`);
      }
    }
  }
  const all = wholeSelectionFromCounts([{kind: 'movie', count: 3, available: 2}], view, portSets[0]!)!.types[0]!.capabilities;
  assert.deepEqual([all.play, all.queue, all.edit, all.watched, all.delete, all.download], [2, 0, 3, 3, 0, 0]);
  assert.equal(wholeSelectionFromCounts([{kind: 'disc' as SelectionKind, count: 1, available: 1}], view, portSets[0]!), undefined, 'a kind no one can pick');
  assert.equal(wholeSelectionFromCounts([], view, portSets[0]!), undefined, 'an empty list');
});

test('with ports, a session reads the exact whole selection when someone selects, for the list it shows', async () => {
  const query = {libraryId: 'lib', pivot: 'movies', capabilities: {library: {kind: 'movie'}} as never, nodes: [], sorts: [{field: 'title', direction: 'asc' as const}]};
  const asked: URL[] = [];
  const wholes: unknown[] = [];
  const session = new BrowseSession({query, current: () => true, item: entry => entry, selectionPorts: portSets[0], onWhole: whole => wholes.push(whole),
    request: async path => {
      const url = new URL(path, 'http://test');
      asked.push(url);
      if (url.pathname === '/v1/items/selection') return {total: 5000, kinds: [{kind: 'movie', count: 5000, available: 4990}]};
      return url.searchParams.has('total') ? {items: [], total: 5000} : {items: [{id: 'one', kind: 'movie', title: 'One'}], nextCursor: 'next'};
    }});
  await session.fetchPage(0, 60, new AbortController().signal);
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(session.wholeSelection(), undefined, 'nothing is estimated, and the whole list is not read before anyone selects');
  assert(!asked.some(url => url.pathname === '/v1/items/selection'));
  session.requestWholeSelection();
  await new Promise(resolve => setTimeout(resolve, 0));
  const read = asked.filter(url => url.pathname === '/v1/items/selection');
  assert.equal(read.length, 1);
  assert.equal(read[0]!.searchParams.get('library'), 'lib');
  assert.equal(read[0]!.searchParams.get('kind'), 'movie');
  const whole = session.wholeSelection()!;
  assert.equal(whole.total, 5000);
  assert.equal(whole.catalogRevision, session.revision);
  assert.deepEqual([whole.types[0]!.capabilities.play, whole.types[0]!.capabilities.queue, whole.types[0]!.capabilities.edit], [4990, 0, 5000]);
  assert.equal(wholes.length, 1);
  session.requestWholeSelection();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(asked.filter(url => url.pathname === '/v1/items/selection').length, 1, 'read once per session');
  session.retire();
  assert.equal(session.wholeSelection(), undefined);
});

// A forget drops a read still on its way and reads again for someone selecting; a list Select all cannot cover answers
// undefined at once; a failed read answers undefined and the next request reads again; a request made before the first
// page waits for it.
test('the exact whole selection survives forgets, failures and lists it cannot cover', async () => {
  const base = {libraryId: 'lib', pivot: 'movies', capabilities: {library: {kind: 'movie'}} as never, nodes: [], sorts: [{field: 'title', direction: 'asc' as const}]};
  const tick = () => new Promise(resolve => setTimeout(resolve, 0));
  const pending: ((value: unknown) => void)[] = [];
  let fail = false, answered = 0;
  const wholes: number[] = [];
  const session = new BrowseSession({query: base, current: () => true, item: entry => entry, selectionPorts: portSets[0], onWhole: whole => wholes.push(whole.total),
    request: async path => {
      const url = new URL(path, 'http://test');
      if (url.pathname === '/v1/items/selection') {
        if (fail) throw new Error('offline');
        return new Promise(resolve => pending.push(resolve));
      }
      return url.searchParams.has('total') ? {items: [], total: 9} : {items: [{id: 'one', kind: 'movie', title: 'One'}], nextCursor: 'next'};
    }});
  const early = session.requestWholeSelection();
  void early.then(() => answered++);
  await session.fetchPage(0, 60, new AbortController().signal);
  await tick();
  assert.equal(pending.length, 1, 'the request made before the first page reads once it arrives');
  session.forgetWholeSelection();
  await tick();
  assert.equal(pending.length, 2, 'a forget while someone selects reads again');
  pending[0]!({total: 1, kinds: [{kind: 'movie', count: 1, available: 1}]});
  await tick();
  assert.deepEqual(wholes, [], 'the read from before the forget answers nothing');
  assert.equal(answered, 0);
  pending[1]!({total: 9, kinds: [{kind: 'movie', count: 9, available: 9}]});
  assert.equal((await early)?.total, 9);
  assert.deepEqual(wholes, [9]);
  fail = true;
  session.forgetWholeSelection();
  await tick();
  assert.equal(await session.requestWholeSelection(), undefined, 'a failed read answers undefined');
  fail = false;
  const again = session.requestWholeSelection();
  await tick();
  pending[2]!({total: 8, kinds: [{kind: 'movie', count: 8, available: 7}]});
  assert.equal((await again)?.total, 8, 'and the next request reads again');
  session.retire();

  const series = new BrowseSession({query: {...base, pivot: 'series', capabilities: {library: {kind: 'book'}} as never}, current: () => true, item: entry => entry, selectionPorts: portSets[0],
    request: async path => new URL(path, 'http://test').pathname === '/v1/items/selection' ? {total: 2, kinds: [{kind: 'series', count: 2, available: 2}]}
      : {items: [{id: 'one', kind: 'series', title: 'One'}], total: 2}});
  await series.fetchPage(0, 60, new AbortController().signal);
  assert.equal(await series.requestWholeSelection(), undefined, 'a list of a kind no one can pick');
  series.retire();
});
