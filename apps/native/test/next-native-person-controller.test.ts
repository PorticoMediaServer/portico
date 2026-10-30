import assert from 'node:assert/strict';
import test from 'node:test';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {createNativePersonController} from '../src-next/screens/people/controller';
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

test('Person uses exact id and canonical bounded facts, without library inference or eager requests', async () => {
  const f = await fixture();
  try {
    assert.equal(f.reads.length, 0); assert.equal(f.source.getSnapshot(), f.source.getSnapshot());
    assert.throws(() => createNativePersonController(f.context, {...f.route}), /route_required/);
    await f.source.load();
    const state = f.source.getSnapshot(); assert.equal(state.phase, 'ready'); assert.ok(state.page);
    assert.equal(state.page.person.id, f.route.personId); assert.equal(state.page.viewerFence, 'Person-own-fence');
    assert.equal(state.page.person.name, 'Stored Person'); assert.equal(state.page.pageInfo.total, 82);
    const url = new URL(f.reads[0]!.path, 'https://fixture.invalid');
    assert.equal(url.pathname, '/v1/people/person-without-library'); assert.equal(url.searchParams.get('limit'), '40');
    assert.equal(url.searchParams.get('role'), 'all'); assert.equal(url.searchParams.has('libraryId'), false);
    const entry = state.page.credits[0]!.media;
    assert.equal(f.source.contains(entry), true); assert.equal(f.source.contains({...entry}), false);
    await f.source.load(); assert.equal(f.reads.length, 1);
  } finally {await f.close();}
});

test('Person preserves arrived source through Title/Back and permanently retires on route drop', async () => {
  const f = await fixture();
  try {
    await f.source.load(); const arrived = f.source.getSnapshot(); assert.ok(arrived.page);
    const entry = arrived.page.credits[0]!.media;
    f.context.navigation.open({kind: 'title', entry}, f.context.authority());
    assert.equal(f.source.activation.getSnapshot().visible, false); assert.equal(f.source.contains(entry), false);
    await f.source.select({role: 'crew'}); assert.equal(f.reads.length, 1);
    f.context.navigation.back(f.context.authority());
    assert.equal(f.source.activation.getSnapshot().intent, 'restore'); assert.equal(f.source.getSnapshot(), arrived);
    assert.equal(f.source.contains(entry), true); await f.source.load(); assert.equal(f.reads.length, 1);
    f.context.navigation.back(f.context.authority()); assert.equal(f.source.getSnapshot().page, undefined);
    f.context.navigation.open(f.route, f.context.authority()); await f.source.load();
    assert.equal(f.source.activation.getSnapshot().visible, false); assert.equal(f.reads.length, 1);
  } finally {await f.close();}
});

test('Person role changes supersede late pages and retained hidden requests cannot publish', async () => {
  const f = await fixture(), late = deferred<unknown>();
  try {
    f.reply(async () => late.promise); const first = f.source.load(); await tick();
    f.reply(async () => ({...page(), credits: []})); await f.source.select({role: 'crew'});
    assert.equal(f.source.getSnapshot().phase, 'ready'); assert.deepEqual(f.source.getSnapshot().group, {role: 'crew'});
    late.resolve(page()); await first; assert.equal(f.source.getSnapshot().page!.credits.length, 0);
    const hidden = deferred<unknown>(); f.reply(async () => hidden.promise); const pending = f.source.select({role: 'all'}); await tick();
    f.context.navigation.open({kind: 'title', entry: film('actual-title')}, f.context.authority());
    hidden.resolve(page()); await pending; assert.equal(f.source.getSnapshot().page, undefined);
    f.context.navigation.back(f.context.authority()); f.reply(async () => page()); await f.source.load();
    assert.equal(f.source.getSnapshot().phase, 'ready');
  } finally {late.resolve(page()); await f.close();}
});

test('Person continuation replaces the bounded resident page and rejects cross-revision splicing', async () => {
  const f = await fixture();
  try {
    f.reply(async () => page(undefined, 0, 40, 'opaque-next'));
    await f.source.load(); const old = f.source.getSnapshot().page!.credits[0]!.media;
    f.reply(async () => page(undefined, 40, 40, 'opaque-after'));
    await f.source.next(); assert.equal(f.source.getSnapshot().page!.credits.length, 40); assert.equal(f.source.contains(old), false);
    assert.equal(new URL(f.reads[1]!.path, 'https://fixture.invalid').searchParams.get('cursor'), 'opaque-next');
    f.reply(async () => page(undefined, 80, 2, '', 'Changed-person-fence'));
    await f.source.next(); assert.equal(f.source.getSnapshot().phase, 'error'); assert.equal(f.source.getSnapshot().page, undefined);
  } finally {await f.close();}
});

test('Person authority retirement clears facts and refuses a late actual read', async () => {
  const f = await fixture(), late = deferred<unknown>();
  try {
    f.reply(async () => late.promise); const pending = f.source.load(); await tick(); f.retire();
    assert.equal(f.source.getSnapshot().page, undefined); assert.equal(f.source.activation.getSnapshot().visible, false);
    late.resolve(page()); await pending; await f.source.load(); assert.equal(f.reads.length, 1);
    assert.equal(f.source.getSnapshot().phase, 'idle'); f.source.dispose(); f.source.dispose();
  } finally {late.resolve(page()); await f.close();}
});
