import test from 'node:test';
import assert from 'node:assert/strict';
import {QueryObserver, type QueryObserverOptions} from '@tanstack/query-core';
import {FreshnessHub, dependsOn} from '../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../src/query/viewer-queries.ts';
import {queryKeys} from '../src/query/keys.ts';

/** A clock the test moves by hand; timers fire in order as it advances (the hub's own test clock). */
function clock() {
  let now = 0, next = 1;
  const timers = new Map<number, {at: number; fn: () => void}>();
  return {
    env: {setTimer: (fn: () => void, ms: number) => { const id = next++; timers.set(id, {at: now + Math.max(0, ms), fn}); return id; }, clearTimer: (id: unknown) => { timers.delete(id as number); }, now: () => now, random: () => 0, contentEveryMs: 0, settleMs: 0},
    async advance(ms: number) {
      const end = now + ms;
      for (let spins = 0; ; spins++) {
        if (spins > 10_000) throw new Error('a timer keeps rescheduling itself at ' + now);
        let due: [number, {at: number; fn: () => void}] | undefined;
        for (const entry of timers) if (entry[1].at <= end && (!due || entry[1].at < due[1].at)) due = entry;
        if (!due) break;
        timers.delete(due[0]); now = due[1].at; due[1].fn();
        await flush();
      }
      now = end;
      await flush();
    },
  };
}
const flush = async () => { for (let i = 0; i < 20; i++) await new Promise(resolve => setImmediate(resolve)); };
const library = (id: string, content: string) => ({id: 'e' + content + id, type: 'library.changed', resource: {kind: 'library', id}, data: {content, contentOrder: '1'}});

type Call = {path: string; signal?: AbortSignal; resolve(value: unknown): void; reject(error: unknown): void};
/** A server whose answers the test gives by hand, in order. */
function server() {
  const calls: Call[] = [];
  return {
    calls,
    api: {request<T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> {
      return new Promise<T>((resolve, reject) => {
        calls.push({path, signal, resolve: resolve as (value: unknown) => void, reject});
        signal?.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), {name: 'AbortError'})), {once: true});
      });
    }},
    count: (path: string) => calls.filter(c => c.path === path).length,
    last: (path: string) => [...calls].reverse().find(c => c.path === path)!,
  };
}
const path = '/v1/items/a/detail?related=none';
function detail(queries: ViewerQueries, extra: Partial<QueryObserverOptions> = {}) {
  return {
    queryKey: queryKeys.itemDetail('a', 'none'),
    queryFn: ({signal}: {signal: AbortSignal}) => queries.api.request<{title: string; cast: {name: string}[]}>(path, 'GET', undefined, signal),
    meta: {dependencies: [dependsOn.content('films')]},
    ...extra,
  } as QueryObserverOptions<{title: string; cast: {name: string}[]}>;
}
/** Every cache a test makes is ended with it, pass or fail (an idle document's collection timer would otherwise keep the run alive). */
const made: ViewerQueries[] = [];
test.afterEach(() => { for (const q of made.splice(0)) q.dispose(); });
function make(options: Parameters<typeof createViewerQueries>[0]) { const q = createViewerQueries(options); made.push(q); return q; }
function setup() {
  const c = clock(), hub = new FreshnessHub(c.env), s = server();
  const queries = make({scope: {serverId: 'server', viewerId: 'alex'}, hub, api: s.api});
  return {c, hub, s, queries};
}
function watch<T>(queries: ViewerQueries, options: QueryObserverOptions<T>) {
  const observer = new QueryObserver(queries.client, options);
  const results: {status: string; fetching: boolean; data: T | undefined}[] = [];
  const stop = observer.subscribe(r => { results.push({status: r.status, fetching: r.isFetching, data: r.data}); });
  return {observer, results, stop};
}

