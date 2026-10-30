import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
async function fixture() {
  let route = {kind: 'shell', destination: {kind: 'read'}};
  const page = route, authority = {serverId: 'server', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'f'};
  let revision = 1, random = 0, onRandom: (() => void) | undefined, opens = 0, downloads = 0;
  const listeners = new Set<() => void>(), errors: unknown[] = [], writes: any[] = [], scopes: any[] = [];
  let write = async (kind: string, id: string, body: any): Promise<any> => ({itemId: id, revision: body.expectedRevision + 1,
    ...(kind === 'list' ? {listed: body.listed} : {read: body.read})});
  const context = {session: () => ({sessionFamilyId: 'family', viewer: {...authority, authority: 'local', role: 'member'}}),
    authority: () => authority, current: () => true, subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    navigation: {getSnapshot: () => ({route, revision}), subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}},
    api: {reading: (scope: any) => {scopes.push(scope); return {
      canUseMyList: () => true, canMarkRead: () => true,
      getMyList: async (id: string) => ({itemId: id, listed: false, revision: 7}), readMark: async (id: string) => ({itemId: id, read: false, revision: 4}),
      setMyList: async (id: string, body: any) => {assert.equal(scope.isCurrent(), true); writes.push({kind: 'list', id, body}); return write('list', id, body);},
      setReadMark: async (id: string, body: any) => {assert.equal(scope.isCurrent(), true); writes.push({kind: 'read', id, body}); return write('read', id, body);},
      dispose() {},
    };}},
  };
  const {NativeReadingActions} = await screenModule(new URL('../src-next/screens/read/reading-actions.ts', import.meta.url), {
    '../../../../../packages/client-core/src/reading-collection': {ReadingCollection: class {constructor() {throw new Error('unexpected collection opening');}}},
    react: {default: {}}, '../../shell/model': {sameAuthority: (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b)},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => {onRandom?.(); return new Uint8Array(16).fill(++random);}}},
    // Member fixtures never reach the owner's bulk editor (next-reading-selection-edit covers it).
    '../destinations/bulk-media': {NativeBulkMedia: class {constructor() {throw new Error('bulk editor opened for a member');}}},
    '../destinations/lifetime': {nativeDestinationLifetime: () => {throw new Error('unused');}},
    '../native-screen-services': {nativeScreenServices: () => {throw new Error('unused');}},
  });
  const actions = new NativeReadingActions(context, (error: unknown) => errors.push(error));
  assert.equal(listeners.size, 0); assert.equal(actions.arm(), true);
  const makeOffer = (id: string) => {
    const life = new AbortController(); let current = true;
    const offer = {item: {id, libraryId: 'library', title: id}, lifetime: {signal: life.signal, isCurrent: () => current},
      open: () => {opens++;}, download: () => {downloads++;}};
    return {offer, retire: () => {current = false; life.abort();}};
  };
  return {actions, makeOffer, errors, writes, scopes, listeners, opens: () => opens, downloads: () => downloads,
    onRandom: (fn?: () => void) => {onRandom = fn;}, write: (fn: typeof write) => {write = fn;},
    navigate: () => {route = {kind: 'shell', destination: {kind: 'other'}}; revision++; for (const listener of [...listeners]) listener();},
    back: () => {route = page; revision++; for (const listener of [...listeners]) listener();},
    changeAuthority: () => {authority.epoch = 'new'; for (const listener of [...listeners]) listener();}};
}

test('long press opens a menu; Download only executes the explicit menu command', async () => {
  const f = await fixture(), {offer} = f.makeOffer('one');
  try {
    f.actions.show(offer); assert.equal(f.actions.getSnapshot().menu, offer); assert.equal(f.downloads(), 0);
    await tick(); assert.equal(f.actions.getSnapshot().loading, false);
    f.actions.download({...offer}); assert.equal(f.downloads(), 0, 'a substituted menu offer is refused');
    f.actions.download(offer); assert.equal(f.downloads(), 1); assert.equal(f.actions.getSnapshot().menu, undefined);
    assert.deepEqual(f.errors, []);
  } finally {f.actions.dispose();}
});

test('explicit Select accumulates item identities and personal bulk actions use each actual revision', async () => {
  const f = await fixture(), one = f.makeOffer('one'), two = f.makeOffer('two');
  try {
    f.actions.start(); f.actions.toggle(one.offer); f.actions.toggle(two.offer);
    assert.equal(f.actions.getSnapshot().selected.length, 2);
    await f.actions.apply(f.actions.getSnapshot(), 'read');
    assert.deepEqual(f.writes.map(call => [call.kind, call.id, call.body.expectedRevision, call.body.read]), [['read', 'one', 4, true], ['read', 'two', 4, true]]);
    assert.notEqual(f.writes[0].body.operationId, f.writes[1].body.operationId);
    assert.equal(f.actions.getSnapshot().selected.length, 0); assert.equal(f.actions.getSnapshot().busy, false);
  } finally {f.actions.dispose();}
});

