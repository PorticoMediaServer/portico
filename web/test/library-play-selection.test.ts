import test from 'node:test';
import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import {BrowseController} from '../src/screens/browse/controller.ts';
import {createLibraryPlaySelection} from '../src/screens/browse/play-selection.ts';
import {wholeViewQuery} from '../src/screens/browse/selection-query.ts';
import type {DetailApi} from '@core/detail.ts';
import type {V1QueuePlayer} from '@core/playback-v1/v1-queue-player.ts';
import type {QueueHeader} from '@core/playback-v1/types.ts';

const pivot = {id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid', 'list'], browsable: true, paging: 'offset'};
const caps = {library: {id: 'library', name: 'Movies', kind: 'movie', defaultView: 'discover'}, pivots: [pivot], resolvedPivot: pivot,
  fields: [], sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 60, maximumLimit: 100, cursorTtlMs: 1800000}};
const scope = {serverId: 'server', viewerId: 'actual-captured-viewer'};
function interactionNode() {
  return {isConnected: true, closest: () => null, setAttribute() {}, removeAttribute() {}} as unknown as HTMLElement;
}
function detail(id: string) {return {scope: {...scope, libraryId: 'library', itemId: id, viewerFence: 'detail-only'}, revision: {catalog: 77, viewer: 3},
  item: {id, libraryId: 'library', kind: 'movie', title: id, durationMs: 1000, progressMs: 0, available: true},
  personal: {watchlisted: false, favorite: false, rating: null, revision: 0}, actions: [
    {id: 'play', labelKey: 'action.play', enabled: true, playback: {itemId: id, startMs: 0}},
    ...['favorite', 'watchlist', 'watched', 'metadata_edit', 'delete'].map(id => ({id, labelKey: id, enabled: true})),
  ], metadata: {status: 'unavailable', ratings: [], genres: [], credits: []}};}
const queue: QueueHeader = {id: 'actual-port-receipt', revision: '1', total: 1, repeat: 'off', segments: []};
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}
async function settle() {for (let index = 0; index < 8; index++) await setImmediate();}
function setup(playback?: Pick<V1QueuePlayer, 'playSegments'>, readDetail: (id: string, signal?: AbortSignal) => Promise<unknown> = async id => detail(id),
  personal?: Readonly<{myList?: boolean; favorite?: boolean; watched?: boolean}>,
  whole?: (start: number, request: {query?: unknown; sort: unknown}) => unknown) {
  const navigation = new AbortController();let authority = true, detailReads = 0;
  const api: DetailApi = {async request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
    if (path.includes('browse-capabilities')) return caps as T;
    if (method === 'POST') {
      const request = body as {range: {start: number}; query?: unknown; sort: unknown}, start = request.range.start;
      // The approved descriptor is first-page only, exactly like the server.
      const wholeSelection = whole ? whole(start, request) : start === 0 ? {selector: {query: {libraryId: 'library', pivot: 'movies',
        ...(request.query ? {filter: request.query} : {}), sort: request.sort}}, catalogRevision: 'browse:opaque/actual', total: 1200,
        types: [{kind: 'movie', count: 1200, capabilityCounts: {play: 1200, queue: 0, playlist: 0, myList: 0, watched: 0, collection: 0,
          favorite: 0, download: 0, edit: 0, refresh: 0, delete: 0}, libraries: [{libraryId: 'library', count: 1200}]}]} : undefined;
      return {pivot: 'movies', applied: {query: request.query ?? null, sort: request.sort, seek: null},
        ...(wholeSelection ? {wholeSelection} : {}),
        entries: Array.from({length: Math.min(60, 1200 - start)}, (_, index) => ({id: 'movie-' + (start + index), libraryId: 'library', kind: 'movie', title: 'Movie ' + (start + index), available: true, playback: {itemId: 'movie-' + (start + index), startMs: 0}, navigation: {view: 'item', entityId: 'movie-' + (start + index)}})),
        pageInfo: {start, total: 1200, totalIsLowerBound: false, revision: 'browse:opaque/actual', hasMore: start + 60 < 1200, nextCursor: ''}, positionIndex: []} as T;
    }
    assert.equal(method, 'GET');detailReads++;
    const id = new URL(path, 'https://local.invalid').pathname.split('/').at(-2)!;
    return await readDetail(id, signal) as T;
  }};
  const source = new BrowseController(api, {libraryId: 'library', pivot: 'movies'}, () => authority);
  const selection = createLibraryPlaySelection({source, api, scope, principal: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'},
    requestId: async () => {throw new Error('No mutation operation may be issued by a grant read');}, signal: navigation.signal, isCurrent: () => authority,
    ...(personal ? {personal} : {}), playback});
  return {source, selection, navigation, detailReads: () => detailReads, authority: (value: boolean) => {authority = value;},
    async load() {selection.start();await source.load();await settle();}, dispose() {selection.dispose();source.dispose();}};
}

