import test from 'node:test';
import assert from 'node:assert/strict';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import {createDetailBindings} from '../src/screens/detail/bindings.ts';
import {createShowBindings} from '../src/screens/detail/show-bindings.ts';
import {createPersonBindings} from '../src/screens/detail/person-bindings.ts';
import {createSearchBindings} from '../src/screens/search/bindings.ts';
import {createEntryDetailsBindings} from '../src/screens/detail/entry-details.ts';
import {createWatchBodyBindings} from '../src/screens/detail/watch-body.ts';
import {createScreenPlayEffect} from '../src/screens/browse/playback-effect.ts';
import type {SelectionContext} from '@core/selection-model/index.ts';
import type {ContentEntry} from '@core/library-content.ts';
import {browseCapabilitiesPath} from '@core/browse.ts';
import {titleCreditsWindow} from '../src/screens/detail/credits-window.ts';
import {setImmediate} from 'node:timers/promises';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import type {WatchVersionRequest} from '../src/screens/watch/version-presentation.ts';

function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return {promise, resolve}; }
function setup(handler: (path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<unknown>) {
  let current = true, requests = 0;
  const context = {
    api: {
      request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => { requests++; return handler(path, method, body, signal) as Promise<T>; },
      requestRaw: async (path: string, method?: string, options?: {body?: unknown; signal?: AbortSignal}) => { requests++; return {status: 200, headers: {}, body: await handler(path, method, options?.body, options?.signal)}; },
    },
    scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])}, isCurrent: () => current,
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry: () => {}, openSettings: () => {}, play: async () => {}, reportError: () => {}},
  } as unknown as ScreenContext;
  return {context, requests: () => requests, retire: () => { current = false; }};
}
const personPage = (id: string) => ({serverId: 'server', viewerFence: 'fence', revision: {catalog: 1, viewer: 1}, person: {id, name: id, revision: 1}, credits: [], pageInfo: {total: 0, nextCursor: ''}});
const detailPage = (id: string) => ({scope: {serverId: 'server', libraryId: 'library', itemId: id, viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1},
  item: {id, libraryId: 'library', kind: 'movie', title: id, durationMs: 1000, progressMs: 0, available: true},
  personal: {watchlisted: false, favorite: false, rating: null, revision: 1, watched: false, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: []},
  actions: [], metadata: {status: 'unavailable', ratings: [], genres: [], credits: []}});
function searchPage(path: string, fence = 'search-only') {
  const params = new URL(path, 'https://local.invalid').searchParams, selected = params.get('group') ?? '';
  const ids = (['movies', 'shows'] as const).filter(id => selected ? selected === id : params.get('groups')?.split(',').includes(id));
  const libraryIds = params.get('libraryIds')?.split(',') ?? [], libraryId = libraryIds[0] ?? 'library';
  const groups = ids.map(id => {
    const itemId = libraryId + '-' + id, kind = id === 'movies' ? 'movie' : 'show';
    return {id, title: id, entityKind: kind, status: 'success', errorCode: '',
      items: [{id: itemId, libraryId, kind, title: itemId, navigation: {view: id === 'movies' ? 'item' : 'show', entityId: itemId}}],
      totalCount: (id === 'movies' ? 5 : 7) * (libraryId === 'other' ? 10 : 1), totalCountExact: true, hasMore: false, nextCursor: ''};
  });
  return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: fence}, revision: {catalog: 4, viewer: 5},
    heading: {key: 'search.title', fallback: ''}, query: {q: params.get('q'), sort: params.get('sort') ?? 'relevance', direction: params.get('direction') ?? 'desc',
      group: selected, groups: ids, libraryIds, limit: Number(params.get('limit')), searchMode: 'normalized_prefix', recorded: params.get('record') === '1'},
    groups, capabilities: {groups: groups.map(group => ({id: group.id, entityKind: group.entityKind, title: group.title, available: true, sorts: ['relevance', 'title']})),
      sorts: ['relevance', 'title'], directions: ['asc', 'desc'], maxLimit: 40, groupBudgetMs: 600}};
}

test('Search All continuation admits its appended action objects and withdraws them with the parent query', async () => {
  const opened: unknown[] = [], played: unknown[] = [];
  const owner = setup(async path => {
    const params = new URL(path, 'https://local.invalid').searchParams, next = !!params.get('cursor'), raw = searchPage(path);
    for (const result of raw.groups) {
      result.nextCursor = next ? '' : 'offered-cursor';result.hasMore = !next;
      result.items = result.items.map(item => ({...item, id: item.id + (next ? '-next' : ''), available: true,
        playback: {itemId: item.id + (next ? '-next' : ''), startMs: 7000}, navigation: {...item.navigation, entityId: item.id + (next ? '-next' : '')}}));
    }
    return raw;
  });
  const binding = createSearchBindings({...owner.context, effects: {...owner.context.effects, openEntry: value => {opened.push(value);}, play: async value => {played.push(value);}}});
  try {
    await binding.select('harbor', {groups: ['movies']});
    const parent = binding.service.getSnapshot(), first = binding.results.getSnapshot().groups[0];
    await binding.results.next(first);
    const combined = binding.results.getSnapshot(), continued = combined.groups[0].items[1];
    assert.strictEqual(binding.service.getSnapshot(), parent);
    assert.strictEqual(combined.data?.groups, combined.groups);
    assert.equal(combined.groups[0].nextCursor, '');
    binding.open(continued);await binding.play(continued);
    assert.deepEqual(opened, [continued]);
    // The pressed card names the starting dock (title and art) until the real dock replaces it.
    // The pressed card names the starting dock (title and art) until the real dock replaces it.
    assert.deepEqual(JSON.parse(JSON.stringify(played)), [{libraryId: 'library', itemId: 'library-movies-next', startSeconds: 7,
      display: {subject: {kind: 'video', item: {id: 'library-movies-next', kind: 'movie', title: 'library-movies'}}}}]);
    await binding.select('other query', {groups: ['movies']});
    assert.throws(() => binding.open(continued), {name: 'AbortError'});
    await assert.rejects(binding.play(continued), {name: 'AbortError'});
    assert.equal(opened.length, 1);assert.equal(played.length, 1);
  } finally {binding.dispose();}
});

