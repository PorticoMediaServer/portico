import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogueSelectionSegments, createCataloguePersonalSelection, playCatalogueSelection, readCatalogueSelectionItem} from '../src/catalogue-selection.ts';
import {selectionCapabilities, type SelectionItem, type SelectionKind} from '../src/selection-model/index.ts';
import {mediaSelectionKey} from '../src/media-selection.ts';
import type {ContentEntry} from '../src/library-content.ts';
import type {DetailApi} from '../src/detail.ts';
import type {QueueHeader} from '../src/playback-v1/types.ts';

const scope = {serverId: 'server', viewerId: 'viewer'};
const entry: ContentEntry = {id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Movie'};
const state = (revision = 0) => ({watchlisted: false, favorite: revision > 0, rating: null, revision});
const detail = () => ({scope: {...scope, libraryId: 'movies', itemId: 'movie', viewerFence: 'detail-only'}, revision: {catalog: 3, viewer: 0}, item: {...entry, durationMs: 10000, progressMs: 0, available: true}, personal: state(), actions: [{id: 'favorite', labelKey: 'favorite', enabled: true}], metadata: {status: 'unavailable', ratings: [], genres: [], credits: []}});
function selected(kind: Exclude<SelectionKind, 'person' | 'channel'>, id = kind): SelectionItem {
  return {kind, id, libraryId: 'library', capabilities: selectionCapabilities(kind, {play: true, queue: true})};
}
const queue: QueueHeader = {id: 'queue', revision: '1', total: 3, repeat: 'off', segments: []};

test('explicit selected identities keep supplied order and aggregate server selectors', () => {
  assert.deepEqual(catalogueSelectionSegments([selected('movie', 'one'), selected('episode', 'two'), selected('show', 'show'), selected('album', 'album'), selected('song', 'three'), selected('collection', 'collection'), selected('playlist', 'playlist')]), [
    {source: {items: {ids: ['one', 'two']}}}, {source: {container: {kind: 'show', id: 'show'}}}, {source: {container: {kind: 'album', id: 'album'}}}, {source: {items: {ids: ['three']}}}, {source: {container: {kind: 'collection', id: 'collection'}}}, {source: {container: {kind: 'playlist', id: 'playlist'}}},
  ]);
  const many = Array.from({length: 501}, (_, i) => selected('movie', String(i)));
  assert.deepEqual(catalogueSelectionSegments(many).map(segment => 'items' in segment.source ? segment.source.items.ids.length : 0), [500, 1]);
});

test('capabilities and unsupported semantic kinds reject before any queue replacement', async () => {
  const unsupported: SelectionItem[] = [selected('author'), selected('disc'), selected('chapter'), {kind: 'person', id: 'person', capabilities: selectionCapabilities('person', {play: true})}, {kind: 'channel', sourceId: 'actual-source', channelId: 'actual-channel', capabilities: selectionCapabilities('channel', {play: true})}, {...selected('movie'), capabilities: selectionCapabilities('movie', {})}];
  let called = 0;
  for (const item of unsupported) await assert.rejects(playCatalogueSelection({playSegments: async () => {called++; return queue;}}, [item], {signal: new AbortController().signal, isCurrent: () => true}), {code: 'catalogue-selection-unavailable'});
  assert.equal(called, 0);
  assert.throws(() => catalogueSelectionSegments([selected('movie'), selected('movie')]), {code: 'catalogue-selection-unavailable'});
  assert.throws(() => catalogueSelectionSegments([{...selected('movie'), capabilities: selectionCapabilities('movie', {play: true})}], 'queue'), {code: 'catalogue-selection-unavailable'});
});

test('segments detach selected identities and cannot be edited after capture', () => {
  const input = {kind: 'movie' as const, id: 'original', libraryId: 'movies', capabilities: selectionCapabilities('movie', {play: true})};
  const result = catalogueSelectionSegments([input]);
  input.id = 'next-viewer';
  assert.deepEqual(result, [{source: {items: {ids: ['original']}}}]);
  assert(Object.isFrozen(result));
  assert(Object.isFrozen(result[0].source));
  assert('items' in result[0].source && Object.isFrozen(result[0].source.items.ids));
});

test('real player receives original lifetime; source retirement rejects late receipt', async () => {
  let current = true;
  const controller = new AbortController();
  await assert.rejects(playCatalogueSelection({playSegments: async (segments, lifetime) => {
    assert.equal(lifetime?.signal, controller.signal);
    assert.equal(lifetime?.isCurrent?.(), true);
    assert.deepEqual(segments, [{source: {container: {kind: 'book', id: 'book'}}}]);
    current = false;
    return queue;
  }}, [selected('book')], {signal: controller.signal, isCurrent: () => current}), {name: 'AbortError'});
});

test('personal binding is inert, retains captured transport and preserves retry operation identity', async () => {
  let requests = 0, fail = true;
  const operations: string[] = [];
  const api: DetailApi = {request: async<T>(_path: string, method = 'GET', body?: unknown): Promise<T> => {
    requests++;
    if (method === 'GET') return detail() as T;
    const operation = body as {operationId: string};
    operations.push(operation.operationId);
    if (fail) {fail = false; throw new TypeError('Connection lost');}
    return {operationId: operation.operationId, serverId: 'server', libraryId: 'movies', itemId: 'movie', viewerFence: 'detail-only', personal: state(1)} as T;
  }};
  const options = {api, scope, requestId: async () => crypto.randomUUID(), signal: new AbortController().signal, isCurrent: () => true};
  const binding = createCataloguePersonalSelection(options);
  assert.equal(requests, 0);
  options.api = {request: async () => {throw new Error('Next viewer transport must not be used');}};
  binding.service.setEntries([entry]);
  binding.service.toggle(mediaSelectionKey(entry));
  await binding.service.apply('favorite', true);
  assert.equal(binding.service.getSnapshot().results[0].status, 'failed');
  await binding.service.retry();
  assert.equal(binding.service.getSnapshot().results[0].status, 'saved');
  assert.equal(operations.length, 2);
  assert.equal(operations[0], operations[1]);
  binding.dispose();
});

test('source retirement or original abort during Detail read prevents personal write', async () => {
  for (const abort of [false, true]) {
    let current = true, writes = 0;
    const controller = new AbortController();
    const api: DetailApi = {request: async<T>(_path: string, method = 'GET'): Promise<T> => {
      if (method !== 'GET') {writes++; throw new Error('Retired write');}
      if (abort) controller.abort(); else current = false;
      return detail() as T;
    }};
    const binding = createCataloguePersonalSelection({api, scope, requestId: async () => crypto.randomUUID(), signal: controller.signal, isCurrent: () => current});
    binding.service.setEntries([entry]);binding.service.selectLoaded();
    await binding.service.apply('favorite', true);
    assert.equal(writes, 0);
    binding.dispose();
  }
});

test('aggregate parent personal intent stays unavailable rather than mutating its first child', async () => {
  let requests = 0;
  const binding = createCataloguePersonalSelection({api: {request: async () => {requests++; throw new Error('unused');}}, scope, requestId: async () => crypto.randomUUID(), signal: new AbortController().signal, isCurrent: () => true});
  binding.service.setEntries([{id: 'show', kind: 'show', libraryId: 'tv', title: 'Show', playback: {itemId: 'first-episode'}}]);binding.service.selectLoaded();
  assert.equal(binding.service.getSnapshot().canApply, false);
  await binding.service.apply('favorite', true);
  assert.equal(requests, 0);
  binding.dispose();
});

const allPorts = {play: true, myList: true, favorite: true, watched: true};
const offeredActions = () => [
  {id: 'resume', labelKey: 'action.resume', enabled: true, playback: {itemId: entry.id, startMs: 2000}},
  ...['watchlist', 'favorite', 'watched', 'metadata_edit', 'delete'].map(id => ({id, labelKey: id, enabled: true})),
];
function capabilityOptions(api: DetailApi) {
  return {api, scope, requestId: async () => {throw new Error('A read must not issue a mutation ID');}, signal: new AbortController().signal, isCurrent: () => true};
}

test('selected leaf capabilities intersect actual Detail actions with real executor availability', async () => {
  const requests: string[] = [];
  const api: DetailApi = {request: async<T>(path: string, method = 'GET'): Promise<T> => {
    assert.equal(method, 'GET');requests.push(path);
    return {...detail(), actions: offeredActions()} as T;
  }};
  const selected = await readCatalogueSelectionItem(capabilityOptions(api), entry, allPorts);
  assert.deepEqual(selected, {kind: 'movie', id: 'movie', libraryId: 'movies', capabilities: selectionCapabilities('movie', allPorts)});
  assert.equal(requests.length, 1);
  assert.equal(selected.capabilities.edit, false);
  assert.equal(selected.capabilities.delete, false);
  assert.equal(selected.capabilities.queue, false);
  assert.equal(selected.capabilities.download, false);
  const noPorts = await readCatalogueSelectionItem(capabilityOptions(api), entry);
  assert.deepEqual(noPorts.capabilities, selectionCapabilities('movie', {}));
});

test('disabled actions, unrelated playback, missing availability and music Watched grant nothing', async () => {
  for (const available of [true, false]) {
    const api: DetailApi = {request: async<T>(): Promise<T> => ({...detail(), item: {...detail().item, available}, actions: [
      {id: 'play', labelKey: 'play', enabled: true, playback: {itemId: available ? 'other-item' : entry.id, startMs: 0}},
      {id: 'start_over', labelKey: 'start', enabled: true, playback: {itemId: entry.id, startMs: 0}},
      {id: 'unknown', labelKey: 'unknown', enabled: true, playback: {itemId: entry.id, startMs: 0}},
      ...['watchlist', 'favorite', 'watched'].map(id => ({id, labelKey: id, enabled: false})),
    ]}) as T};
    assert.deepEqual((await readCatalogueSelectionItem(capabilityOptions(api), entry, allPorts)).capabilities, selectionCapabilities('movie', {}));
  }
  const song = {...entry, kind: 'song' as const};
  const api: DetailApi = {request: async<T>(): Promise<T> => ({...detail(), item: {...detail().item, kind: 'song'}, actions: offeredActions()}) as T};
  const result = await readCatalogueSelectionItem(capabilityOptions(api), song, allPorts);
  assert.equal(result.capabilities.play, true);
  assert.equal(result.capabilities.watched, false);
});

test('capability read captures identity, transport and available ports before asynchronous work', async () => {
  let release!: (raw: unknown) => void;
  const api: DetailApi = {request: <T>() => new Promise<T>(resolve => {release = raw => resolve(raw as T);})};
  const options = capabilityOptions(api), ports = {...allPorts, favorite: false}, input = {...entry};
  const pending = readCatalogueSelectionItem(options, input, ports);
  ports.favorite = true;input.id = 'replacement';
  options.api = {request: async () => {throw new Error('Must retain captured principal');}};
  release({...detail(), actions: offeredActions()});
  const result = await pending;
  assert.equal(result.id, entry.id);
  assert.equal(result.capabilities.favorite, false);
  assert.equal(result.capabilities.myList, true);
});

test('unoffered parents stay unoffered and redirected or mismatched leaves are refused', async () => {
  let reads = 0;
  const options = capabilityOptions({request: async<T>(): Promise<T> => {reads++;return detail() as T;}});
  const parent = await readCatalogueSelectionItem(options, {id: 'show', libraryId: 'tv', kind: 'show', title: 'Show', playback: {itemId: 'first-episode'}}, allPorts);
  assert.deepEqual(parent.capabilities, selectionCapabilities('show', {}));
  await assert.rejects(readCatalogueSelectionItem(options, {...entry, navigation: {view: 'item', entityId: 'other-item'}}, allPorts), {code: 'catalogue-selection-unavailable'});
  assert.equal(reads, 0);
  for (const item of [{...detail().item, id: 'other-item'}, {...detail().item, kind: 'episode'}]) {
    await assert.rejects(readCatalogueSelectionItem(capabilityOptions({request: async<T>(): Promise<T> => ({...detail(), item}) as T}), entry, allPorts));
  }
});

test('source retirement or original abort refuses a late capability response without writes', async () => {
  for (const abort of [false, true]) {
    let current = true, writes = 0;
    const controller = new AbortController();
    const api: DetailApi = {request: async<T>(_path: string, method = 'GET'): Promise<T> => {
      if (method !== 'GET') writes++;
      if (abort) controller.abort();else current = false;
      return {...detail(), actions: offeredActions()} as T;
    }};
    await assert.rejects(readCatalogueSelectionItem({...capabilityOptions(api), signal: controller.signal, isCurrent: () => current}, entry, allPorts), {name: 'AbortError'});
    assert.equal(writes, 0);
  }
});


test('personal selection wrapper forwards acknowledgement only after a validated Detail receipt', async () => {
  for(const valid of [false,true]) {
    const acks:string[]=[],operations:string[]=[];
    const api:DetailApi={acknowledgeUserMutation:async key=>{acks.push(key);},request:async<T>(_path:string,method='GET',body?:unknown)=>{
      if(method==='GET')return detail() as T;
      const operation=body as {operationId:string};operations.push(operation.operationId);
      return {operationId:operation.operationId,serverId:'server',libraryId:'movies',itemId:'movie',viewerFence:valid?'detail-only':'other',personal:state(1)} as T;
    }};
    const binding=createCataloguePersonalSelection({api,scope,requestId:async()=>crypto.randomUUID(),signal:new AbortController().signal,isCurrent:()=>true});
    try{binding.service.setEntries([entry]);binding.service.selectLoaded();await binding.service.apply('favorite',true);
      assert.deepEqual(acks,valid?operations:[]);assert.equal(binding.service.getSnapshot().results[0].status,valid?'saved':'failed');
    }finally{binding.dispose();}
  }
});