test('Library selection defers to an actual token and offers no grant or Detail read without an executor', async () => {
  const owner = setup();
  try {
    assert.equal(owner.selection.getSnapshot().phase, 'waiting');assert.equal(owner.detailReads(), 0);
    assert.deepEqual(await owner.selection.registerWindow(0, 2), []);
    assert.equal(owner.selection.getSnapshot().phase, 'waiting');assert.equal(owner.detailReads(), 0);
    await owner.load();const rows = await owner.selection.registerWindow(0, 2);
    assert.equal(owner.detailReads(), 0);assert.equal(rows.length, 3);
    const context = owner.selection.getSnapshot().selection!.context;
    assert.deepEqual(context.revision, {pageRevision: 'browse:opaque/actual'});assert(!('privacyFence' in context.scope));
    assert.deepEqual(JSON.parse(context.scope.viewId), {libraryId: 'library', pivot: 'movies', query: null, sort: [{field: 'title', direction: 'asc'}]});
    owner.selection.selectLoaded();assert.equal(owner.selection.getSnapshot().selection!.summary.count, 3);
    assert.equal(owner.selection.getSnapshot().canPlay, false);
    await assert.rejects(owner.selection.play(), {code: 'library-play-selection-unavailable'});
  } finally {owner.dispose();}
});

test('original row interaction survives preview publications but retires before window replacement', async () => {
  const owner = setup();
  try {
    await owner.load();const rows = await owner.selection.registerWindow(0, 2);
    owner.selection.toggle(rows[0]);
    const source = owner.selection.interaction(rows[2]), admission = source.capture(interactionNode())!;
    admission.preview(true);
    assert.equal(owner.selection.rangePreviewed(rows[1]), true);
    assert.equal(owner.selection.interaction(rows[2]), source);
    assert.equal(admission.current(), true);
    let retired = false;admission.onRetire(() => {retired = true;});
    const replacement = owner.selection.registerWindow(1, 3);
    assert.equal(retired, true);assert.equal(admission.current(), false);
    const next = await replacement, nextAdmission = owner.selection.interaction(next[1]).capture(interactionNode())!;
    nextAdmission.preview(true);admission.preview(false);
    assert.equal(nextAdmission.current(), true);
  } finally {owner.dispose();}
});

test('an original Library physical-row cleanup cannot clear a successor preview', async () => {
  const owner = setup();
  try {
    await owner.load();const rows = await owner.selection.registerWindow(0, 2);owner.selection.toggle(rows[0]);
    const source = owner.selection.interaction(rows[2]), node = interactionNode(), first = source.capture(node)!;
    first.preview(true);const second = source.capture(node)!;second.preview(true);
    first.retire();first.preview(false);assert.equal(owner.selection.rangePreviewed(rows[1]), true);
    second.retire();assert.equal(owner.selection.rangePreviewed(rows[1]), false);
  } finally {owner.dispose();}
});

