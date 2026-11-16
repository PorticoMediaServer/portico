import test from 'node:test';
import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {SearchGroup} from '@core/search.ts';
import {createI18n, resolveRegion} from '@i18n';
import {setServiceI18n} from '@core/presentation/service-text.ts';
import {createSearchBindings as bindSearch} from '../src/screens/search/bindings.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';
const createSearchBindings = (context: Parameters<typeof bindSearch>[0]) => bindSearch(context, context.queries);

/** The shell confirms the viewer's catalogue and device zone before any screen reads. */
setServiceI18n(createI18n(resolveRegion(undefined, {locales: ['en-CA'], timeZone: 'America/Halifax'})));

const iso = (ms: number) => new Date(ms).toISOString().replace('.000Z', 'Z');
/** A channel as the rebuilt server's search and guide name it. */
const liveChannel = (id: string, name: string) => ({id, name, number: '802', kind: 'source', group: 'Drama', favorite: false, hidden: false});
/** One rebuilt-server search answer: an empty Movies group and, when given, the Live TV group with these channels. */
function searchPage(channels?: readonly ReturnType<typeof liveChannel>[]) {
  return {groups: [{kind: 'movie', total: 0, items: []}, ...(channels ? [{kind: 'channel', total: channels.length, channels}] : [])]};
}
/** The guide around a channel for this minute (`GET /v1/live/guide?around=`). */
const guidePage = (items: readonly Readonly<{channel: unknown; programmes: readonly unknown[]}>[]) => ({outOfDate: false, items});
const programme = (id: string, title: string, startMs: number, endMs: number) => ({id, title, startsAt: iso(startMs), endsAt: iso(endMs)});
function setup(handler: (path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<unknown>) {
  let current = true;
  const paths: string[] = [];
  const opened: unknown[] = [];
  const context = withQueries({
    api: {
      request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => { paths.push(path); return handler(path, method, body, signal) as Promise<T>; },
      requestRaw: async () => { throw new Error('No raw transport.'); },
    },
    scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])}, isCurrent: () => current,
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry: (entry: unknown) => opened.push(entry), openSettings: () => {}, play: async () => {}, reportError: () => {}},
  }) as unknown as ScreenContext;
  return {context, paths, opened, retire: () => { current = false; }};
}
async function until(check: () => boolean) {
  for (let attempt = 0; attempt < 100; attempt++) { if (check()) return; await setImmediate(); }
  throw new Error('the expected state never arrived');
}
const guidePaths = (paths: readonly string[]) => paths.filter(path => path.startsWith('/v1/live/guide?'));
const groups: readonly SearchGroup[] = ['movies', 'live-tv'];

test('a live result takes its channel row and on-air programme from the guide around it', async () => {
  const now = Date.now(), owner = setup(async path => {
    if (path.startsWith('/v1/search?')) return searchPage([liveChannel('ember', 'Ember Drama'), liveChannel('northwind', 'Northwind News')]);
    if (path.startsWith('/v1/live/guide?')) return guidePage([
      {channel: liveChannel('ember', 'Ember Drama'), programmes: [programme('shift', 'The Night Shift', now - 45 * 60_000, now + 15 * 60_000),
        programme('later', 'Fault Lines', now + 15 * 60_000, now + 45 * 60_000)]},
      {channel: liveChannel('northwind', 'Northwind News'), programmes: [programme('market', 'Market Watch', now - 5 * 60_000, now + 25 * 60_000)]},
    ]);
    throw new Error('unexpected ' + path);
  });
  const binding = createSearchBindings(owner.context);
  try {
    await binding.select('ma', {groups});
    await until(() => binding.live.getSnapshot().size === 2);
    const cards = binding.live.getSnapshot();
    assert.equal(cards.get('ember')!.channel.number, '802');
    assert.equal(cards.get('ember')!.programme!.title, 'The Night Shift', 'only the programme actually on air decorates the card');
    assert.equal(cards.get('northwind')!.programme!.title, 'Market Watch');
    assert(cards.get('ember')!.programme!.progress! > 0 && cards.get('ember')!.programme!.progress! < 1);
    assert(cards.get('ember')!.programme!.remaining, 'the drawn remaining line is composed from the real end');
    const reads = guidePaths(owner.paths);
    assert.equal(reads.length, 1, 'one read around the first channel answered both');
    assert.equal(new URL(reads[0]!, 'https://local.invalid').searchParams.get('around'), 'ember');
  } finally { binding.dispose(); }
});

test('a refused guide read leaves the plain result rows and never invents a programme', async () => {
  const owner = setup(async path => {
    if (path.startsWith('/v1/search?')) return searchPage([liveChannel('ember', 'Ember Drama')]);
    if (path.startsWith('/v1/live/guide?')) throw Object.assign(new Error('forbidden'), {status: 403});
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
      return q === 'ma' ? searchPage([liveChannel('old', 'Old Channel')]) : searchPage([liveChannel('new', 'New Channel')]);
    }
    if (path.startsWith('/v1/live/guide?')) {
      reads++;
      if (reads === 1) return held;
      return guidePage([{channel: liveChannel('new', 'New Channel'), programmes: []}]);
    }
    throw new Error('unexpected ' + path);
  });
  const binding = createSearchBindings(owner.context);
  try {
    await binding.select('ma', {groups});
    await until(() => reads === 1);
    await binding.select('other', {groups});
    await until(() => binding.live.getSnapshot().has('new'));
    release(guidePage([{channel: liveChannel('old', 'Old Channel'), programmes: []}]));
    await setImmediate(); await setImmediate();
    assert.equal(binding.live.getSnapshot().has('old'), false, 'a retired query never decorates the screen');
    assert.equal(binding.live.getSnapshot().size, 1);
  } finally { binding.dispose(); }
});

test('no live group means no guide read', async () => {
  const owner = setup(async path => {
    if (path.startsWith('/v1/search?')) return searchPage();
    if (path.startsWith('/v1/live/guide?')) return guidePage([{channel: liveChannel('ember', 'Ember Drama'), programmes: []}]);
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
      return q === 'ma' ? searchPage([liveChannel('ember', 'Ember Drama')]) : searchPage();
    }
    if (path.startsWith('/v1/live/guide?')) return guidePage([{channel: liveChannel('ember', 'Ember Drama'),
      programmes: [programme('shift', 'The Night Shift', Date.now() - 60_000, Date.now() + 60_000)]}]);
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