test('failed bulk item and remaining items stay selected without replaying successful writes', async () => {
  const f = await fixture(), failure = new Error('conflict');
  try {
    for (const id of ['one', 'two', 'three']) f.actions.toggle(f.makeOffer(id).offer);
    f.write(async (_kind, id, body) => {if (id === 'two') throw failure; return {itemId: id, listed: true, revision: body.expectedRevision + 1};});
    await assert.rejects(f.actions.apply(f.actions.getSnapshot(), 'list'), failure);
    assert.deepEqual(f.writes.map(call => call.id), ['one', 'two']);
    assert.deepEqual(f.actions.getSnapshot().selected.map((offer: any) => offer.item.id), ['two', 'three']);
    assert.equal(f.actions.getSnapshot().busy, false);
  } finally {f.actions.dispose();}
});

test('retired row/query lifetime prunes selection and its retained menu cannot dispatch', async () => {
  const f = await fixture(), row = f.makeOffer('one');
  try {
    f.actions.toggle(row.offer); f.actions.show(row.offer); await tick();
    const prior = f.actions.getSnapshot(); row.retire(); f.actions.reconcile();
    assert.equal(f.actions.getSnapshot().selected.length, 0); assert.equal(f.actions.getSnapshot().menu, undefined);
    f.actions.open(row.offer); f.actions.download(row.offer); await f.actions.personal(prior, 'listed');
    assert.equal(f.opens(), 0); assert.equal(f.downloads(), 0); assert.equal(f.writes.length, 0);
    assert.equal(f.scopes.every(scope => !scope.isCurrent()), true, 'client scopes retain the query lifetime, not only the route');
  } finally {f.actions.dispose();}
});

test('retained/forged My List membership cannot replace the object drawn by the hero', async () => {
  const f = await fixture(), {offer} = f.makeOffer('one');
  try {
    const listed = await f.actions.inspect(offer);
    assert.equal(await f.actions.toggleListed(offer, {...listed}), undefined); assert.equal(f.writes.length, 0);
    const next = await f.actions.toggleListed(offer, listed);
    assert.equal(next.listed, true); assert.equal(f.writes[0].body.expectedRevision, 7);
    assert.equal(await f.actions.toggleListed(offer, listed), undefined); assert.equal(f.writes.length, 1);
  } finally {f.actions.dispose();}
});

test('retirement during operation ID creation refuses the original write and reentrant action', async () => {
  const f = await fixture(), {offer} = f.makeOffer('one'); let nested: Promise<void> | undefined;
  try {
    f.actions.show(offer); await tick(); const rendered = f.actions.getSnapshot();
    f.onRandom(() => {f.onRandom(); nested = f.actions.personal(rendered, 'mark'); f.navigate();});
    await f.actions.personal(rendered, 'listed'); await nested;
    assert.equal(f.writes.length, 0); assert.equal(f.actions.getSnapshot().menu, undefined);
  } finally {f.actions.dispose();}
});

test('late personal write cannot publish after route retirement or clear another arm', async () => {
  const f = await fixture(), {offer} = f.makeOffer('one'), pending = deferred<any>();
  try {
    f.actions.show(offer); await tick(); f.write(async () => pending.promise);
    const command = f.actions.personal(f.actions.getSnapshot(), 'mark'); assert.equal(f.writes.length, 1);
    f.actions.release(); f.actions.arm();
    pending.resolve({itemId: 'one', read: true, revision: 5}); await command;
    assert.equal(f.actions.getSnapshot().menu, undefined); assert.equal(f.actions.getSnapshot().mark, undefined);
    f.actions.show(offer); await tick(); assert.equal(f.actions.getSnapshot().menu, offer, 'mount cleanup can rearm the same memoized action owner');
  } finally {pending.resolve(undefined); f.actions.dispose();}
});

test('a dismissal listener that retires the route prevents Read from firing afterward', async () => {
  const f = await fixture(), {offer} = f.makeOffer('one');
  try {
    f.actions.show(offer); await tick();
    const stop = f.actions.subscribe(() => {if (!f.actions.getSnapshot().menu) {stop(); f.navigate();}});
    f.actions.open(offer); assert.equal(f.opens(), 0);
  } finally {f.actions.dispose();}
});


test('retained page can reopen menus and select after Back, but authority retirement is permanent', async () => {
  const f = await fixture(), {offer} = f.makeOffer('one');
  try {
    f.actions.toggle(offer); f.actions.show(offer); await tick();
    const oldScopes = [...f.scopes];
    f.navigate();
    assert.equal(f.actions.getSnapshot().menu, undefined);
    assert.equal(f.actions.getSnapshot().selecting, false);
    assert.equal(f.actions.getSnapshot().selected.length, 0);
    f.actions.show(offer); f.actions.toggle(offer);
    assert.equal(f.actions.getSnapshot().menu, undefined);
    assert.equal(f.actions.getSnapshot().selecting, false);
    f.back();
    assert.equal(oldScopes.every(scope => !scope.isCurrent()), true, 'return cannot revive retired IO');
    f.actions.show(offer); await tick();
    assert.equal(f.actions.getSnapshot().menu, offer);
    f.actions.toggle(offer);
    assert.equal(f.actions.selected(offer.item), true);
    f.changeAuthority();
    assert.equal(f.actions.getSnapshot().selecting, false);
    assert.equal(f.listeners.size, 0);
    assert.equal(f.actions.arm(), false);
    f.actions.show(offer); f.actions.toggle(offer);
    assert.equal(f.actions.getSnapshot().menu, undefined);
    assert.equal(f.actions.getSnapshot().selected.length, 0);
  } finally {f.actions.dispose();}
});
