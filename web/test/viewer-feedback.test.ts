import test from 'node:test';
import assert from 'node:assert/strict';
import {ViewerFeedback, type FeedbackTarget} from '../src/screens/account/feedback-controller.ts';
import {feedbackAvailable, installFeedback, openFeedback, subscribeFeedback} from '../src/screens/account/feedback.ts';
import type {QueuePlaybackFence} from '../../packages/player-core/src/index.ts';

const viewer = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
const capabilities = {revision: 'e1.0', canSubmit: true, kinds: [
  {id: 'general', label: 'General', categories: [{id: 'other', label: 'Something else', description: 'Actual description.', wantsPlaybackSession: false, wantsItem: false}]},
  {id: 'playback', label: 'Playback', categories: [{id: 'buffering', label: 'Buffering', description: 'It keeps pausing.', wantsPlaybackSession: true, wantsItem: true}]},
], maxMessageLength: 2000, minMessageLength: 8, diagnosticsSupported: true, diagnosticsOptional: true, duplicateWindowHours: 24,
  retentionDays: 180, statuses: ['open'], diagnosticsDecisions: ['attached'], perProfileHourlyLimit: 10, reporterName: 'Actual person', reporterAuthority: 'local'};
const at = (value: number) => new Date(value).toISOString();
const report = {id: 'report', cursor: '1', revision: 1, status: 'open', kind: 'playback', category: 'buffering', message: 'A genuine report.',
  createdAt: at(1), updatedAt: at(1), expiresAt: at(9), reporter: {name: 'Actual person', authority: 'local', role: 'member', self: true},
  diagnostics: {decision: 'attached'}, duplicates: 0, thread: []};
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
function fixture() {
  const calls: {path: string; body?: unknown; signal?: AbortSignal}[] = [];
  let caps = capabilities, current = true, fail = false, duplicate = false, pending: Promise<unknown> | undefined, capPending: Promise<unknown> | undefined;
  const api = {request: async <T,>(path: string, _method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push({path, body, signal});
    if (path === '/v1/feedback/capabilities') return capPending ? await capPending as T : {data: caps} as T;
    if (pending) return await pending as T;
    if (fail) throw new TypeError('Failed to fetch');
    return {data: {report, duplicate, created: !duplicate}} as T;
  }};
  const controller = new ViewerFeedback(api, viewer, () => current, () => 'actual-operation');
  return {controller, calls, setCaps: (value: typeof capabilities) => {caps = value;}, fail: (value: boolean) => {fail = value;},
    duplicate: () => {duplicate = true;}, retire: () => {current = false; controller.dispose();}, pending: (value: Promise<unknown>) => {pending = value;}, capPending: (value?: Promise<unknown>) => {capPending = value;}};
}
async function ready(controller: ViewerFeedback) {
  if (controller.getSnapshot().service.phase !== 'loading') return;
  await new Promise<void>(resolve => {const off = controller.subscribe(() => {if (controller.getSnapshot().service.phase !== 'loading') {off();resolve();}});});
}
function playback() {
  const listeners = new Set<() => void>(); let current = true;
  const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope: {...viewer, authority: 'local', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
    sessionId: 'actual-session', itemId: 'actual-item', sourceId: 'source', entryId: 'entry', generation: 3};
  const target: FeedbackTarget = {item: {id: fence.itemId, title: 'The actual title'}, playback: {fence, isCurrent: () => current,
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}}};
  return {target, retire: () => {current = false;for (const listener of listeners) listener();}, listeners};
}
test('construction/imports do no I/O; actual capability gates both action and gesture', async () => {
  const f = fixture(); assert.equal(f.calls.length, 0); assert.equal(f.controller.open(), false);
  f.controller.initialize(); await ready(f.controller);
  assert.equal(f.controller.available(), true); assert.equal(f.controller.open(), true); assert.equal(f.controller.open(), false);
  await ready(f.controller); f.retire(); assert.equal(f.controller.available(), false); assert.equal(f.controller.open(), false);
});
test('server-denied capability never advertises a page or fabricates a permitted action', async () => {
  const f = fixture(); f.setCaps({...capabilities, canSubmit: false, submitBlockedReason: 'Actual server refusal.'} as typeof capabilities);
  f.controller.initialize(); await ready(f.controller);
  assert.equal(f.controller.available(), false); assert.equal(f.controller.open(), false);
  assert.equal(f.controller.getSnapshot().service.capabilities?.submitBlockedReason, 'Actual server refusal.'); f.retire();
});
test('closing a capability refresh preserves the last confirmed permission and cannot strand the real action', async () => {
  const f = fixture(), pending = deferred<unknown>(); f.controller.initialize(); await ready(f.controller);
  f.capPending(pending.promise); f.controller.open();
  assert.equal(f.controller.getSnapshot().service.phase, 'loading');
  f.controller.close(); assert.equal(f.controller.available(), true);
  pending.resolve({data: {...capabilities, canSubmit: false}}); await Promise.resolve(); await Promise.resolve();
  assert.equal(f.controller.available(), true, 'a canceled old answer cannot overwrite the confirmed permission');
  f.capPending(); assert.equal(f.controller.open(), true); await ready(f.controller); f.retire();
});
test('general feedback starts on first real kind/category and receives no playback context', async () => {
  const f = fixture(); f.controller.initialize(); await ready(f.controller); f.controller.open(); await ready(f.controller);
  assert.equal(f.controller.getSnapshot().draft.kind, 'general'); assert.equal(f.controller.getSnapshot().draft.category, 'other');
  f.controller.change({message: 'What actually happened.'}); await f.controller.submit();
  assert.deepEqual(f.calls.at(-1)?.body, {operationId: 'actual-operation', kind: 'general', category: 'other', message: 'What actually happened.', itemId: '', playbackSessionId: '', attachDiagnostics: true});
  f.retire();
});
test('genuine player target retains the original full fence and starts on the actual playback category', async () => {
  const f = fixture(), p = playback(); f.controller.initialize(); await ready(f.controller);
  assert.equal(f.controller.open(p.target), true); await ready(f.controller);
  assert.equal(f.controller.getSnapshot().target, p.target); assert.equal(f.controller.getSnapshot().target?.playback?.fence, p.target.playback?.fence);
  assert.equal(f.controller.getSnapshot().draft.kind, 'playback');
  f.controller.change({message: 'The original playback failed.'}); await f.controller.submit();
  const body = f.calls.at(-1)?.body as Record<string, unknown>;
  assert.equal(body.itemId, 'actual-item'); assert.equal(body.playbackSessionId, 'actual-session');
  f.retire(); assert.equal(p.listeners.size, 0);
});
test('stale/foreign target cannot open or attach a made-up session', async () => {
  const f = fixture(), p = playback(); f.controller.initialize(); await ready(f.controller);
  assert.equal(f.controller.open({...p.target, item: {id: 'other-item', title: 'Different'}}), false);
  const fence = p.target.playback!.fence;
  assert.equal(f.controller.open({...p.target, playback: {...p.target.playback!, fence: {...fence, playback: {...fence.playback, scope: {...fence.playback.scope, accountId: 'foreign'}}}}}), false);
  p.retire(); assert.equal(f.controller.open(p.target), false); f.retire();
});
test('failed send preserves exact draft and retries the same actual operation, without extra fields', async () => {
  const f = fixture(); f.controller.initialize(); await ready(f.controller); f.controller.open(); await ready(f.controller);
  f.controller.change({message: 'A message that must survive failure.'}); const draft = f.controller.getSnapshot().draft;
  f.fail(true); assert.equal(await f.controller.submit(), false);
  assert.equal(f.controller.getSnapshot().draft, draft); assert.equal(f.controller.getSnapshot().service.phase, 'ready');
  f.fail(false); assert.equal(await f.controller.submit(), true);
  const bodies = f.calls.filter(call => call.body).map(call => call.body);
  assert.deepEqual(bodies[0], bodies[1]); f.retire();
});
test('source retirement aborts the in-flight request, clears draft/context, and ignores the late result', async () => {
  const f = fixture(), p = playback(), pending = deferred<unknown>(); f.controller.initialize(); await ready(f.controller);
  f.controller.open(p.target); await ready(f.controller); f.controller.change({message: 'Private draft.'});
  f.pending(pending.promise); const work = f.controller.submit(); p.retire();
  assert.equal(f.calls.at(-1)?.signal?.aborted, true); assert.equal(f.controller.getSnapshot().open, false);
  assert.equal(f.controller.getSnapshot().draft.message, ''); assert.equal(f.controller.getSnapshot().target, undefined);
  pending.resolve({data: {report, duplicate: false, created: true}}); assert.equal(await work, false);
  assert.equal(f.controller.getSnapshot().open, false); f.retire();
});
test('mandatory diagnostics stay checked and duplicate response uses the actual sent state', async () => {
  const f = fixture(), p = playback(); f.setCaps({...capabilities, diagnosticsOptional: false}); f.controller.initialize(); await ready(f.controller);
  f.controller.open(p.target); await ready(f.controller); f.controller.change({attachDiagnostics: false, message: 'A duplicate actual report.'});
  assert.equal(f.controller.getSnapshot().draft.attachDiagnostics, true); f.duplicate(); await f.controller.submit();
  assert.equal(f.controller.getSnapshot().service.result?.duplicate, true); f.retire();
});
test('changing kind preselects its genuine first category and unknown option values cannot enter the draft', async () => {
  const f = fixture(); f.controller.initialize(); await ready(f.controller); f.controller.open(); await ready(f.controller);
  f.controller.change({kind: 'playback'}); assert.equal(f.controller.getSnapshot().draft.category, 'buffering');
  const before = f.controller.getSnapshot().draft;
  f.controller.change({category: 'not-offered'}); assert.equal(f.controller.getSnapshot().draft, before);
  f.controller.change({kind: 'not-offered'}); assert.equal(f.controller.getSnapshot().draft, before); f.retire();
});
test('singleton action bridge installs only the live host and old cleanup cannot remove a successor', async () => {
  const first = fixture(), second = fixture(); let changes = 0; const off = subscribeFeedback(() => {changes++;});
  const removeFirst = installFeedback(first.controller); first.controller.initialize(); await ready(first.controller); assert.equal(feedbackAvailable(), true);
  const removeSecond = installFeedback(second.controller); removeFirst(); assert.equal(feedbackAvailable(), false);
  second.controller.initialize(); await ready(second.controller); openFeedback(); await ready(second.controller);
  assert.equal(second.controller.getSnapshot().open, true); assert.equal(first.controller.getSnapshot().open, false);
  first.retire(); second.retire(); removeSecond(); assert.equal(feedbackAvailable(), false); assert.ok(changes > 0); off();
});