test('Search summary counts follow actual library scope and ordering when the text and revisions stay the same', async () => {
  const owner = setup(async path => searchPage(path)), binding = createSearchBindings(owner.context);
  try {
    await binding.select('harbor', {groups: ['movies', 'shows'], libraryIds: ['library']});
    assert.equal(binding.service.getSnapshot().phase, 'ready');
    assert.deepEqual(binding.summary().map(group => group.totalCount), [5, 7]);
    await binding.select('harbor', {group: 'movies', libraryIds: ['library']});
    assert.deepEqual(binding.summary().map(group => group.totalCount), [5, 7], 'same-query group narrowing retains server summary');
    await binding.select('harbor', {group: 'movies', libraryIds: ['other']});assert.deepEqual(binding.summary(), []);
    await binding.select('harbor', {groups: ['movies', 'shows'], libraryIds: ['other']});
    assert.deepEqual(binding.summary().map(group => group.totalCount), [50, 70]);
    await binding.select('harbor', {group: 'movies', libraryIds: ['other'], sort: 'title'});assert.deepEqual(binding.summary(), []);
  } finally {binding.dispose();}
});

test('Search summary is immutable and unavailable during replacement reads or authority retirement', async () => {
  const held = deferred<unknown>();let hold = false, heldPath = '';
  const owner = setup(async path => {if (hold) {heldPath = path;return held.promise;}return searchPage(path);}), binding = createSearchBindings(owner.context);
  try {
    await binding.select('harbor', {groups: ['movies', 'shows'], libraryIds: ['library']});
    const summary = binding.summary();assert(Object.isFrozen(summary));assert(Object.isFrozen(summary[0]));
    hold = true;
    const read = binding.select('harbor', {group: 'movies', libraryIds: ['other']});
    assert.deepEqual(binding.summary(), []);
    held.resolve(searchPage(heldPath));await read;
    assert.deepEqual(binding.summary(), []);
    hold = false;await binding.select('harbor', {groups: ['movies', 'shows'], libraryIds: ['library']});
    assert.deepEqual(binding.summary().map(group => group.totalCount), [5, 7]);
    owner.retire();assert.deepEqual(binding.summary(), []);
  } finally {binding.dispose();}
});

test('a changed Search query retires its original caller through the actual screen play effect', async () => {
  const held = deferred<void>();let guard: (() => boolean) | undefined;
  const owner = setup(async path => {
    const raw = searchPage(path);
    return {...raw, groups: raw.groups.map(group => ({...group, items: group.items.map(item => item.kind === 'movie'
      ? {...item, available: true, playback: {itemId: item.id, startMs: 0}} : item)}))};
  });
  const effect = createScreenPlayEffect({isCurrent: owner.context.isCurrent, runtime: () => runtime});
  const runtime = {async playFromScreen(_target: unknown, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) {guard = lifetime.isCurrent;await held.promise;}};
  const binding = createSearchBindings({...owner.context, effects: {...owner.context.effects, play: effect}});
  try {
    await binding.select('harbor', {groups: ['movies', 'shows']});const entry = binding.service.getSnapshot().groups[0].items[0];
    const play = binding.play(entry);assert.equal(guard?.(), true);
    await binding.select('different', {groups: ['movies', 'shows']});assert.equal(guard?.(), false);
    held.resolve();await assert.rejects(play, {name: 'AbortError'});
  } finally {held.resolve();binding.dispose();}
});

test('paused or restarted Search never revives an original Play when the same query and revisions return', async () => {
  for (const change of ['pause', 'same-query', 'round-trip']) {
    const held = deferred<void>();let guard: (() => boolean) | undefined;
    const owner = setup(async path => {
      const raw = searchPage(path);
      return {...raw, groups: raw.groups.map(group => ({...group, items: group.items.map(item => item.kind === 'movie'
        ? {...item, available: true, playback: {itemId: item.id, startMs: 0}} : item)}))};
    });
    const runtime = {async playFromScreen(_target: unknown, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) {
      guard = lifetime.isCurrent;await held.promise;
    }};
    const effect = createScreenPlayEffect({isCurrent: owner.context.isCurrent, runtime: () => runtime});
    const binding = createSearchBindings({...owner.context, effects: {...owner.context.effects, play: effect}});
    try {
      await binding.select('harbor', {groups: ['movies', 'shows']});
      const entry = binding.service.getSnapshot().groups[0].items[0];
      assert('libraryId' in entry && entry.libraryId);
      const play = binding.context.effects.play({libraryId: entry.libraryId, itemId: entry.id, startSeconds: 0}, new AbortController().signal);
      assert.equal(guard?.(), true);
      if (change === 'pause') {binding.service.pause();assert.equal(guard?.(), false);}
      if (change === 'round-trip') await binding.select('different', {groups: ['movies', 'shows']});
      await binding.select('harbor', {groups: ['movies', 'shows']});
      assert.equal(binding.service.getSnapshot().phase, 'ready');assert.equal(guard?.(), false, change);
      held.resolve();await assert.rejects(play, {name: 'AbortError'});
    } finally {held.resolve();binding.dispose();}
  }
});

