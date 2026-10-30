import assert from 'node:assert/strict';
import test from 'node:test';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {createNativePersonController} from '../src-next/screens/people/controller';
import {createNativePersonCreditWindow} from '../src-next/screens/people/credit-window';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {resolve, promise};};
const film = (id: string): ContentEntry => ({id, kind: 'movie', libraryId: 'films', title: id, navigation: {view: 'item', entityId: id}});
function page(personId = 'person-without-library', start = 0, count = 2, cursor = '', viewer = 'Person-own-fence') {
  return {serverId: 'demo-server', viewerFence: viewer, revision: {catalog: 12, viewer: 7},
    person: {id: personId, name: 'Stored Person', revision: 3, biography: 'Actual biography', knownFor: [film('known-film')]},
    groups: [{id: 'movies', kind: 'movie', total: 82}],
    credits: Array.from({length: count}, (_, index) => ({media: film(`credit-${start + index}`), creditKind: 'cast', role: 'Stored role', character: 'Stored character'})),
    pageInfo: {total: 82, nextCursor: cursor}};
}
async function fixture() {
  const reads: {path: string; signal?: AbortSignal}[] = [];
  let reply = async (_path: string): Promise<unknown> => page();
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string; signal?: AbortSignal}, f: {pin: {serverId: string}}) => {
    if (!request.path.startsWith('/v1/people/')) return;
    reads.push({path: request.path, signal: request.signal});
    const body = await reply(request.path);
    return {status: 200, body: {...body as object, serverId: f.pin.serverId}};
  }});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    const route = {kind: 'person', personId: 'person-without-library'} as const;
    assert.equal(context.navigation.open(route, context.authority()), true);
    const source = createNativePersonController(context, route);
    return {source, context, route, reads, reply: (value: typeof reply) => {reply = value;},
      retire: () => runtime!.dispose(), close: async () => {source.dispose(); runtime?.dispose(); await app.unmount(); app.disposeLoader();}};
  } catch (error) {runtime?.dispose(); await app.unmount(); app.disposeLoader(); throw error;}
}

function ready(window: ReturnType<typeof createNativePersonCreditWindow>, first: number, last = first, error = false, request = true) {
  return new Promise<void>((resolve, reject) => {
    let offPage = () => {}, off = () => {};
    const timer = setTimeout(() => finish(new Error('actual credit page did not settle')), 10000);
    const finish = (failure?: unknown) => {clearTimeout(timer); offPage(); off(); if (failure) reject(failure); else resolve();};
    const check = () => {
      const state = window.collection.getSnapshot();
      if (state.status === 'error') {finish(error ? undefined : state.error); return;}
      if (window.collection.itemAt(first)) finish(error ? new Error('unexpected accepted credit page') : undefined);
    };
    offPage = window.collection.subscribePage(window.collection.pageOf(first), check);
    off = window.collection.subscribe(check);
    if (request) window.collection.ensureRange(first, last);
    check();
  });
}
const credits = (path: string, fence = 'Person-own-fence') => {
  const cursor = new URL(path, 'https://fixture.invalid').searchParams.get('cursor');
  const start = cursor ? Number(cursor.slice('opaque-'.length)) : 0;
  const nextCursor = start + 40 < 320 ? `opaque-${start + 40}` : '';
  return {...page(undefined, start, Math.min(40, 320 - start), nextCursor, fence), pageInfo: {total: 320, nextCursor}};
};