test('actual Detail grants resolve at four concurrent reads and remain bounded to the requested resident window', async () => {
  let active = 0, peak = 0;
  const pending: {id: string; result: ReturnType<typeof deferred<unknown>>}[] = [];
  const owner = setup({playSegments: async () => queue}, async id => {
    active++;peak = Math.max(peak, active);const result = deferred<unknown>();pending.push({id, result});
    try {return await result.promise;} finally {active--;}
  });
  try {
    await owner.load();let done = false;
    const read = owner.selection.registerWindow(0, 9).then(rows => {done = true;return rows;});
    await settle();assert.equal(active, 4);
    for (let round = 0; round < 10 && !done; round++) {for (const value of pending.splice(0)) value.result.resolve(detail(value.id));await settle();}
    const rows = await read;assert.equal(peak, 4);assert.equal(owner.detailReads(), 10);assert.equal(rows.length, 10);
    assert(rows.every(row => row.capabilities.play && !row.capabilities.myList && !row.capabilities.favorite && !row.capabilities.edit && !row.capabilities.queue));
    assert.deepEqual(rows.map(row => row.position), Array.from({length: 10}, (_, index) => index));
    await assert.rejects(owner.selection.registerWindow(0, 60), {code: 'library-play-selection-unavailable'});
  } finally {owner.dispose();}
});

test('replacement windows reject old row handles but explicit picks survive real page eviction', async () => {
  let ids: readonly string[] | undefined;
  const owner = setup({playSegments: async segments => {const first = segments[0].source;assert('items' in first);ids = first.items.ids;return queue;}});
  try {
    await owner.load();const first = await owner.selection.registerWindow(0, 2);owner.selection.toggle(first[0]);
    const sameEntries = await owner.selection.registerWindow(1, 3);
    assert.throws(() => owner.selection.toggle(first[1]), {name: 'AbortError'});
    assert.equal(sameEntries[0].position, 1);
    for (const start of [120, 300, 600, 900]) {owner.source.collection.ensureRange(start, start + 5);await settle();}
    assert.equal(owner.source.contains(first[0].entry), false);assert.equal(owner.selection.getSnapshot().selection!.summary.count, 1);
    assert.equal(owner.selection.getSnapshot().canPlay, true);assert.equal(await owner.selection.play(), queue);
    assert.deepEqual(ids, ['movie-0']);
  } finally {owner.dispose();}
});

test('replacing the mounted window cancels its unfinished grants before admitting new rows', async () => {
  const pending: {id: string; signal?: AbortSignal; finish: () => void}[] = [];
  const owner = setup({playSegments: async () => queue}, (id, signal) => new Promise((resolve, reject) => {
    const abort = () => reject(new DOMException('cancelled', 'AbortError'));
    signal!.addEventListener('abort', abort, {once: true});
    pending.push({id, signal, finish: () => {signal!.removeEventListener('abort', abort);resolve(detail(id));}});
  }));
  try {
    await owner.load();
    const first = assert.rejects(owner.selection.registerWindow(0, 9), {name: 'AbortError'});
    await settle();assert.equal(pending.length, 4);
    const next = owner.selection.registerWindow(10, 11);
    await first;await settle();
    assert(pending.slice(0, 4).every(value => value.signal!.aborted));
    assert.deepEqual(pending.slice(4).map(value => value.id), ['movie-10', 'movie-11']);
    for (const value of pending.slice(4)) value.finish();
    const rows = await next;assert.deepEqual(rows.map(row => row.position), [10, 11]);
    assert.equal(owner.detailReads(), 6);
  } finally {owner.dispose();for (const value of pending) value.finish();}
});

test('selection and navigation cancel a pending real-port call; reselecting or Back cannot revive it', async () => {
  for (const navigate of [false, true]) {
    const held = deferred<QueueHeader>();let lifetime: {signal?: AbortSignal; isCurrent?: () => boolean} | undefined;
    const owner = setup({playSegments: async (_segments, options) => {lifetime = options;return held.promise;}});
    try {
      await owner.load();const rows = await owner.selection.registerWindow(0, 2);owner.selection.toggle(rows[0]);
      const playing = owner.selection.play();assert.equal(lifetime?.isCurrent?.(), true);
      if (navigate) owner.navigation.abort();else {owner.selection.clear();owner.selection.toggle(rows[0]);}
      assert.equal(lifetime?.signal?.aborted, true);assert.equal(lifetime?.isCurrent?.(), false);
      owner.authority(true);held.resolve(queue);await assert.rejects(playing, {name: 'AbortError'});
      if (navigate) {assert.equal(owner.selection.getSnapshot().phase, 'retired');assert.throws(() => owner.selection.toggle(rows[0]), {name: 'AbortError'});}
    } finally {held.resolve(queue);owner.dispose();}
  }
});