test('a replaced Detail projection retires its original caller through the actual screen play effect', async () => {
  const held = deferred<void>();let guard: (() => boolean) | undefined;
  const owner = setup(async path => {
    const id = path.includes('/two/') ? 'two' : 'one';
    return {...detailPage(id), actions: [{id: 'play', labelKey: 'action.play', enabled: true, playback: {itemId: id, startMs: 0}}]};
  });
  const runtime = {async playFromScreen(_target: unknown, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) {guard = lifetime.isCurrent;await held.promise;}};
  const binding = createDetailBindings({...owner.context, effects: {...owner.context.effects,
    play: createScreenPlayEffect({isCurrent: owner.context.isCurrent, runtime: () => runtime})}});
  try {
    await binding.select({libraryId: 'library', itemId: 'one'});const data = binding.service.getSnapshot().data!;
    const play = binding.play(data, 'play');assert.equal(guard?.(), true);
    await binding.select({libraryId: 'library', itemId: 'two'});assert.equal(guard?.(), false);
    held.resolve();await assert.rejects(play, {name: 'AbortError'});
  } finally {held.resolve();binding.dispose();}
});
const showPage = () => ({scope: {serverId: 'server', libraryId: 'library', viewerFence: 'show-fence'}, revision: {catalog: 3, viewer: 2},
  show: {id: 'show', libraryId: 'library', title: 'show', libraryKind: 'tv', providerMatchStatus: 'unresolved'},
  seasons: [{id: 'season', showId: 'show', number: 1, title: 'season'}], seasonTotalCount: 1, nextSeasonCursor: '', groups: [], selected: {seasonId: 'season'},
  episodes: {scope: {serverId: 'server', libraryId: 'library', libraryKind: 'tv', view: 'season', entityId: 'season', viewerFence: 'show-fence'}, revision: {catalog: 3, viewer: 2},
    heading: {key: 'entity.title', fallback: 'show'}, navigation: [], query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'},
    sorts: [{id: 'episode', labelKey: 'sort.episode', directions: ['asc']}], filters: [], sections: [{id: 'episodes', type: 'list', heading: {key: 'content.episodes', fallback: 'episodes'},
      entries: [{id: 'one', kind: 'episode', title: 'one', libraryId: 'library', available: true, navigation: {view: 'item', entityId: 'one'}, playback: {itemId: 'one', startMs: 10}}], totalCount: 1, nextCursor: ''}]}});

test('show More favorites the show container and retires with the original page', async () => {
  const writes: {path: string; body: unknown}[] = [], state = {revision: 1, watched: false, watermark: '', watchlisted: false, favorite: false, libraryId: 'library'};
  const owner = setup(async (path, method, body) => {
    if (path.includes('/show-workspace')) return showPage();
    if (method === 'PUT') {writes.push({path, body}); return {...state, favorite: true};}
    return state;
  });
  const binding = createShowBindings(owner.context);
  try {
    await binding.select({libraryId: 'library', showId: 'show'});
    const data = binding.service.getSnapshot().data!;
    await binding.setFavorite(data, true);
    assert.deepEqual(writes, [{path: '/v1/containers/show/show/personal-state', body: {expectedRevision: 0, favorite: true}}]);
    assert.equal(binding.personal.getSnapshot()?.favorite, true);
    owner.retire();
    await assert.rejects(binding.setFavorite(data, false), {name: 'AbortError'});
    assert.equal(writes.length, 1);
  } finally {binding.dispose();}
});

test('Watch body retains real title state across repeated presentation loads and retires My List', async () => {
  const owner = setup(async () => ({...detailPage('movie'), actions: [{id: 'watchlist', labelKey: 'action.watchlist', enabled: true}]}));
  const binding = createWatchBodyBindings(owner.context, {libraryId: 'library', itemId: 'movie', kind: 'movie'});
  assert.equal(owner.requests(), 0);
  assert.deepEqual(binding.myList.getSnapshot(), {available: false, busy: false});
  await binding.load();
  assert.equal(binding.body.kind, 'title');
  if (binding.body.kind !== 'title') throw new Error('Expected actual title body');
  binding.body.binding.view.tab('details');
  const reads = owner.requests();
  await binding.load();
  assert.equal(owner.requests(), reads);
  assert.equal(binding.body.binding.view.getSnapshot().tab, 'details');
  assert.deepEqual(binding.myList.getSnapshot(), {available: true, watchlisted: false, busy: false});
  owner.retire();
  await assert.rejects(binding.myList.set(true), {name: 'AbortError'});
  assert.equal(owner.requests(), reads);
  binding.dispose();
  assert.equal(binding.body.binding.service.getSnapshot().data, null);
  assert.deepEqual(binding.myList.getSnapshot(), {available: false, busy: false});
  assert.throws(() => binding.load(), {name: 'AbortError'});
});