test('owners asking for the same document share one read, and Back shows it with no request', async () => {
  const {s, queries, c} = setup();
  const a = watch(queries, detail(queries));
  const guard = queries.client.fetchQuery(detail(queries));
  const menu = watch(queries, detail(queries));
  await flush();
  assert.equal(s.count(path), 1, 'screen, guard and menu share one read');
  s.last(path).resolve({title: 'Arrival', cast: [{name: 'Amy'}]});
  assert.equal((await guard).title, 'Arrival');
  a.stop(); menu.stop();
  await c.advance(60_000);
  // Back: the page is there at once, with nothing read.
  const again = watch(queries, detail(queries));
  assert.equal(again.observer.getCurrentResult().data?.title, 'Arrival');
  assert.equal(again.observer.getCurrentResult().isPending, false);
  await c.advance(60_000);
  assert.equal(s.count(path), 1, 'nothing moved, so nothing is read however long it stays open');
  again.stop(); queries.dispose();
});

test('a hint read again in place keeps what is shown, and unchanged parts keep their identity', async () => {
  const {s, queries, c, hub} = setup();
  const page = watch(queries, detail(queries));
  await flush(); s.last(path).resolve({title: 'Arrival', cast: [{name: 'Amy'}]});
  await flush();
  const before = page.observer.getCurrentResult().data!;
  hub.accept(library('films', '2'));
  await c.advance(1000);
  assert.equal(s.count(path), 2, 'one quiet read answers the hint');
  const during = page.observer.getCurrentResult();
  assert.equal(during.status, 'success', 'a refresh is not a first load');
  assert.equal(during.isFetching, true);
  assert.equal(during.data, before, 'the page stays on screen while it is read again');
  s.last(path).resolve({title: 'Arrival (2016)', cast: [{name: 'Amy'}]});
  await flush();
  const after = page.observer.getCurrentResult().data!;
  assert.equal(after.title, 'Arrival (2016)');
  assert.equal(after.cast, before.cast, 'what did not change keeps its identity, so it is not redrawn');
  assert.ok(page.results.every(r => r.data !== undefined || r.status === 'pending'), 'never blanked after the first answer');
  page.stop(); queries.dispose();
});

test('a hint for a closed document is owed, and read once when it is opened again', async () => {
  const {s, queries, c, hub} = setup();
  const page = watch(queries, detail(queries));
  await flush(); s.last(path).resolve({title: 'Arrival', cast: []});
  await flush(); page.stop();
  hub.accept(library('films', '2'));
  await c.advance(60_000);
  assert.equal(s.count(path), 1, 'nothing is read for a page nobody shows');
  const guard = queries.client.fetchQuery(detail(queries));
  await flush();
  assert.equal(s.count(path), 2, 'a caller asking for an owed document reads it again');
  s.last(path).resolve({title: 'Arrival (2016)', cast: []});
  assert.equal((await guard).title, 'Arrival (2016)');
  const again = watch(queries, detail(queries));
  await c.advance(60_000);
  assert.equal(s.count(path), 2, 'answered once, not again by the screen');
  again.stop(); queries.dispose();
});

test('a change made on this device reads the documents built from it again, and only those', async () => {
  const {s, queries, c} = setup();
  const page = watch(queries, detail(queries, {meta: {dependencies: dependsOn.personalTitle('films', 'a')}}));
  await flush(); s.last(path).resolve({title: 'Arrival', cast: []});
  await flush();
  // A change to another title of the library, named by this device.
  queries.moved(['personal:title:b']);
  await c.advance(5000);
  assert.equal(s.count(path), 1);
  queries.moved(['personal:title:a']);
  await c.advance(5000);
  assert.equal(s.count(path), 2);
  page.stop(); queries.dispose();
});

test('a failed quiet read keeps the page and is tried again on the hub\'s waits; a failed first load recovers by itself', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, backoffMs: [2000, 4000]}), s = server();
  const queries = make({scope: {serverId: 'server', viewerId: 'alex'}, hub, api: s.api});
  const page = watch(queries, detail(queries));
  await flush();
  s.last(path).reject(Object.assign(new Error('down'), {status: 503}));
  await flush();
  assert.equal(page.observer.getCurrentResult().status, 'error');
  await c.advance(2000);
  assert.equal(s.count(path), 2, 'the first load is tried again by itself');
  s.last(path).resolve({title: 'Arrival', cast: []});
  await flush();
  assert.equal(page.observer.getCurrentResult().data?.title, 'Arrival');
  hub.accept(library('films', '2'));
  await c.advance(1000);
  assert.equal(s.count(path), 3);
  s.last(path).reject(Object.assign(new Error('down'), {status: 503}));
  await flush();
  assert.equal(page.observer.getCurrentResult().data?.title, 'Arrival', 'a failed refresh never replaces what is shown');
  await c.advance(2000);
  assert.equal(s.count(path), 4, 'tried again after the first wait');
  s.last(path).resolve({title: 'Arrival (2016)', cast: []});
  await flush();
  assert.equal(page.observer.getCurrentResult().data?.title, 'Arrival (2016)');
  await c.advance(120_000);
  assert.equal(s.count(path), 4);
  page.stop(); queries.dispose();
});