test('query and authority changes permanently retire the original model even if the same token returns', async () => {
  for (const query of [false, true]) {
    const owner = setup({playSegments: async () => queue});
    try {
      await owner.load();const rows = await owner.selection.registerWindow(0, 2);owner.selection.toggle(rows[0]);
      if (query) owner.source.setQuery([], {field: 'title', direction: 'desc'});else owner.authority(false);
      assert.equal(owner.selection.getSnapshot().phase, 'retired');owner.authority(true);
      if (query) {await settle();owner.source.setQuery([], {field: 'title', direction: 'asc'});await settle();}
      assert.equal(owner.selection.getSnapshot().phase, 'retired');assert.equal(owner.selection.getSnapshot().canPlay, false);
      assert.throws(() => owner.selection.toggle(rows[0]), {name: 'AbortError'});
    } finally {owner.dispose();}
  }
});

test('an actual denied grant retires cached selections before any Play can be dispatched', async () => {
  let denied = false, plays = 0;
  const owner = setup({playSegments: async () => {plays++;return queue;}}, async id => {
    if (denied) throw Object.assign(new Error('denied'), {status: 403, code: 'forbidden'});
    return detail(id);
  });
  try {
    await owner.load();const rows = await owner.selection.registerWindow(0, 1);owner.selection.toggle(rows[0]);
    assert.equal(owner.selection.getSnapshot().canPlay, true);denied = true;
    await assert.rejects(owner.selection.registerWindow(2, 3));
    assert.equal(owner.selection.getSnapshot().phase, 'error');assert.equal(owner.selection.getSnapshot().canPlay, false);
    await assert.rejects(owner.selection.play(), {name: 'AbortError'});assert.equal(plays, 0);
  } finally {owner.dispose();}
});

test('the original navigation signal cancels all queued grant reads and admits no late rows', async () => {
  const pending: {id: string; signal?: AbortSignal; result: ReturnType<typeof deferred<unknown>>}[] = [];
  const owner = setup({playSegments: async () => queue}, async (id, signal) => {const result = deferred<unknown>();pending.push({id, signal, result});return result.promise;});
  try {
    await owner.load();const read = owner.selection.registerWindow(0, 9);await settle();assert.equal(owner.detailReads(), 4);
    owner.navigation.abort();await assert.rejects(read, {name: 'AbortError'});assert(pending.every(value => value.signal?.aborted));
    for (const value of pending) value.result.resolve(detail(value.id));await settle();
    assert.equal(owner.detailReads(), 4);assert.deepEqual(owner.selection.getSnapshot().rows, []);
  } finally {for (const value of pending) value.result.resolve(detail(value.id));owner.dispose();}
});

test('the controller keeps the first page descriptor for its own revision and drops it on invalidate', async () => {
  const owner = setup({playSegments: async () => queue});
  try {
    await owner.load();
    const descriptor = owner.source.wholeSelection();
    assert.equal(descriptor?.total, 1200);
    assert.equal(descriptor?.catalogRevision, 'browse:opaque/actual');
    owner.source.collection.ensureRange(60, 60);await settle();
    // Page two carries no descriptor and must not erase the first page's exact one.
    assert.equal(owner.source.wholeSelection(), descriptor);
    owner.source.setQuery([], {field: 'title', direction: 'desc'});
    assert.equal(owner.source.wholeSelection(), undefined);
    await settle();
    assert.deepEqual(owner.source.wholeSelection()?.sort, [{field: 'title', direction: 'desc'}]);
  } finally {owner.dispose();}
});