test('domain factory creation is inert and disposal erases all private snapshots', () => {
  const owner = setup(async () => { throw new Error('No constructor request is authorized'); });
  const detail = createDetailBindings(owner.context), show = createShowBindings(owner.context), person = createPersonBindings(owner.context), search = createSearchBindings(owner.context);
  assert.equal(owner.requests(), 0);
  detail.dispose(); show.dispose(); person.dispose(); search.dispose();
  assert.equal(detail.service.getSnapshot().data, null);
  assert.equal(show.service.getSnapshot().data, null);
  assert.equal(show.personal.getSnapshot(), undefined);
  assert.equal(person.getSnapshot().page, undefined);
  assert.equal(search.service.getSnapshot().data, null);
  assert.equal(search.genres.getSnapshot().phase, 'idle');
  assert.deepEqual(search.genres.getSnapshot().genres, []);
  assert.deepEqual(search.session(), {text: '', group: undefined});
});

test('a bare item-library resolution aborts at disposal and never starts detail with a late reply', async () => {
  const response = deferred<unknown>(); let signal: AbortSignal | undefined;
  const owner = setup(async (_path, _method, _body, input) => { signal = input; return response.promise; });
  const binding = createDetailBindings(owner.context), read = binding.select({itemId: 'one'});
  assert.equal(owner.requests(), 1);
  binding.dispose(); assert.equal(signal?.aborted, true);
  response.resolve({id: 'one', libraryId: 'library', kind: 'movie', title: 'one', available: true, durationMs: 1000, progressMs: 0});
  await assert.rejects(read, {name: 'AbortError'});
  assert.equal(owner.requests(), 1);
  assert.equal(binding.service.getSnapshot().data, null);
});

test('a family or profile handover refuses even a newly invoked old service request', async () => {
  const owner = setup(async () => ({})), binding = createSearchBindings(owner.context);
  owner.retire();
  await assert.rejects(binding.select('one'), {name: 'AbortError'});
  await assert.rejects(binding.recent(), {name: 'AbortError'});
  assert.equal(owner.requests(), 0);
  binding.dispose();
});

test('an episode tile reads current playback permission and resume without opening its panel', async () => {
  const starts: unknown[] = [];
  let allowed = true;
  const owner = setup(async path => path.includes('/show-workspace') ? showPage() : {...detailPage('one'), actions: [{id: 'resume', labelKey: 'entry.resume', enabled: allowed, playback: {itemId: 'one', startMs: 725}}]});
  const binding = createShowBindings({...owner.context, effects: {...owner.context.effects, play: async target => { starts.push(target); }}});
  await binding.service.select({libraryId: 'library', showId: 'show'});
  const data = binding.service.getSnapshot().data!;
  assert.ok(data);
  const entry = data.episodes.sections[0].entries[0];
  await binding.playEpisode(data, entry);
  assert.deepEqual(starts, [{itemId: 'one', libraryId: 'library', startSeconds: 0.725}]);
  assert.equal(binding.service.getSnapshot().anchor.expandedEpisodeId, null);
  assert.equal(binding.episode.service.getSnapshot().data, null);
  allowed = false;
  await assert.rejects(binding.playEpisode(data, entry), {name: 'AbortError'});
  assert.equal(starts.length, 1);
  binding.dispose();
});

test('a late episode permission read cannot play after its show page is replaced', async () => {
  const pending = deferred<unknown>(); let detailSignal: AbortSignal | undefined, starts = 0;
  const owner = setup(async (path, _method, _body, signal) => {
    if (path.includes('/show-workspace')) return showPage();
    detailSignal = signal; return pending.promise;
  });
  const binding = createShowBindings({...owner.context, effects: {...owner.context.effects, play: async () => { starts++; }}});
  await binding.service.select({libraryId: 'library', showId: 'show'});
  const data = binding.service.getSnapshot().data!, entry = data.episodes.sections[0].entries[0];
  const refused = assert.rejects(binding.playEpisode(data, entry), {name: 'AbortError'});
  binding.cancel();
  assert.equal(detailSignal?.aborted, true);
  pending.resolve({...detailPage('one'), actions: [{id: 'play', labelKey: 'title.play', enabled: true, playback: {itemId: 'one', startMs: 0}}]});
  await refused;
  assert.equal(starts, 0);
  binding.dispose();
});

