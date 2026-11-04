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
    // A reply may be the server's refusal: {refuse: code} answers 400 in the server's error envelope.
    if (body && typeof body === 'object' && 'refuse' in body) return {status: 400, body: {error: {code: (body as {refuse: string}).refuse, message: 'refused'}}};
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
const credits = (path: string, fence = 'Person-own-fence', catalog = 12) => {
  const cursor = new URL(path, 'https://fixture.invalid').searchParams.get('cursor');
  const start = cursor ? Number(cursor.slice('opaque-'.length)) : 0;
  const nextCursor = start + 40 < 320 ? `opaque-${start + 40}` : '';
  return {...page(undefined, start, Math.min(40, 320 - start), nextCursor, fence), revision: {catalog, viewer: 7}, pageInfo: {total: 320, nextCursor}};
};
const hasCursor = (path: string) => new URL(path, 'https://fixture.invalid').searchParams.has('cursor');

test('Person sparse credits borrow one arrived page and follow only real continuation tokens', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load();
    window = createNativePersonCreditWindow(f.context, f.route, f.source);
    assert.equal(f.reads.length, 1); assert.equal(window.collection.resident().items, 40, 'the arrived page is resident at once');
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
    const before = f.reads.length;
    f.reply(async path => credits(path, 'Person-own-fence', 13));
    await ready(window, 40, 40, true);
    assert.equal(window.collection.getSnapshot().status, 'error'); assert.equal(window.collection.itemAt(40), undefined);
    assert.equal(f.source.getSnapshot().phase, 'ready');
    assert.equal(f.reads.length, before + 1, 'a moved revision is not read again: the page shows its refusal');
  } finally {window?.dispose(); await f.close();}
});

test('step 7: a continuation answered under a moved fence is the newer answer for the same person revision', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    f.reply(async path => credits(path, 'Changed-person-fence'));
    await ready(window, 40);
    assert.equal(window.collection.itemAt(40)?.media.id, 'credit-40');
  } finally {window?.dispose(); await f.close();}
});

test('step 7: a refused credit cursor reads the person again once in place of the shown page; a second refusal shows', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    const shown = f.source.getSnapshot(), phases: string[] = [];
    const off = f.source.subscribe(() => phases.push(f.source.getSnapshot().phase));
    f.reply(async path => hasCursor(path) ? {refuse: 'invalid_cursor'} : credits(path, 'Moved-person-fence'));
    window.collection.ensureRange(40, 40);
    for (let i = 0; i < 50 && f.source.getSnapshot() === shown; i++) await tick();
    off();
    const reread = f.source.getSnapshot();
    assert.notEqual(reread, shown); assert.equal(reread.phase, 'ready'); assert.equal(reread.pageOrigin, 'initial');
    assert.equal(reread.page?.viewerFence, 'Moved-person-fence');
    assert.ok(!phases.includes('loading'), 'the shown page stays until the re-read answers');
    assert.equal(window.collection.getSnapshot().status === 'error', false, 'the replaced window shows no error');
    window.dispose();
    // The renderer builds a new window from the new initial page; its cursor refused again is shown, not read again.
    window = createNativePersonCreditWindow(f.context, f.route, f.source);
    const before = f.reads.length;
    await ready(window, 40, 40, true);
    assert.equal(window.collection.getSnapshot().status, 'error'); assert.equal(f.source.getSnapshot(), reread);
    assert.equal(f.reads.length, before + 1);
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

test('step 7: a later access change in the same visit reads the person again too, once a credit page has answered since the last re-read', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    // Continuations follow a script: refused, answered, refused, refused. Each first page names a new fence.
    const script = ['refuse', 'answer', 'refuse', 'refuse']; let firsts = 0;
    f.reply(async path => hasCursor(path) ? (script.shift() === 'answer' ? credits(path, `fence-${firsts}`) : {refuse: 'invalid_cursor'}) : credits(path, `fence-${++firsts}`));
    const replaced = async (shown: ReturnType<typeof f.source.getSnapshot>) => {for (let i = 0; i < 50 && f.source.getSnapshot() === shown; i++) await tick(); return f.source.getSnapshot();};
    // First access change: page 1 is refused, and the person is read again.
    let shown = f.source.getSnapshot(); window.collection.ensureRange(40, 40);
    const second = await replaced(shown); assert.notEqual(second, shown); assert.equal(second.page?.viewerFence, 'fence-1');
    window.dispose(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    // Paging goes on: page 1 answers; a second access change refuses page 2, and the person is read again.
    await ready(window, 40); shown = f.source.getSnapshot(); window.collection.ensureRange(80, 80);
    const third = await replaced(shown); assert.notEqual(third, second); assert.equal(third.phase, 'ready'); assert.equal(third.page?.viewerFence, 'fence-2');
    window.dispose(); window = createNativePersonCreditWindow(f.context, f.route, f.source);
    // A refusal straight after that re-read, with no page answered in between, shows.
    const before = f.reads.length;
    await ready(window, 40, 40, true);
    assert.equal(window.collection.getSnapshot().status, 'error'); assert.equal(f.source.getSnapshot(), third); assert.equal(f.reads.length, before + 1);
  } finally {window?.dispose(); await f.close();}
});

test('a window built from an arrived page has that first page at once, with no request, so a re-read paints no placeholders', async () => {
  const f = await fixture(); let window: ReturnType<typeof createNativePersonCreditWindow> | undefined;
  try {
    f.reply(async path => credits(path)); await f.source.load();
    const before = f.reads.length;
    window = createNativePersonCreditWindow(f.context, f.route, f.source);
    assert.equal(window.collection.itemAt(0)?.media.id, 'credit-0'); assert.equal(window.collection.itemAt(39)?.media.id, 'credit-39');
    assert.equal(window.collection.itemAt(40), undefined); assert.equal(window.collection.getSnapshot().total, 320);
    window.collection.ensureRange(0, 39); await tick();
    assert.equal(f.reads.length, before, 'page 0 is the arrived page: not read again');
  } finally {window?.dispose(); await f.close();}
});
