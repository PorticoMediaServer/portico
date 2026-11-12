import test from 'node:test';
import assert from 'node:assert/strict';
import {ViewerFeedback, type FeedbackTarget} from '../src/screens/account/feedback-controller.ts';
import {feedbackAvailable, installFeedback, openFeedback, subscribeFeedback} from '../src/screens/account/feedback.ts';
import type {QueuePlaybackFence} from '../../packages/player-core/src/index.ts';

const viewer = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
/** A stand-in for POST /v1/reports. */
function fixture() {
  const calls: {path: string; headers?: Readonly<Record<string, string>>; body?: any; signal?: AbortSignal}[] = [];
  let current = true, fail = false, pending: Promise<unknown> | undefined;
  const api = {requestRaw: async (path: string, _method: string, options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>) => {
    calls.push({path, headers: options?.headers, body: options?.body, signal: options?.signal});
    if (pending) await pending;
    if (fail) throw new TypeError('Failed to fetch');
    return {status: 200, body: {id: 'rpt_1', kind: 'problem', status: 'open'}};
  }};
  const controller = new ViewerFeedback(api, viewer, () => current, () => 'actual-operation');
  return {controller, calls, fail: (value: boolean) => {fail = value;}, retire: () => {current = false; controller.dispose();}, pending: (value: Promise<unknown>) => {pending = value;}};
}
function playback() {
  const listeners = new Set<() => void>(); let current = true;
  const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope: {...viewer, authority: 'local', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
    sessionId: 'actual-session', itemId: 'actual-item', sourceId: 'source', entryId: 'entry', generation: 3};
  const target: FeedbackTarget = {item: {id: fence.itemId, title: 'The actual title'}, playback: {fence, isCurrent: () => current,
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}}};
  return {target, retire: () => {current = false;for (const listener of listeners) listener();}, listeners};
}
test('construction/imports do no I/O; anyone signed in can open it once', () => {
  const f = fixture(); assert.equal(f.calls.length, 0); assert.equal(f.controller.open(), false, 'not before the host initialises it');
  f.controller.initialize();
  assert.equal(f.controller.available(), true); assert.equal(f.controller.open(), true); assert.equal(f.controller.open(), false);
  assert.equal(f.calls.length, 0, 'opening loads nothing'); f.retire(); assert.equal(f.controller.available(), false); assert.equal(f.controller.open(), false);
});
test('a report opened outside the player carries no title or playback', async () => {
  const f = fixture(); f.controller.initialize(); f.controller.open();
  assert.equal(f.controller.getSnapshot().draft.kind, 'playback'); assert.equal(f.controller.getSnapshot().draft.category, 'wont-start');
  f.controller.change({kind: 'apps'}); f.controller.change({message: 'What actually happened.'}); assert.equal(await f.controller.submit(), true);
  const sent = f.calls.at(-1)!;
  assert.equal(sent.path, '/v1/reports'); assert.equal(sent.headers?.['Idempotency-Key'], 'actual-operation');
  const {online: _online, ...context} = sent.body.context;
  assert.deepEqual({...sent.body, context}, {kind: 'problem', message: 'What actually happened.', context: {about: 'apps/crash'}});
  f.retire();
});
test('genuine player target retains the original full fence and sends its title and playback', async () => {
  const f = fixture(), p = playback(); f.controller.initialize();
  assert.equal(f.controller.open(p.target), true);
  assert.equal(f.controller.getSnapshot().target, p.target); assert.equal(f.controller.getSnapshot().target?.playback?.fence, p.target.playback?.fence);
  assert.equal(f.controller.getSnapshot().draft.kind, 'playback');
  f.controller.change({message: 'The original playback failed.'}); await f.controller.submit();
  const context = f.calls.at(-1)?.body.context;
  assert.deepEqual(context.item, {id: 'actual-item', title: 'The actual title'}); assert.equal(context.playbackSessionId, 'actual-session');
  f.retire(); assert.equal(p.listeners.size, 0);
});
test('stale/foreign target cannot open or attach a made-up session', async () => {
  const f = fixture(), p = playback(); f.controller.initialize();
  assert.equal(f.controller.open({...p.target, item: {id: 'other-item', title: 'Different'}}), false);
  const fence = p.target.playback!.fence;
  assert.equal(f.controller.open({...p.target, playback: {...p.target.playback!, fence: {...fence, playback: {...fence.playback, scope: {...fence.playback.scope, accountId: 'foreign'}}}}}), false);
  p.retire(); assert.equal(f.controller.open(p.target), false); f.retire();
});
test('failed send preserves exact draft and retries the same actual report', async () => {
  const f = fixture(); f.controller.initialize(); f.controller.open();
  f.controller.change({message: 'A message that must survive failure.'}); const draft = f.controller.getSnapshot().draft;
  f.fail(true); assert.equal(await f.controller.submit(), false);
  assert.equal(f.controller.getSnapshot().draft, draft); assert.equal(f.controller.getSnapshot().service.phase, 'ready');
  assert.ok(f.controller.getSnapshot().service.error);
  f.fail(false); assert.equal(await f.controller.submit(), true); assert.equal(f.controller.getSnapshot().service.phase, 'sent');
  assert.deepEqual(f.calls[0]!.body, f.calls[1]!.body); assert.equal(f.calls[0]!.headers?.['Idempotency-Key'], f.calls[1]!.headers?.['Idempotency-Key']); f.retire();
});
test('source retirement aborts the in-flight request, clears draft/context, and ignores the late result', async () => {
  const f = fixture(), p = playback(), pending = deferred<unknown>(); f.controller.initialize();
  f.controller.open(p.target); f.controller.change({message: 'Private draft.'});
  f.pending(pending.promise); const work = f.controller.submit(); await Promise.resolve(); p.retire();
  assert.equal(f.calls.at(-1)?.signal?.aborted, true); assert.equal(f.controller.getSnapshot().open, false);
  assert.equal(f.controller.getSnapshot().draft.message, ''); assert.equal(f.controller.getSnapshot().target, undefined);
  pending.resolve(undefined); assert.equal(await work, false);
  assert.equal(f.controller.getSnapshot().open, false); f.retire();
});
test('changing kind preselects its first category and unknown option values cannot enter the draft', () => {
  const f = fixture(); f.controller.initialize(); f.controller.open();
  f.controller.change({kind: 'library'}); assert.equal(f.controller.getSnapshot().draft.category, 'missing-item');
  const before = f.controller.getSnapshot().draft;
  f.controller.change({category: 'not-offered'}); assert.equal(f.controller.getSnapshot().draft, before);
  f.controller.change({kind: 'not-offered'}); assert.equal(f.controller.getSnapshot().draft, before); f.retire();
});
test('singleton action bridge installs only the live host and old cleanup cannot remove a successor', () => {
  const first = fixture(), second = fixture(); let changes = 0; const off = subscribeFeedback(() => {changes++;});
  const removeFirst = installFeedback(first.controller); first.controller.initialize(); assert.equal(feedbackAvailable(), true);
  const removeSecond = installFeedback(second.controller); removeFirst(); assert.equal(feedbackAvailable(), false);
  second.controller.initialize(); openFeedback();
  assert.equal(second.controller.getSnapshot().open, true); assert.equal(first.controller.getSnapshot().open, false);
  first.retire(); second.retire(); removeSecond(); assert.equal(feedbackAvailable(), false); assert.ok(changes > 0); off();
});