test('a not-allowed or gone answer is final: it is not retried', async () => {
  const {s, queries, c} = setup();
  const page = watch(queries, detail(queries));
  await flush();
  s.last(path).reject(Object.assign(new Error('gone'), {status: 404}));
  await c.advance(120_000);
  assert.equal(s.count(path), 1);
  assert.equal(page.observer.getCurrentResult().status, 'error');
  page.stop(); queries.dispose();
});

test('a hint that arrives during the first load is answered by a read after it', async () => {
  const {s, queries, c, hub} = setup();
  const page = watch(queries, detail(queries));
  await flush();
  hub.accept(library('films', '2'));
  await c.advance(1000);
  assert.equal(s.count(path), 1, 'the first load is let finish');
  s.last(path).resolve({title: 'old', cast: []});
  await c.advance(1000);
  assert.equal(s.count(path), 2, 'its answer may predate the change, so it is read once more');
  s.last(path).resolve({title: 'new', cast: []});
  await flush();
  assert.equal(page.observer.getCurrentResult().data?.title, 'new');
  page.stop(); queries.dispose();
});

test('the cache keeps a bounded number of documents nobody shows, dropping the least recently shown', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), s = server();
  const queries = make({scope: {serverId: 'server', viewerId: 'alex'}, hub, api: s.api, maxInactive: 2});
  for (const id of ['a', 'b', 'c']) {
    const page = watch(queries, {queryKey: queryKeys.itemDetail(id, 'none'), queryFn: ({signal}) => queries.api.request(`/v1/items/${id}/detail`, 'GET', undefined, signal), meta: {dependencies: [dependsOn.content('films')]}});
    await flush(); s.last(`/v1/items/${id}/detail`).resolve({id});
    await flush(); page.stop();
  }
  const kept = queries.client.getQueryCache().getAll().map(q => q.queryKey[1]);
  assert.deepEqual(kept.sort(), ['b', 'c']);
  queries.dispose();
});