test('Select all registers the server descriptor and keeps the mounted window a member of that exact view', async () => {
  const owner = setup({playSegments: async () => queue});
  try {
    await owner.load();
    const descriptor = owner.source.wholeSelection()!, stale = await owner.selection.registerWindow(0, 2);
    assert.equal(owner.selection.selectWhole(wholeViewQuery(descriptor)).ok, true);
    const selected = owner.selection.getSnapshot().selection!;
    assert.equal(selected.summary.count, 1200);
    assert.equal(selected.target?.mode, 'query');
    assert.equal(selected.target?.mode === 'query' ? selected.target.expected.catalogRevision : '', 'browse:opaque/actual');
    await settle();
    const rows = owner.selection.getSnapshot().rows;
    assert.equal(rows.length, 3);
    // The pre-query window handles are retired; the same resident rows come back bound to the query.
    assert.throws(() => owner.selection.toggle(stale[0]!), {name: 'AbortError'});
    assert.equal(owner.selection.preview(rows[0]!), false);
    assert.equal(owner.selection.toggle(rows[0]!).ok, true);
    assert.equal(owner.selection.getSnapshot().selection!.summary.count, 1199);
    assert.equal(owner.selection.preview(rows[1]!), true);
    assert.equal(owner.selection.selected(rows[0]!), false);
    assert.equal(owner.selection.selected(rows[1]!), true);
    assert.equal(owner.selection.toggle(rows[1]!).ok, true);
    assert.equal(owner.selection.getSnapshot().selection!.summary.count, 1198);
    assert.equal(owner.selection.selected(rows[1]!), false);
  } finally {owner.dispose();}
});

test('a whole-view descriptor from another page revision cannot become this page selection', async () => {
  const owner = setup({playSegments: async () => queue});
  try {
    await owner.load();
    const query = wholeViewQuery(owner.source.wholeSelection()!);
    assert.throws(() => owner.selection.selectWhole({...query, catalogRevision: 'browse:other', id: 'whole:other'}), {code: 'library-play-selection-unavailable'});
    assert.equal(owner.selection.getSnapshot().selection?.active, false);
  } finally {owner.dispose();}
});

test('personal capabilities come from the title’s own Detail actions, with or without a playback port', async () => {
  const withPorts = setup(undefined, undefined, {myList: true, favorite: true, watched: true});
  try {
    await withPorts.load();const rows = await withPorts.selection.registerWindow(0, 1);
    assert.equal(rows[0]?.capabilities.play, false);
    assert.equal(rows[0]?.capabilities.myList, true);
    assert.equal(rows[0]?.capabilities.favorite, true);
    assert.equal(rows[0]?.capabilities.watched, true);
    assert.equal(withPorts.detailReads(), 2);
  } finally {withPorts.dispose();}
  const without = setup();
  try {
    await without.load();const rows = await without.selection.registerWindow(0, 1);
    assert.equal(rows[0]?.capabilities.myList, false);
    assert.equal(rows[0]?.capabilities.favorite, false);
    assert.equal(rows[0]?.capabilities.watched, false);
    assert.equal(without.detailReads(), 0);
  } finally {without.dispose();}
});

test('a personal port grants nothing when Detail does not enable that exact action', async () => {
  const owner = setup(undefined, async id => {const value = detail(id);value.actions = value.actions.filter(action => action.id !== 'watchlist');return value;},
    {myList: true, favorite: true, watched: true});
  try {
    await owner.load();const rows = await owner.selection.registerWindow(0, 1);
    assert.equal(rows[0]?.capabilities.myList, false);
    assert.equal(rows[0]?.capabilities.favorite, true);
    assert.equal(rows[0]?.capabilities.watched, true);
  } finally {owner.dispose();}
});

