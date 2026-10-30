import test from 'node:test';
import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {SearchGroup} from '@core/search.ts';
import {createI18n, resolveRegion} from '@i18n';
import {setServiceI18n} from '@core/presentation/service-text.ts';
import {createSearchBindings} from '../src/screens/search/bindings.ts';

/** The shell confirms the viewer's catalogue and device zone before any screen reads. */
setServiceI18n(createI18n(resolveRegion(undefined, {locales: ['en-CA'], timeZone: 'America/Halifax'})));

const iso = (ms: number) => new Date(ms).toISOString().replace('.000Z', 'Z');
const channelEntry = (id: string, title: string, sourceId = 'source') => ({id, kind: 'channel', title, subtitle: '101 · Drama',
  navigation: {view: 'channel', entityId: id, sourceId, channelId: id, provenance: 'live-source'}});
const movieEntry = (id: string) => ({id, libraryId: 'library', kind: 'movie', title: id, navigation: {view: 'item', entityId: id}});
/** One search answer whose only groups are the requested ones. */
function searchPage(path: string, items: readonly unknown[] = [movieEntry('mayday')]) {
  const params = new URL(path, 'https://local.invalid').searchParams;
  const groups = (params.get('groups') ?? '').split(',').filter(Boolean);
  return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'fence'},
    revision: {catalog: 4, viewer: 2}, heading: {key: 'search.title', fallback: ''},
    query: {q: params.get('q'), sort: 'relevance', direction: 'desc', group: '', groups, libraryIds: [], limit: Number(params.get('limit')),
      searchMode: 'normalized_prefix', recorded: false},
    groups: groups.map(id => ({id, entityKind: id === 'live-tv' ? 'channel' : 'movie', title: id, status: 'success', errorCode: '',
      items: id === 'live-tv' ? items : [], totalCount: id === 'live-tv' ? items.length : 0, totalCountExact: true, hasMore: false, nextCursor: ''})),
    capabilities: {groups: groups.map(id => ({id, entityKind: id === 'live-tv' ? 'channel' : 'movie', title: id, available: true, sorts: ['relevance']})),
      sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 600}};
}
const guideChannel = (id: string, title: string, sourceId = 'source', programmes: readonly unknown[] = []) => ({id, sourceId, provenance: 'live-source',
  name: title, number: '802', group: 'Drama', generation: 'g1', preferenceRevision: 0, programmes, tuneAvailable: true, recordAvailable: true,
  favorite: false, hidden: false, mark: {text: 'ED', seed: 'source:' + id}});
/** The guide envelope echoes the window it was asked for, as the route demands. */
function guidePage(path: string, channels: readonly unknown[]) {
  const params = new URL(path, 'https://local.invalid').searchParams;
  const start = params.get('start')!, end = params.get('end')!;
  return {protocolVersion: '1.0', serverId: 'server', guide: {state: 'ready', viewerFence: 'fence', start, end, timezone: params.get('timezone'),
    observedAt: start, nextCursor: '', sources: [{id: 'source', name: 'Live', generation: 'g1', publishedAt: start,
      availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'ready'}], channels}};
}
const programme = (id: string, channelId: string, title: string, startMs: number, endMs: number) =>
  ({id: `${id}-${channelId}`, channelId, title, start: iso(startMs), end: iso(endMs)});
function setup(handler: (path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<unknown>) {
  let current = true;
  const paths: string[] = [];
  const opened: unknown[] = [];
  const context = {
    api: {
      request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => { paths.push(path); return handler(path, method, body, signal) as Promise<T>; },
      requestRaw: async () => { throw new Error('No raw transport.'); },
    },
    scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])}, isCurrent: () => current,
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry: (entry: unknown) => opened.push(entry), openSettings: () => {}, play: async () => {}, reportError: () => {}},
  } as unknown as ScreenContext;
  return {context, paths, opened, retire: () => { current = false; }};
}
async function until(check: () => boolean) {
  for (let attempt = 0; attempt < 100; attempt++) { if (check()) return; await setImmediate(); }
  throw new Error('the expected state never arrived');
}
const guidePaths = (paths: readonly string[]) => paths.filter(path => path.startsWith('/v1/guide?'));
const groups: readonly SearchGroup[] = ['movies', 'live-tv'];