test('the canonical episode preference changes the real Show layout and remains its next-title default', async () => {
  const owner = setup(async () => showPage()), listeners = new Set<() => void>();
  let value: 'auto' | 'tiles' | 'list' = 'list';
  const snapshot = (): PreferenceSnapshot => ({registry: {revision: 'registry', fields: [{key: 'appearance.episodesLayout', type: 'string', default: 'auto', allowedValues: ['auto', 'tiles', 'list'], scopes: ['profile-device'], group: 'appearance', labelKey: 'preferences.appearance.episodesLayout'}]}, effective: {'appearance.episodesLayout': value}} as PreferenceSnapshot);
  const binding = createShowBindings({...owner.context, preferences: {getSnapshot: snapshot, subscribe: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }}});
  assert.equal(owner.requests(), 0);
  assert.equal(binding.view.getSnapshot().layout, 'list');
  binding.view.layout('grid');
  for (const listener of listeners) listener();
  assert.equal(binding.view.getSnapshot().layout, 'grid');
  binding.view.reset();
  assert.equal(binding.view.getSnapshot().layout, 'list');
  value = 'tiles'; for (const listener of listeners) listener();
  assert.equal(binding.view.getSnapshot().layout, 'grid');
  value = 'list'; owner.retire(); for (const listener of listeners) listener();
  assert.equal(binding.view.getSnapshot().layout, 'grid');
  binding.dispose();
  assert.equal(listeners.size, 0);
});

test('switching people aborts the old read and ignores a transport that still returns it', async () => {
  const old = deferred<unknown>(); let oldSignal: AbortSignal | undefined;
  const owner = setup(async (path, _method, _body, signal) => {
    if (path.includes('/first?')) { oldSignal = signal; return old.promise; }
    return personPage('second');
  });
  const binding = createPersonBindings(owner.context), first = binding.select('first');
  await binding.select('second');
  assert.equal(oldSignal?.aborted, true);
  old.resolve(personPage('first')); await first;
  assert.equal(binding.getSnapshot().page?.person.id, 'second');
  binding.dispose();
  assert.equal(binding.getSnapshot().page, undefined);
});

test('person page identity must still be current before starting a credit collection', async () => {
  const owner = setup(async path => personPage(path.includes('/first?') ? 'first' : 'second'));
  const binding = createPersonBindings(owner.context);
  await binding.select('first'); const first = binding.getSnapshot().page!;
  await binding.select('second');
  assert.throws(() => binding.credits(first), /Person required/);
  assert.equal(owner.requests(), 2);
  binding.dispose();
});

test('aborting the previous caller cannot cancel a newly selected title in the same turn', async () => {
  const first = deferred<unknown>(), second = deferred<unknown>(); let secondSignal: AbortSignal | undefined;
  const owner = setup(async (path, _method, _body, signal) => {
    if (path.includes('/first/')) return first.promise;
    secondSignal = signal; return second.promise;
  });
  const binding = createDetailBindings(owner.context), oldCaller = new AbortController();
  const before = assert.rejects(binding.select({libraryId: 'library', itemId: 'first'}, oldCaller.signal), {name: 'AbortError'});
  const after = binding.select({libraryId: 'library', itemId: 'second'});
  oldCaller.abort();
  assert.equal(secondSignal?.aborted, false);
  second.resolve(detailPage('second')); await after;
  first.resolve(detailPage('first')); await before;
  assert.equal(binding.service.getSnapshot().data?.item.id, 'second');
  binding.dispose();
});

test('a File info command carries its opening and exact occurrence, and a changed Detail stops it before any request', async () => {
  const writes: {path: string; body: unknown}[] = [];
  const owner = setup(async (path, method, body) => {
    if (method === 'POST') { writes.push({path, body}); return {assetId: 'asset', previewKey: 'key', confirmation: 'one', confirmationKind: 'title', totalBytes: 1, totalBytesText: '1 B', targets: [{itemId: 'one'}], blocked: []}; }
    return {...detailPage('one'), fileInfoOpening: 'opening-1'};
  });
  const binding = createDetailBindings(owner.context); await binding.select({libraryId: 'library', itemId: 'one'});
  const data = binding.service.getSnapshot().data!;
  assert.equal(data.fileInfoOpening, 'opening-1');
  const file = {occurrence: 'opaque-1', itemId: 'one', id: 'asset', size: {value: 1}, container: 'mkv', available: true, partIndex: 0, audioCount: 0, subtitleCount: 0};
  await assert.rejects(binding.previewDelete(data, 'opening-0', file), /File info opening changed/);
  assert.deepEqual(writes, []);
  await binding.previewDelete(data, 'opening-1', file).catch(() => {});
  assert.deepEqual(writes, [{path: '/v1/admin/items/one/files/asset/delete/preview', body: {fileInfo: {opening: 'opening-1', occurrence: 'opaque-1'}}}]);
  binding.dispose();
});
test('manual versions use fresh Options resume and never carry the choice into ordinary Play', async () => {
  const starts: unknown[] = [];
  const page = {...detailPage('one'), actions: [{id: 'play', labelKey: 'title.play', enabled: true, playback: {itemId: 'one', startMs: 100}}]};
  const owner = setup(async path => path.includes('/playback-options') ? {itemId: 'one', versions: [{id: 'grouped-version', durationMs: 1000}], resume: {positionMs: 725, partIndex: 0}} : page);
  const context = {...owner.context, effects: {...owner.context.effects, play: async (target: unknown) => { starts.push(target); }}};
  const binding = createDetailBindings(context); await binding.select({libraryId: 'library', itemId: 'one'});
  const data = binding.service.getSnapshot().data!;
  await binding.play(data, 'play', 'grouped-version');
  await binding.play(data, 'play');
  assert.deepEqual(starts, [
    {itemId: 'one', libraryId: 'library', startSeconds: 0.725, versionId: 'grouped-version'},
    {itemId: 'one', libraryId: 'library', startSeconds: 0.1},
  ]);
  binding.dispose();
});