test('a descriptor for another library or a later page never becomes this view', async () => {
  const foreign = setup(undefined, undefined, undefined, (start, request) => start === 0 ? {selector: {query: {libraryId: 'other', pivot: 'movies', sort: request.sort}},
    catalogRevision: 'browse:opaque/actual', total: 3, types: [{kind: 'movie', count: 3, capabilityCounts: {play: 3, queue: 0, playlist: 0, myList: 0,
      watched: 0, collection: 0, favorite: 0, download: 0, edit: 0, refresh: 0, delete: 0}, libraries: [{libraryId: 'other', count: 3}]}]} : undefined);
  try {
    await foreign.load();
    assert.equal(foreign.source.wholeSelection(), undefined);
  } finally {foreign.dispose();}
  const page = (total: number) => ({selector: {query: {libraryId: 'library', pivot: 'movies', sort: [{field: 'title', direction: 'asc' as const}]}},
    catalogRevision: 'browse:opaque/actual', total, types: [{kind: 'movie', count: total, capabilityCounts: {play: total, queue: 0, playlist: 0, myList: 0,
      watched: 0, collection: 0, favorite: 0, download: 0, edit: 0, refresh: 0, delete: 0}, libraries: [{libraryId: 'library', count: total}]}]});
  const later = setup(undefined, undefined, undefined, start => page(start === 0 ? 1200 : 3));
  try {
    await later.load();
    assert.equal(later.source.wholeSelection()?.total, 1200);
    later.source.collection.ensureRange(60, 60);await settle();
    assert.equal(later.source.wholeSelection()?.total, 1200);
  } finally {later.dispose();}
});

test('retry drops the held descriptor before any re-read', async () => {
  const owner = setup({playSegments: async () => queue});
  try {
    await owner.load();
    assert.notEqual(owner.source.wholeSelection(), undefined);
    owner.source.retry();
    assert.equal(owner.source.wholeSelection(), undefined);
  } finally {owner.dispose();}
});

test('Select all refuses a same-revision selector that is not this source descriptor', async () => {
  const owner = setup({playSegments: async () => queue});
  try {
    await owner.load();
    const query = wholeViewQuery(owner.source.wholeSelection()!);
    const forged = {...query, selector: {query: {...query.selector.query, filter: {field: 'year', operator: 'at-least', value: 2000}}}};
    assert.throws(() => owner.selection.selectWhole(forged), {code: 'library-play-selection-unavailable'});
    assert.equal(owner.selection.getSnapshot().selection?.active, false);
  } finally {owner.dispose();}
});

test('Reading rows never enter the catalogue selection model or close gaps between admitted rows', async () => {
  const entries = [
    {id: 'movie-a', libraryId: 'library', kind: 'movie' as const, title: 'A'},
    {id: 'epub', libraryId: 'library', kind: 'ebook' as const, title: 'Publication'},
    {id: 'movie-b', libraryId: 'library', kind: 'movie' as const, title: 'B'},
    {id: 'comic', libraryId: 'library', kind: 'comic' as const, title: 'Comic'},
  ];
  let reads = 0;
  const source = {
    viewIdentity: () => 'original-mixed-window',
    getSnapshot: () => ({phase: 'ready', revision: 'accepted-page', queryRevision: 1}),
    subscribe: () => () => {}, contains: (entry: unknown) => entries.includes(entry as typeof entries[number]),
    collection: {subscribe: () => () => {}, itemAt: (index: number) => entries[index] ? {entry: entries[index]} : undefined},
  };
  const selection = createLibraryPlaySelection({source, scope, principal: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'},
    signal: new AbortController().signal, isCurrent: () => true, api: {async request<T>(): Promise<T> {reads++; throw new Error('No grant read');}},
    requestId: async () => {throw new Error('No mutation');}});
  try {
    const rows = await selection.registerWindow(0, 3);
    assert.deepEqual(rows.map(row => row.position), [0, 2]);
    assert.deepEqual(rows.map(row => row.entry.id), ['movie-a', 'movie-b']);
    selection.selectLoaded(); assert.equal(selection.getSnapshot().selection?.summary.count, 2);
    assert.equal(reads, 0); assert.equal(selection.getSnapshot().canPlay, false);
  } finally {selection.dispose();}
});
