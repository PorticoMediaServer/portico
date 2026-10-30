import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = <T,>() => {let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
async function fixture() {
  const authority = {serverId: 'server', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'f'};
  const series = {id: 's'.repeat(43), name: 'Series'}, book = {id: 'book', libraryId: 'library', sourceId: 'source', format: 'pdf', title: 'Book', inventoryAvailable: true, series};
  const one = {...book, id: 'one', sourceId: 'one-source'}, two = {...book, id: 'two', sourceId: 'two-source'};
  const route = {kind: 'read-book', libraryId: 'library', item: book}, manifest = {itemId: 'book', sourceId: 'source', format: 'pdf', metadata: {title: 'Book'}};
  let respond = async (): Promise<any> => ({items: [book, one], nextCursor: 'continuation'}), calls = 0;
  const {NativeReadBook} = await screenModule(new URL('../src-next/screens/read/read-book-model.ts', import.meta.url), {
    react: {default: {}}, '../../../../../packages/client-core/src': {ReadingError: class extends Error {}}, '../../shell/model': {sameAuthority: (a: unknown, b: unknown) => a === b},
    '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(16)}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({category: error.message === 'offline' ? 'offline' : 'unexpected'})},
    '../downloads/request': {askNativeDownload() {}}, './eligibility': {nativeReadOrigin: () => ({route, revision: 1}), openNativeReadBook: async () => true},
  });
  const client = {versions: async () => ({chosenSourceId: 'source', versions: [{sourceId: 'source', format: 'pdf', inventoryAvailable: true}]}),
    open: async () => ({manifest, progress: async () => ({progress: null}), close: async () => {}}),
    canUseMyList: () => false, canMarkRead: () => false, series: async () => {calls++; return respond();}, dispose() {}};
  const model = new NativeReadBook({api: {reading: () => client}, authority: () => authority, current: () => true,
    session: () => ({sessionFamilyId: 'family', viewer: {authority: 'local', role: 'member', ...authority}}), navigation: {getSnapshot: () => ({route, revision: 1})}}, route);
  model.arm(); await model.load(); await model.loadSeriesShelf(manifest, series);
  return {model, manifest, one, two, calls: () => calls, respond: (next: typeof respond) => {respond = next;}};
}

test('book shelf publishes pending before IO and settles into the same ordered volumes', async () => {
  const f = await fixture(), tail = deferred<any>();
  try {
    const rendered = f.model.shelfSnapshot(), lifetime = f.model.shelfItemLifetime(rendered.witness, f.manifest, f.one);
    assert.equal(lifetime.isCurrent(), true); f.respond(async () => tail.promise);
    const request = f.model.moreSeriesShelf(rendered.witness, f.manifest), pending = f.model.shelfSnapshot();
    assert.equal(pending.morePending, true); assert.deepEqual(pending.items, [f.one]);
    assert.equal(lifetime.isCurrent(), false, 'old menu obeys the existing exact shelf witness retirement');
    await f.model.moreSeriesShelf(rendered.witness, f.manifest); assert.equal(f.calls(), 2);
    tail.resolve({items: [f.two]}); await request;
    assert.notEqual(f.model.shelfSnapshot().morePending, true); assert.deepEqual(f.model.shelfSnapshot().items, [f.one, f.two]);
  } finally {tail.resolve({items: []}); f.model.dispose();}
});

test('transient shelf failure removes the loading tail while the exact failed witness can retry', async () => {
  const f = await fixture(), tail = deferred<any>();
  try {
    f.respond(async () => tail.promise); const request = f.model.moreSeriesShelf(f.model.shelfSnapshot().witness, f.manifest);
    const failure = new Error('offline'); tail.reject(failure); await request;
    const failed = f.model.shelfSnapshot(); assert.equal(failed.morePending, false); assert.equal(failed.moreError, failure);
    assert.deepEqual(failed.items, [f.one]); f.respond(async () => ({items: [f.two]}));
    await f.model.retrySeriesShelf(failed.witness, f.manifest);
    assert.deepEqual(f.model.shelfSnapshot().items, [f.one, f.two]); assert.equal(f.model.shelfSnapshot().moreError, undefined);
    f.model.release(); assert.equal(f.model.shelfItemLifetime(failed.witness, f.manifest, f.one), undefined);
  } finally {tail.resolve({items: []}); f.model.dispose();}
});