test('Title More passes original genuine Options to the single chooser and starts only a fresh offered exact version', async () => {
  let captured: WatchVersionRequest | undefined, optionsReads = 0;
  const closed = deferred<void>(), starts: unknown[] = [];
  const page = {...detailPage('one'), actions: [{id: 'play', labelKey: 'title.play', enabled: true, playback: {itemId: 'one', startMs: 100}}]};
  const owner = setup(async path => path.includes('/playback-options') ? {itemId: 'one', versions: [{id: 'first', durationMs: 1000}, {id: 'second', durationMs: 1000}], resume: {positionMs: ++optionsReads === 1 ? 0 : 700, partIndex: 0}} : page);
  const binding = createDetailBindings({...owner.context, effects: {...owner.context.effects,
    play: async target => {starts.push(target);}, chooseVersion: async request => {captured = request;await closed.promise;}}});
  try {
    await binding.select({itemId: 'one', libraryId: 'library'});
    const data = binding.service.getSnapshot().data!, original = await binding.readVersions(data), signal = new AbortController();
    await assert.rejects(binding.chooseVersion(data, {...original}, signal.signal), {name: 'AbortError'});
    const showing = binding.chooseVersion(data, original, signal.signal);
    assert.ok(captured);assert.equal(captured.options, original);assert.equal(captured.options.versions, original.versions);
    assert.deepEqual(captured.subject, {libraryId: 'library', itemId: 'one', title: 'one'});
    await captured.playVersion('second', signal.signal);
    assert.deepEqual(starts, [{itemId: 'one', libraryId: 'library', startSeconds: .7, versionId: 'second'}]);
    assert.equal(optionsReads, 2);
    await binding.select({libraryId: 'library', itemId: 'one'});
    const replacement = binding.service.getSnapshot().data!;
    assert.notEqual(replacement, data);
    await assert.rejects(binding.chooseVersion(replacement, original, signal.signal), {name: 'AbortError'});
    assert.equal(captured.isCurrent(), false);
    owner.retire();assert.equal(captured.isCurrent(), false);
    const before = owner.requests();await assert.rejects(captured.playVersion('first', signal.signal), {name: 'AbortError'});assert.equal(owner.requests(), before);
    closed.resolve();await showing;
  } finally {closed.resolve();binding.dispose();}
});

test('a one-version title cannot open a chooser even when a presentation port is installed', async () => {
  let opened = 0;
  const owner = setup(async path => path.includes('/playback-options') ? {itemId: 'one', versions: [{id: 'only', durationMs: 1000}]}
    : {...detailPage('one'), actions: [{id: 'play', labelKey: 'title.play', enabled: true, playback: {itemId: 'one', startMs: 0}}]});
  const binding = createDetailBindings({...owner.context, effects: {...owner.context.effects, chooseVersion: async () => {opened++;}}});
  try {
    await binding.select({itemId: 'one', libraryId: 'library'});const data = binding.service.getSnapshot().data!;
    await assert.rejects(binding.chooseVersion(data, await binding.readVersions(data), new AbortController().signal), {name: 'AbortError'});
    assert.equal(opened, 0);
  } finally {binding.dispose();}
});

