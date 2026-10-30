import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Controller-boundary regression sources; no actual reader/session/server is mocked into acceptance.
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
async function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'one', viewerFence: 'viewer'};
  const item = {id: 'book', libraryId: 'books', title: 'Book', sourceId: 'source', format: 'pdf', inventoryAvailable: true};
  const route = {kind: 'read-book', libraryId: 'books', item};
  const manifest = {itemId: 'book', sourceId: 'source', format: 'pdf', sourceRevision: 'a'.repeat(64), metadata: {title: 'Book'}};
  let revision = 1, beforeCurrent: (() => void) | undefined, beforeRandom: (() => void) | undefined, beforeRevision: (() => void) | undefined;
  let next = 0, reply: (kind: string, body: any) => Promise<any> = async (kind, body) => kind === 'read' ? {revision: 10, read: body.read} : {revision: 5, listed: body.listed};
  const calls: {kind: string; id: string; body: any; signal: AbortSignal}[] = [];
  const read = {read: false, get revision() {beforeRevision?.(); return 9;}};
  const listed = {listed: false, revision: 4};
  const client = {
    versions: async () => ({chosenSourceId: 'source', versions: [{sourceId: 'source', format: 'pdf', inventoryAvailable: true, size: 10}]}),
    open: async () => ({manifest, progress: async () => ({location: {format: 'pdf', pageIndex: 0, progression: 0}, sourceRevision: manifest.sourceRevision}), close: async () => {}}),
    canUseMyList: () => true, canMarkRead: () => true, getMyList: async () => listed, readMark: async () => read,
    setReadMark: async (id: string, body: any, signal: AbortSignal) => {calls.push({kind: 'read', id, body, signal}); return reply('read', body);},
    setMyList: async (id: string, body: any, signal: AbortSignal) => {calls.push({kind: 'list', id, body, signal}); return reply('list', body);}, dispose() {},
  };
  const {NativeReadBook} = await screenModule(new URL('../src-next/screens/read/read-book-model.ts', import.meta.url), {
    react: {default: {}},
    '../../../../../packages/client-core/src': {ReadingError: class extends Error {}},
    '../../shell/model': {sameAuthority: (a: unknown, b: unknown) => a === b},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => {beforeRandom?.(); return new Uint8Array(16).fill(++next);}}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({})},
    '../downloads/request': {askNativeDownload: () => {throw new Error('not a download fixture');}},
    './eligibility': {nativeReadOrigin: () => ({route, revision})},
  });
  const context = {api: {reading: () => client}, session: () => ({sessionFamilyId: 'family', viewer: {authority: 'local', ...authority}}),
    authority: () => authority, current: () => {beforeCurrent?.(); return true;}, navigation: {getSnapshot: () => ({route, revision})}};
  const model = new NativeReadBook(context, route);
  model.arm(); await model.load(); assert.equal(model.getSnapshot().phase, 'ready');
  return {model, calls, read, listed, manifest,
    current: (callback?: () => void) => {beforeCurrent = callback;}, random: (callback?: () => void) => {beforeRandom = callback;},
    revision: (callback?: () => void) => {beforeRevision = callback;}, respond: (callback: typeof reply) => {reply = callback;},
    reopen: async () => {model.release(); revision++; model.arm(); await model.load();}};
}

test('Finished and My List send the exact original offered revisions and boolean intent', async () => {
  const f = await fixture();
  try {
    assert.equal(await f.model.toggleRead(f.read), true);
    assert.equal(await f.model.toggleList(f.listed), true);
    assert.deepEqual(f.calls.map(value => [value.kind, value.id, value.body.expectedRevision]), [['read', 'book', 9], ['list', 'book', 4]]);
    assert.equal(f.calls[0]!.body.read, true); assert.equal(f.calls[1]!.body.listed, true);
    assert.notEqual(f.calls[0]!.body.operationId, f.calls[1]!.body.operationId);
    assert.equal(f.model.getSnapshot().read.read, true);
  } finally {f.model.dispose();}
});

test('equal-looking read/list descriptors cannot substitute for the original offered objects', async () => {
  const f = await fixture();
  try {
    assert.equal(await f.model.toggleRead({...f.read}), false);
    assert.equal(await f.model.toggleList({...f.listed}), false);
    assert.equal(f.calls.length, 0);
  } finally {f.model.dispose();}
});

test('command is claimed before a currentness callback can reenter either personal action', async () => {
  const f = await fixture(); let nested: Promise<boolean> | undefined;
  try {
    f.current(() => {f.current(); nested = f.model.toggleList(f.listed);});
    assert.equal(await f.model.toggleRead(f.read), true);
    assert.equal(await nested, false); assert.equal(f.calls.length, 1); assert.equal(f.calls[0]!.kind, 'read');
  } finally {f.model.dispose();}
});

test('operation-id callbacks cannot dispatch a second command or retire then send the first', async () => {
  const f = await fixture(); let nested: Promise<boolean> | undefined;
  try {
    f.random(() => {f.random(); nested = f.model.toggleList(f.listed); f.model.release();});
    assert.equal(await f.model.toggleRead(f.read), false); assert.equal(await nested, false); assert.equal(f.calls.length, 0);
  } finally {f.model.dispose();}
});

test('retirement during original revision capture refuses before IO', async () => {
  const f = await fixture();
  try {
    f.revision(() => {f.revision(); f.model.release();});
    assert.equal(await f.model.toggleRead(f.read), false); assert.equal(f.calls.length, 0);
  } finally {f.model.dispose();}
});

test('late settlement cannot publish into or clear a successor command', async () => {
  const f = await fixture(), old = deferred<any>(), successor = deferred<any>();
  try {
    f.respond(async () => f.calls.length === 1 ? old.promise : successor.promise);
    const first = f.model.toggleRead(f.read); assert.equal(f.calls.length, 1);
    await f.reopen(); const second = f.model.toggleRead(f.read); assert.equal(f.calls.length, 2);
    old.resolve({revision: 10, read: true}); assert.equal(await first, false);
    assert.equal(f.model.getSnapshot().read, f.read);
    assert.equal(await f.model.toggleList(f.listed), false, 'old finally did not clear the successor claim');
    successor.resolve({revision: 10, read: true}); assert.equal(await second, true);
    assert.equal(f.calls.length, 2);
  } finally {old.resolve(undefined); successor.resolve(undefined); f.model.dispose();}
});
