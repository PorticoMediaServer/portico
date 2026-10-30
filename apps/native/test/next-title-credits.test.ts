import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeTitleCredits} from '../src-next/screens/watch/title-credits.ts';
import {reconcileRowSlots, rowNeedsPage} from '../src-next/kit/layout/row-state.ts';
import type {CreditPage, DetailCredit} from '../../../packages/client-core/src/detail';
const revision = {catalog: 7, viewer: 3};
const credit = (id: string): DetailCredit => ({id, name: id, role: 'Actor', department: 'Acting', provider: 'stored', personId: `person-${id}`});
const page = (group: 'cast' | 'crew', ids: string[], nextCursor?: string): CreditPage => ({group, revision, credits: ids.map(credit), total: 100, ...(nextCursor ? {nextCursor} : {})});
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};

test('duplicate preview cast advances to crew and its real arrival fills the retained waiting host', async () => {
  const calls: unknown[] = [], responses = [page('cast', ['preview', 'preview']), page('crew', ['writer', 'writer'], 'crew-2'), page('crew', ['next'])];
  const credits = new NativeTitleCredits({id: 'movie', revision, credits: [credit('preview')], totals: {cast: 1, crew: 2}, current: () => true,
    read: async (group, cursor) => {calls.push([group, cursor]); return responses.shift()!;}, changed() {}});
  const first = credits.getSnapshot(), waiting = {id: first.waitingId, name: '', role: '', department: '', provider: ''};
  let slots = reconcileRowSlots([], first.credits, item => item.id, false, {...first, waitingItem: waiting, requestNext() {}});
  const originalTail = slots.at(-1)!.focusId;
  await credits.next(first); let next = credits.getSnapshot();
  assert.deepEqual(next.credits.map(item => item.id), ['preview']);
  assert.deepEqual(next.fulfilled, {waitingId: first.waitingId});
  assert.notEqual(next.waitingId, first.waitingId);
  slots = reconcileRowSlots(slots, next.credits, item => item.id, true, {...next, waitingItem: {...waiting, id: next.waitingId}, requestNext() {}});
  assert.equal(slots.at(-1)!.focusId, originalTail);
  assert.equal(rowNeedsPage(slots.at(-1)!.position, next.credits.length, next), true, 'the new real crew request remains eligible while the same tail stays focused');
  await credits.next(next); next = credits.getSnapshot();
  slots = reconcileRowSlots(slots, next.credits, item => item.id, true, {...next, waitingItem: {...waiting, id: next.waitingId}, requestNext() {}});
  assert.equal(slots.find(slot => slot.item.id === 'writer')!.focusId, originalTail);
  await credits.next(next);
  assert.deepEqual(calls, [['cast', undefined], ['crew', undefined], ['crew', 'crew-2']]);
  assert.deepEqual(credits.getSnapshot().credits.map(item => item.id), ['preview', 'writer', 'next']);
  assert.equal(credits.getSnapshot().hasMore, false);
  await credits.next(credits.getSnapshot()); assert.equal(calls.length, 3);
});

test('a stale snapshot, repeated request and late cancelled response cannot append', async () => {
  const response = deferred<CreditPage>(); let calls = 0, signal: AbortSignal | undefined;
  const credits = new NativeTitleCredits({id: 'movie', revision, credits: [credit('seed')], current: () => true,
    read: async (_group, _cursor, value) => {calls++; signal = value; return response.promise;}, changed() {}});
  const old = credits.getSnapshot(), request = credits.next(old);
  await Promise.resolve(); await credits.next(old);
  assert.equal(calls, 1); assert.equal(credits.getSnapshot().loading, true);
  credits.cancel(); assert.equal(signal!.aborted, true); assert.equal(credits.getSnapshot().loading, false);
  response.resolve(page('cast', ['late'])); await request;
  assert.deepEqual(credits.getSnapshot().credits.map(item => item.id), ['seed']);
  credits.dispose(); await credits.next(credits.getSnapshot()); assert.equal(calls, 1);
});

test('changed revisions and repeated cursors cannot contaminate the loaded prefix', async () => {
  const responses = [page('cast', ['first'], 'cursor-a'), {...page('cast', ['wrong']), revision: {catalog: 8, viewer: 3}}, page('cast', ['loop'], 'cursor-a')];
  const calls: (string | undefined)[] = [];
  const credits = new NativeTitleCredits({id: 'movie', revision, credits: [credit('seed')], current: () => true,
    read: async (_group, cursor) => {calls.push(cursor); return responses.shift()!;}, changed() {}});
  await credits.next(credits.getSnapshot()); const valid = credits.getSnapshot(); await credits.next(valid);
  assert.equal((credits.getSnapshot().error as {code: string}).code, 'stale_continuation');
  assert.deepEqual(credits.getSnapshot().credits.map(item => item.id), ['seed', 'first']);
  assert.equal(credits.getSnapshot().waitingId, valid.waitingId);
  await credits.next(credits.getSnapshot()); assert.deepEqual(calls, [undefined, 'cursor-a', 'cursor-a']);
  assert.deepEqual(credits.getSnapshot().credits.map(item => item.id), ['seed', 'first']);
  assert.equal((credits.getSnapshot().error as {code: string}).code, 'stale_continuation');
});

test('the declared complete preview does not invent another page', async () => {
  const credits = new NativeTitleCredits({id: 'movie', revision, credits: [credit('cast'), credit('crew')], totals: {cast: 1, crew: 1},
    current: () => true, read: async () => {assert.fail('no read needed');}, changed() {}});
  assert.equal(credits.getSnapshot().hasMore, false); await credits.next(credits.getSnapshot());
});