test('the entry version command preserves nested authority and refuses a removed grouped version', async () => {
  let current = true, starts = 0;
  const captured: SelectionContext = {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', viewId: 'search', privacyFence: 'fence'}, revision: {catalogRevision: '1', viewerRevision: '1'}};
  const entry: ContentEntry = {id: 'one', libraryId: 'library', kind: 'movie', title: 'one', navigation: {view: 'item', entityId: 'one'}};
  const owner = setup(async path => path.includes('/playback-options') ? {itemId: 'one', versions: [{id: 'current-version', durationMs: 1000}]} : {...detailPage('one'), actions: [{id: 'play', labelKey: 'title.play', enabled: true, playback: {itemId: 'one', startMs: 0}}]});
  const context = {...owner.context, effects: {...owner.context.effects, play: async () => { starts++; }}};
  const binding = createEntryDetailsBindings(context, {isCurrent: value => current && value === captured});
  await assert.rejects(binding.playVersion(entry, 'removed-version', captured, new AbortController().signal), /Version playback unavailable/);
  current = false; const requests = owner.requests();
  await assert.rejects(binding.playVersion(entry, 'current-version', captured, new AbortController().signal), {name: 'AbortError'});
  assert.equal(owner.requests(), requests); assert.equal(starts, 0);
  binding.dispose();
});

test('person credits expose only cursor-addressable pages and retain a bounded window', async () => {
  let pages = 0, active = 0, concurrent = 0;
  const owner = setup(async path => {
    const query = new URL('https://unit.invalid' + path).searchParams;
    if (query.get('limit') === '1') return {...personPage('one'), pageInfo: {total: 1000000, nextCursor: ''}};
    const page = Number(query.get('cursor') ?? 0); pages++; active++; concurrent = Math.max(concurrent, active);
    await setImmediate(); active--;
    return {...personPage('one'), credits: Array.from({length: 40}, (_, index) => ({creditKind: 'cast', roles: [], media: {id: `title-${page * 40 + index}`, libraryId: 'library', kind: 'movie', title: `title-${page * 40 + index}`}})), pageInfo: {total: 1000000, nextCursor: String(page + 1)}};
  });
  const binding = createPersonBindings(owner.context); await binding.select('one');
  const collection = binding.credits(binding.getSnapshot().page!); collection.ensureRange(0, 10);
  for (let turn = 0; turn < 8; turn++) await setImmediate();
  assert.equal(collection.getSnapshot().totalIsLowerBound, true);
  assert.ok(collection.getSnapshot().total! <= 120); assert.ok(pages <= 2);
  const last = collection.getSnapshot().total! - 1; collection.ensureRange(last - 10, last);
  for (let turn = 0; turn < 10; turn++) await setImmediate();
  assert.equal(concurrent, 1); assert.ok(pages <= 6); assert.ok(collection.residentItems().length <= 200);
  binding.cancel(); assert.equal(binding.getSnapshot().page, undefined);
  binding.dispose();
});

test('a retained title body keeps its tab for the same real identity and resets for another title', async () => {
  const owner = setup(async path => detailPage(path.includes('/second/') ? 'second' : 'first'));
  const binding = createDetailBindings(owner.context);
  await binding.select({libraryId: 'library', itemId: 'first'}); binding.view.tab('details');
  binding.service.cancel(); await binding.select({libraryId: 'library', itemId: 'first'});
  assert.equal(binding.view.getSnapshot().tab, 'details');
  await binding.select({libraryId: 'library', itemId: 'second'});
  assert.equal(binding.view.getSnapshot().tab, 'related');
  owner.retire(); assert.throws(() => binding.view.tab('details'), {name: 'AbortError'});
  binding.dispose();
});

test('entry playback keeps the source context and item detail in their own revision domains', async () => {
  const starts: unknown[] = [];
  const captured: SelectionContext = {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', viewId: 'search', privacyFence: 'search-fence'}, revision: {catalogRevision: '8573', viewerRevision: '0'}};
  const entry: ContentEntry = {id: 'one', libraryId: 'library', kind: 'movie', title: 'one', navigation: {view: 'item', entityId: 'one'}};
  const owner = setup(async path => path.includes('/playback-options') ? {itemId: 'one', versions: [{id: 'grouped-version', durationMs: 1000}], resume: {positionMs: 500, partIndex: 0}} : {...detailPage('one'), scope: {...detailPage('one').scope, viewerFence: 'detail-fence'}, revision: {catalog: 2189, viewer: 0}, actions: [{id: 'play', labelKey: 'title.play', enabled: true, playback: {itemId: 'one', startMs: 0}}]});
  const binding = createEntryDetailsBindings({...owner.context, effects: {...owner.context.effects, play: async target => { starts.push(target); }}}, {isCurrent: value => value === captured});
  await binding.playVersion(entry, 'grouped-version', captured, new AbortController().signal);
  assert.deepEqual(starts, [{libraryId: 'library', itemId: 'one', startSeconds: 0.5, versionId: 'grouped-version'}]);
  binding.dispose();
});

test('a removed title retains only its genuine same-target subject and clears it at disposal', async () => {
  let removed = false;
  const owner = setup(async () => { if (removed) throw Object.assign(new Error('gone'), {code: 'not_found', status: 404}); return detailPage('one'); });
  const binding = createDetailBindings(owner.context); await binding.select({libraryId: 'library', itemId: 'one'});
  removed = true; await binding.service.refresh();
  assert.equal(binding.service.getSnapshot().data, null); assert.equal(binding.subject(), 'one');
  await binding.select({libraryId: 'library', itemId: 'another'}); assert.equal(binding.subject(), undefined);
  binding.dispose(); assert.equal(binding.subject(), undefined);
});

const genreCapabilities = () => ({
  library: {id: 'library', name: 'Movies', kind: 'movie', defaultView: 'discover'},
  pivots: [{id: 'movies', labelKey: 'library.movies', entityKinds: ['movie'], defaultSort: [], supportedViews: ['grid', 'list'], browsable: true}],
  resolvedPivot: null,
  fields: [{id: 'genre', labelKey: 'browse.field.genre', type: 'identity-set', operators: ['equals', 'contains'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed-join', applicableKinds: ['movie']}],
  sorts: [], quickFilters: [],
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 1800000},
});
const genrePage = () => ({...detailPage('one'), metadata: {status: 'unavailable', ratings: [], genres: [{id: 'genre', name: 'Action'}], credits: []}});

test('genre navigation validates the library vocabulary and leaves its default view to the parent', async () => {
  const destinations: unknown[] = [];
  const owner = setup(async path => path === browseCapabilitiesPath('library', '') ? genreCapabilities() : genrePage());
  const binding = createDetailBindings({...owner.context, effects: {...owner.context.effects, openBrowse: selector => { destinations.push(selector); }}});
  await binding.select({libraryId: 'library', itemId: 'one'});
  const page = binding.service.getSnapshot().data!;
  await binding.browseGenre(page, 'Action');
  assert.deepEqual(destinations, [{libraryId: 'library', query: {variant:'predicate',field: 'genre', operator: 'equals', value: 'Action'}}]);
  const requests = owner.requests();
  assert.throws(() => binding.browseGenre(page, 'Unknown'), {name: 'AbortError'});
  assert.equal(owner.requests(), requests);
  binding.dispose();
});

test('a late genre vocabulary cannot navigate after an authority handover', async () => {
  const response = deferred<unknown>(); let destinations = 0;
  const owner = setup(async path => path === browseCapabilitiesPath('library', '') ? response.promise : genrePage());
  const binding = createDetailBindings({...owner.context, effects: {...owner.context.effects, openBrowse: () => { destinations++; }}});
  await binding.select({libraryId: 'library', itemId: 'one'});
  const navigation = binding.browseGenre(binding.service.getSnapshot().data!, 'Action');
  owner.retire(); response.resolve(genreCapabilities());
  await assert.rejects(navigation, {name: 'AbortError'});
  assert.equal(destinations, 0);
  binding.dispose();
});

test('title credit windows read lazily, bound resident pages and revisit evicted cursor addresses', async () => {
  const reads: number[] = [];
  const owner = setup(async path => {
    if (!path.includes('/credits?')) return detailPage('one');
    const query = new URL('https://unit.invalid' + path).searchParams, page = Number(query.get('cursor') ?? 0);
    reads.push(page);
    return {revision: {catalog: 1, viewer: 1}, group: 'cast', total: 1000000, nextCursor: String(page + 1),
      credits: Array.from({length: 40}, (_, index) => ({id: `credit-${page * 40 + index}`, name: `Person ${page * 40 + index}`, role: 'Character', department: 'Acting', provider: 'local'}))};
  });
  const binding = createDetailBindings(owner.context); await binding.select({libraryId: 'library', itemId: 'one'});
  const collection = titleCreditsWindow(binding, binding.service.getSnapshot().data!, 'cast');
  assert.equal(reads.length, 0);
  const settle = async () => { for (let turn = 0; turn < 10; turn++) await setImmediate(); };
  collection.ensureRange(0, 10); await settle();
  assert.ok(reads.length <= 2); assert.equal(collection.getSnapshot().totalIsLowerBound, true);
  for (let step = 0; step < 6; step++) {
    const last = collection.getSnapshot().total! - 1;
    collection.ensureRange(last - 10, last); await settle();
  }
  assert.ok(collection.resident().pages <= 5); assert.ok(collection.residentItems().length <= 200);
  assert.equal(collection.itemAt(0), undefined);
  collection.ensureRange(0, 10); await settle();
  assert.equal(collection.itemAt(0)?.id, 'credit-0');
  assert.ok(reads.filter(page => page === 0).length >= 2);
  assert.notEqual(collection.getSnapshot().status, 'error');
  collection.dispose(); binding.dispose(); assert.equal(collection.residentItems().length, 0);
});

test('season actions use complete server state and CAS, never the visible episode subset',async()=>{
 const writes:unknown[]=[];
 const state={revision:17,watched:false,watermark:'',watchlisted:false,favorite:false,watchedCount:6,unwatchedCount:18,libraryId:'library'};
 const owner=setup(async(path,method,body)=>{
  if(path.includes('/show-workspace'))return showPage();
  if(path==='/v1/containers/season/season/personal-state'){
   if(method==='PUT'){writes.push(body);return {...state,revision:18,watched:true};}return state;
  }
  if(path==='/v1/containers/show/show/personal-state')return {...state,revision:1};
  throw new Error('Unexpected route '+path);
 });
 const binding=createShowBindings(owner.context);await binding.select({libraryId:'library',showId:'show'});
 const data=binding.service.getSnapshot().data!;
 assert.equal(data.episodes.sections[0].entries.length,1);
 const complete=await binding.readSeasonState(data);assert.equal(complete.watchedCount,6);assert.equal(complete.unwatchedCount,18);
 await assert.rejects(binding.setSeasonWatched(data,{...complete},true),{name:'AbortError'});assert.equal(writes.length,0);
 await binding.setSeasonWatched(data,complete,true);assert.deepEqual(writes,[{expectedRevision:17,watched:true}]);
 await assert.rejects(binding.setSeasonWatched(data,complete,false),{name:'AbortError'});assert.equal(writes.length,1);
 binding.dispose();
});

test('season play captures actual selected season and retires after the selected workspace changes',async()=>{
 const held=deferred<void>();let calls=0,guard:(()=>boolean)|undefined;
 const owner=setup(async path=>path.includes('/show-workspace')?showPage():{revision:1,watched:false,watermark:'',watchlisted:false,favorite:false,libraryId:'library'});
 const context={...owner.context,effects:{...owner.context.effects,playSeason:async(id:string,signal:AbortSignal,current:()=>boolean)=>{calls++;assert.equal(id,'season');assert.equal(signal.aborted,false);guard=current;await held.promise;}}};
 const binding=createShowBindings(context);await binding.select({libraryId:'library',showId:'show'});
 const data=binding.service.getSnapshot().data!,pending=binding.playSeason(data);assert.equal(guard?.(),true);
 await binding.service.refresh();assert.equal(guard?.(),false);held.resolve();await assert.rejects(pending,{name:'AbortError'});
 await assert.rejects(binding.playSeason(data),{name:'AbortError'});assert.equal(calls,1);binding.dispose();
});