test('privacy: a viewer\'s cache ends with the viewer; nothing it read or was reading reaches the next', async () => {
  const c = clock(), s = server();
  const alexHub = new FreshnessHub(c.env);
  const alex = make({scope: {serverId: 'server', viewerId: 'alex'}, hub: alexHub, api: s.api});
  // Alex has a title page and search results, and a third read is on its way.
  const page = watch(alex, detail(alex));
  await flush(); s.last(path).resolve({title: 'Alex only', cast: []});
  await flush();
  void alex.client.fetchQuery({queryKey: ['search', 'private'], queryFn: ({signal}) => alex.api.request('/v1/search?q=private', 'GET', undefined, signal)}).catch(() => {});
  const late = watch(alex, {queryKey: queryKeys.itemDetail('late', 'none'), queryFn: ({signal}) => alex.api.request('/v1/items/late/detail', 'GET', undefined, signal), meta: {dependencies: [dependsOn.content('films')]}});
  await flush();
  const inFlight = s.last('/v1/items/late/detail');
  assert.equal(inFlight.signal?.aborted, false);

  // The profile is switched: the shell ends Alex's cache before the next viewer's exists.
  alex.dispose();
  assert.equal(alex.isDisposed(), true);
  assert.equal(inFlight.signal?.aborted, true, 'every request Alex\'s cache started is aborted');
  assert.equal(alex.client.getQueryCache().getAll().length, 0, 'nothing Alex read is kept');
  assert.equal(alex.client.getQueryData(queryKeys.itemDetail('a', 'none')), undefined);
  // A server that answers anyway is refused before anything is stored.
  inFlight.resolve({title: 'late, for Alex'});
  await flush();
  assert.equal(alex.client.getQueryCache().getAll().length, 0);
  await assert.rejects(alex.api.request('/v1/items/a/detail'), {name: 'AbortError'}, 'Alex\'s requester refuses new work');
  // Alex's screens are still unmounting: they read nothing and fetch nothing, even if they render again.
  assert.equal(page.observer.getCurrentResult().data, undefined);
  const before = s.calls.length;
  page.observer.setOptions(alex.client.defaultQueryOptions(detail(alex)));
  assert.equal(page.observer.getOptimisticResult(alex.client.defaultQueryOptions(detail(alex))).data, undefined);
  await flush();
  assert.equal(s.calls.length, before, 'a re-render of an ended viewer\'s screen fetches nothing');
  alexHub.accept(library('films', '9'));
  await c.advance(60_000);
  assert.equal(s.calls.length, before, 'a hint to the ended cache reads nothing');
  page.stop(); late.stop();

  // Kids: a new cache with nothing of Alex's in it.
  const kids = make({scope: {serverId: 'server', viewerId: 'kids'}, hub: new FreshnessHub(c.env), api: s.api});
  assert.equal(kids.client.getQueryCache().getAll().length, 0);
  const kidsPage = watch(kids, detail(kids));
  assert.equal(kidsPage.observer.getCurrentResult().data, undefined, 'the same key starts empty for another viewer');
  assert.equal(kidsPage.observer.getCurrentResult().isPending, true);
  await flush();
  assert.equal(s.count(path), 2, 'and is read for this viewer');
  kidsPage.stop(); kids.dispose();
});

test('a scope needs a server and a viewer', () => {
  const hub = new FreshnessHub();
  assert.throws(() => createViewerQueries({scope: {serverId: '', viewerId: 'alex'}, hub, api: server().api}));
  assert.throws(() => createViewerQueries({scope: {serverId: 'server', viewerId: ''}, hub, api: server().api}));
  hub.dispose();
});

test('a quiet read waits for a pause in the person\'s input before its answer changes the screen', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, settleMs: 300}), s = server();
  const queries = make({scope: {serverId: 'server', viewerId: 'alex'}, hub, api: s.api});
  const page = watch(queries, detail(queries));
  await flush(); s.last(path).resolve({title: 'Arrival', cast: []});
  await flush();
  hub.accept(library('films', '2'));
  await c.advance(1000);
  assert.equal(s.count(path), 2);
  // The person is pressing when the answer arrives: it is held.
  hub.touch(); s.last(path).resolve({title: 'Arrival (2016)', cast: []});
  await c.advance(100); hub.touch(); await c.advance(100);
  assert.equal(page.observer.getCurrentResult().data?.title, 'Arrival', 'nothing moves under a press');
  await c.advance(400);
  assert.equal(page.observer.getCurrentResult().data?.title, 'Arrival (2016)', 'shown once input pauses');
  page.stop();
});

test('a live value (a grid\'s controller) is let go once: when it is replaced, leaves the cache, or its viewer ends', async () => {
  const {queries} = setup();
  const released: string[] = [];
  const live = (name: string) => ({name});
  const options = (name: string) => ({queryKey: ['library', 'films', 'browse', name], queryFn: async () => live(name), meta: {release: (value: unknown) => released.push((value as {name: string}).name)}});
  await queries.client.fetchQuery(options('a'));
  queries.client.setQueryData(['library', 'films', 'browse', 'a'], live('a2'));
  assert.deepEqual(released, ['a'], 'a replaced value is let go');
  await queries.client.fetchQuery(options('b'));
  queries.client.removeQueries({queryKey: ['library', 'films', 'browse', 'b'], exact: true});
  assert.deepEqual(released, ['a', 'b'], 'a value leaving the cache is let go');
  queries.dispose();
  assert.deepEqual(released, ['a', 'b', 'a2'], 'and every value still held when the viewer ends');
});