test('Person sparse credits borrow one arrived page and follow only real continuation tokens', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load();
    window = createNativePersonCreditWindow(f.context, f.route, f.source);
    assert.equal(f.reads.length, 1); assert.equal(window.collection.resident().items, 0);
    await ready(window, 0); assert.equal(f.reads.length, 1, 'seed does not reread page zero');
    const old = window.collection.itemAt(0)!.media;
    assert.equal(window.contains(old), true); assert.equal(window.contains({...old}), false);
    await ready(window, 200).catch(error => {throw new Error(`${error.message}; fixture reads=${f.reads.map(read => new URL(read.path, 'https://fixture.invalid').search).join('|')}; window=${JSON.stringify(window!.collection.getSnapshot())}; resident=${JSON.stringify(window!.collection.resident())}`);});
    await ready(window, 240, 240, false, false); // Existing collection direction-prefetch, without moving interest.
    const paths = f.reads.slice(1).map(read => new URL(read.path, 'https://fixture.invalid'));
    assert.deepEqual(paths.map(url => url.searchParams.get('cursor')), ['opaque-40', 'opaque-80', 'opaque-120', 'opaque-160', 'opaque-200', 'opaque-240']);
    assert.ok(paths.every(url => !url.searchParams.has('offset') && url.searchParams.get('limit') === '40'));
    assert.equal(window.collection.itemAt(200)!.media.id, 'credit-200');
    assert.equal(window.collection.getSnapshot().total, 320); assert.ok(window.collection.resident().items <= 200);
  } finally {window?.dispose(); await f.close();}
});

test('Person Title/Back preserves actual resident credits while group replacement retires the old window', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    await ready(window, 40); const entry = window.collection.itemAt(40)!.media;
    f.context.navigation.open({kind: 'title', entry}, f.context.authority());
    assert.equal(window.contains(entry), false); assert.equal(window.collection.itemAt(40)!.media, entry);
    f.context.navigation.back(f.context.authority()); assert.equal(window.contains(entry), true);
    const oldCount = f.reads.length; await f.source.select({role: 'cast'});
    assert.equal(window.collection.resident().items, 0); assert.equal(window.contains(entry), false);
    window.collection.ensureRange(80, 80); await tick(); assert.equal(f.reads.length, oldCount + 1);
    assert.equal(f.source.getSnapshot().phase, 'ready', 'disposing a borrowed window never disposes its parent source');
  } finally {window?.dispose(); await f.close();}
});

test('Person window refuses a continuation from another revision and publishes no rows from it', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    f.reply(async path => credits(path, 'Changed-person-fence'));
    await ready(window, 40, 40, true);
    assert.equal(window.collection.getSnapshot().status, 'error'); assert.equal(window.collection.itemAt(40), undefined);
    assert.equal(f.source.getSnapshot().phase, 'ready');
  } finally {window?.dispose(); await f.close();}
});

test('Person window owner retirement aborts pending credit traffic and cannot accept its late answer', async () => {
  const f = await fixture(), late = deferred<unknown>(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    f.reply(async () => late.promise); window.collection.ensureRange(40, 40); await tick();
    f.context.navigation.back(f.context.authority());
    assert.equal(window.collection.resident().items, 0);
    late.resolve(page(undefined, 40, 40, 'opaque-80')); await tick(); await tick();
    assert.equal(window.collection.itemAt(40), undefined); assert.equal(window.contains(film('credit-40')), false);
    f.context.navigation.open(f.route, f.context.authority()); window.collection.ensureRange(40, 40); await tick();
    assert.equal(f.reads.length, 2); window.dispose(); window.dispose();
  } finally {late.resolve(page()); window?.dispose(); await f.close();}
});

test('Person credit window requires actual initial-request provenance and never renumbers a continuation seed', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load();
    assert.equal(f.source.getSnapshot().pageOrigin, 'initial');
    await f.source.next();
    const continuation = f.source.getSnapshot();
    assert.equal(continuation.pageOrigin, 'continuation'); assert.equal(continuation.page!.credits[0]!.media.id, 'credit-40');
    assert.throws(() => createNativePersonCreditWindow(f.context, f.route, f.source), /initial_page_required/);
    assert.equal(f.reads.length, 2, 'refusing continuation provenance performs no window request');
    await f.source.select({role: 'all'}); assert.equal(f.source.getSnapshot().pageOrigin, 'initial');
    window = createNativePersonCreditWindow(f.context, f.route, f.source); await ready(window, 0);
    assert.equal(window.collection.itemAt(0)!.media.id, 'credit-0'); assert.equal(f.reads.length, 3);
  } finally {window?.dispose(); await f.close();}
});