test('a live result takes its channel row and on-air programme from one bounded guide read of its own matched ids', async () => {
  const now = Date.now(), owner = setup(async path => {
    if (path.startsWith('/v1/search?')) return searchPage(path, [channelEntry('ember', 'Ember Drama'), channelEntry('northwind', 'Northwind News')]);
    if (path.startsWith('/v1/guide?')) return guidePage(path, [
      guideChannel('ember', 'Ember Drama', 'source', [programme('shift', 'ember', 'The Night Shift', now - 45 * 60_000, now + 15 * 60_000),
        programme('later', 'ember', 'Fault Lines', now + 15 * 60_000, now + 45 * 60_000)]),
      guideChannel('northwind', 'Northwind News', 'source', [programme('market', 'northwind', 'Market Watch', now - 5 * 60_000, now + 25 * 60_000)]),
    ]);
    throw new Error('unexpected ' + path);
  });
  const binding = createSearchBindings(owner.context);
  try {
    await binding.select('ma', {groups});
    await until(() => binding.live.getSnapshot().size === 2);
    const cards = binding.live.getSnapshot();
    assert.equal(cards.get('ember')!.channel.number, '802');
    assert.deepEqual(cards.get('ember')!.channel.mark, {sourceId: 'source', id: 'ember', text: 'ED', seed: 'source:ember'});
    assert.equal(cards.get('ember')!.programme!.title, 'The Night Shift', 'only the programme actually on air decorates the card');
    assert.equal(cards.get('northwind')!.programme!.title, 'Market Watch');
    assert(cards.get('ember')!.programme!.progress! > 0 && cards.get('ember')!.programme!.progress! < 1);
    assert(cards.get('ember')!.programme!.remaining, 'the drawn remaining line is composed from the real end');
    const reads = guidePaths(owner.paths);
    assert.equal(reads.length, 1, 'one source is one bounded read');
    const guide = new URL(reads[0]!, 'https://local.invalid');
    assert.equal(guide.searchParams.get('kind'), 'live-source');
    assert.equal(guide.searchParams.get('sourceId'), 'source');
    assert.deepEqual(guide.searchParams.get('channels')!.split(','), ['ember', 'northwind'], 'the read names exactly the matched channels');
    assert.equal(guide.searchParams.get('limit'), '50');
    assert.equal(guide.searchParams.get('timezone'), 'America/Halifax');
  } finally { binding.dispose(); }
});

test('a refused guide read leaves the plain result rows and never invents a programme', async () => {
  const owner = setup(async path => {
    if (path.startsWith('/v1/search?')) return searchPage(path, [channelEntry('ember', 'Ember Drama')]);
    if (path.startsWith('/v1/guide?')) throw Object.assign(new Error('forbidden'), {status: 403});
    throw new Error('unexpected ' + path);
  });
  const binding = createSearchBindings(owner.context);
  try {
    await binding.select('ma', {groups});
    await until(() => guidePaths(owner.paths).length === 1);
    await setImmediate();
    assert.equal(binding.live.getSnapshot().size, 0);
    assert.equal(binding.service.getSnapshot().phase, 'ready', 'the search answer itself is unaffected');
    binding.open(binding.service.getSnapshot().groups.find(group => group.id === 'live-tv')!.items[0]!);
    assert.equal(owner.opened.length, 1, 'the channel row still opens its own destination');
  } finally { binding.dispose(); }
});

test('a later search retires the earlier guide read so its answer is never shown', async () => {
  let release!: (value: unknown) => void;
  const held = new Promise<unknown>(resolve => { release = resolve; });
  let reads = 0;
  const owner = setup(async path => {
    if (path.startsWith('/v1/search?')) {
      const q = new URL(path, 'https://local.invalid').searchParams.get('q');
      return q === 'ma' ? searchPage(path, [channelEntry('old', 'Old Channel')]) : searchPage(path, [channelEntry('new', 'New Channel')]);
    }
    if (path.startsWith('/v1/guide?')) {
      reads++;
      if (reads === 1) return held;
      return guidePage(path, [guideChannel('new', 'New Channel', 'source', [])]);
    }
    throw new Error('unexpected ' + path);
  });
  const binding = createSearchBindings(owner.context);
  try {
    await binding.select('ma', {groups});
    await until(() => reads === 1);
    await binding.select('other', {groups});
    await until(() => binding.live.getSnapshot().has('new'));
    release(guidePage('/v1/guide?start=x&end=y&timezone=UTC', [guideChannel('old', 'Old Channel')]));
    await setImmediate(); await setImmediate();
    assert.equal(binding.live.getSnapshot().has('old'), false, 'a retired query never decorates the screen');
    assert.equal(binding.live.getSnapshot().size, 1);
  } finally { binding.dispose(); }
});

test('no live group means no guide read', async () => {
  const owner = setup(async path => {
    if (path.startsWith('/v1/search?')) return searchPage(path, []);
    if (path.startsWith('/v1/guide?')) return guidePage(path, [guideChannel('ember', 'Ember Drama')]);
    throw new Error('unexpected ' + path);
  });
  const binding = createSearchBindings(owner.context);
  try {
    await binding.select('ma', {groups: ['movies']});
    await setImmediate();
    assert.deepEqual(guidePaths(owner.paths), []);
    assert.equal(binding.live.getSnapshot().size, 0);
  } finally { binding.dispose(); }
});

test('moving off Live TV clears the cards it no longer owns', async () => {
  const owner = setup(async path => {
    if (path.startsWith('/v1/search?')) {
      const q = new URL(path, 'https://local.invalid').searchParams.get('q');
      return q === 'ma' ? searchPage(path, [channelEntry('ember', 'Ember Drama')]) : searchPage(path, [movieEntry('mayday')]);
    }
    if (path.startsWith('/v1/guide?')) return guidePage(path, [guideChannel('ember', 'Ember Drama', 'source',
      [programme('shift', 'ember', 'The Night Shift', Date.now() - 60_000, Date.now() + 60_000)])]);
    throw new Error('unexpected ' + path);
  });
  const binding = createSearchBindings(owner.context);
  try {
    await binding.select('ma', {groups});
    await until(() => binding.live.getSnapshot().has('ember'));
    await binding.select('mayday', {groups: ['movies']});
    await until(() => binding.live.getSnapshot().size === 0);
  } finally { binding.dispose(); }
});
