import test from 'node:test';
import assert from 'node:assert/strict';
import {SelectionActionController} from '../src/selection-actions-controller.ts';
import type {SelectionActionInput, SelectionActionsApi} from '../src/selection-actions.ts';
import {routePath} from '../../contracts/src/route-paths.ts';

const basis = 'a'.repeat(64);
const input: SelectionActionInput = {action: 'delete', selector: {query: {libraryId: 'library', pivot: 'movies', selectionBasis: basis}}, expected: {catalogRevision: 'browse_' + 'b'.repeat(64)}, options: {deleteFiles: true}};
const preview = {kind: 'selection-action-preview', preparationId: 'prep', action: 'delete', basis, total: 1000, files: 1000, bytes: 1_000_000,
  bytesKnown: true, confirmation: '1000', confirmationKind: 'count', expiresAt: '2030-01-01T00:00:00Z', sample: []};
const complete = {operationId: 'operation', action: 'delete', state: 'complete', total: 1000, admitted: 1000, done: 1000, failed: 0,
  bytesDeleted: 1_000_000, filesDeleted: 1000, retryAfterMs: 1000};
function source() {
  let current = true; const listeners = new Set<() => void>(), lifetime = new AbortController();
  return {isCurrent: () => current, subscribe: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};}, signal: lifetime.signal,
    retire: () => {current = false; for (const listener of [...listeners]) listener();}, listeners, lifetime};
}
function apiWith(handler: (path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<unknown> | unknown) {
  const requests: {path: string; method?: string; body?: unknown; signal?: AbortSignal}[] = [];
  const api: SelectionActionsApi = {async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) {
    requests.push({path, method, body, signal}); return await handler(path, method, body, signal) as T;
  }};
  return {api, requests};
}
const flush = async () => {for (let n = 0; n < 20; n++) await Promise.resolve();};
test('controller preflight refuses a retired source without a transport or operation id', async () => {
  const mock = apiWith(() => {throw new Error('must not request');}), live = source(); live.retire();
  let ids = 0;
  const controller = new SelectionActionController({api: mock.api, requestId: async () => {ids++;return 'operation';}});
  await controller.open(input, live); controller.setConfirmation('1000'); await controller.confirm(); await controller.retry(); await controller.cancel();
  assert.equal(controller.getSnapshot().phase, 'retired'); assert.equal(mock.requests.length, 0); assert.equal(ids, 0);
  controller.dispose();
});
test('controller preflight rejects unsupported semantics and cannot reopen after disposal', async () => {
  const mock = apiWith(() => {throw new Error('must not request');}), controller = new SelectionActionController({api: mock.api, requestId: async () => 'operation'});
  await assert.rejects(controller.open({...input, selector: {query: {...input.selector.query, selectionBasis: undefined}}} as never, source()));
  controller.dispose(); await assert.rejects(controller.open(input, source())); assert.equal(mock.requests.length, 0);
});
test('controller exact confirmation freezes one real body and replays it after lost 202 even after preview expiry', async () => {
  let now = Date.parse('2029-01-01T00:00:00Z'), ids = 0, posts = 0;
  const mock = apiWith((_path, method, body) => {
    if (method === 'POST' && body && 'selector' in (body as object)) return preview;
    if (method === 'POST') {if (++posts === 1) throw new Error('lost 202'); return complete;}
    throw new Error('unexpected request');
  });
  const controller = new SelectionActionController({api: mock.api, requestId: async () => {ids++; return 'operation';}, now: () => now});
  const live = source(); await controller.open(input, live);
  controller.setConfirmation('1,000'); await controller.confirm();
  assert.equal(mock.requests.length, 1); assert.equal(ids, 0);
  controller.setConfirmation('1000'); await controller.confirm();
  assert.equal(controller.getSnapshot().phase, 'uncertain');
  now = Date.parse('2031-01-01T00:00:00Z');
  await controller.retry();
  assert.equal(ids, 1); assert.equal(controller.getSnapshot().phase, 'result');
  assert.deepEqual(mock.requests[1].body, {operationId: 'operation', preparationId: 'prep', confirmation: '1000'});
  assert.deepEqual(mock.requests[1].body, mock.requests[2].body);
  controller.dispose(); assert.equal(live.listeners.size, 0);
});
test('controller bounded pending/progress polling uses actual parser state and cancellation status', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let prepGets = 0;
  const mock = apiWith((path, method, body) => {
    if (method === 'POST' && body && 'selector' in (body as object)) return {kind: 'selection-action-preparing', preparationId: 'prep', retryAfterMs: 0};
    if (path === routePath('get_selection_action_preparation', {id: 'library', preparationId: 'prep'})) {prepGets++; return preview;}
    if (method === 'POST') return {...complete, state: 'capturing', admitted: 20, done: 0, bytesDeleted: 0, filesDeleted: 0};
    if (method === 'DELETE') return {...complete, state: 'partial', done: 20, bytesDeleted: 20, filesDeleted: 20, errorCode: 'cancelled'};
    throw new Error('unexpected poll');
  });
  const controller = new SelectionActionController({api: mock.api, requestId: async () => 'operation'});
  const pending = controller.open(input, source()); await flush();
  assert.equal(controller.getSnapshot().phase, 'preparing');
  t.mock.timers.tick(249); await flush(); assert.equal(prepGets, 0);
  t.mock.timers.tick(1); await pending; assert.equal(prepGets, 1);
  controller.setConfirmation('1000'); const working = controller.confirm(); await flush();
  assert.equal(controller.getSnapshot().job?.admitted, 20); assert.equal(controller.getSnapshot().job?.done, 0);
  await controller.cancel(); await working;
  assert.equal(controller.getSnapshot().phase, 'result'); assert.equal(controller.getSnapshot().job?.state, 'partial');
  assert.equal(controller.getSnapshot().job?.done, 20);
  controller.dispose();
});
test('controller source retirement aborts actual request and releases a late preparation without exposing it', async () => {
  let resolve!: (value: unknown) => void;
  const mock = apiWith((_path, method) => method === 'POST' ? new Promise(r => {resolve = r;}) : undefined);
  const controller = new SelectionActionController({api: mock.api, requestId: async () => 'operation'}), live = source();
  const pending = controller.open(input, live); await flush(); live.retire();
  assert.equal(mock.requests[0].signal?.aborted, true); assert.equal(controller.getSnapshot().phase, 'retired');
  resolve(preview); await pending; await flush();
  assert.equal(controller.getSnapshot().preparation, null); assert.equal(mock.requests[1].method, 'DELETE');
  assert.equal(live.listeners.size, 0); controller.dispose();
});
test('controller disposal detaches a possibly accepted action without implicit cancellation', async () => {
  let resolve!: (value: unknown) => void;
  const mock = apiWith((_path, _method, body) => body && 'selector' in (body as object) ? preview : new Promise(r => {resolve = r;}));
  const controller = new SelectionActionController({api: mock.api, requestId: async () => 'operation'}), live = source();
  await controller.open(input, live); controller.setConfirmation('1000');
  const pending = controller.confirm(); await flush(); controller.dispose(); resolve(complete); await pending;
  assert.equal(mock.requests.length, 2); assert.ok(mock.requests.every(r => r.method === 'POST'));
  assert.equal(controller.getSnapshot().job, null); assert.equal(live.listeners.size, 0);
  const denied = apiWith((_path, _method, body) => {
    if (body && 'selector' in (body as object)) return preview;
    throw Object.assign(new Error('revoked'), {status: 403, code: 'not_permitted'});
  });
  const revoked = new SelectionActionController({api: denied.api, requestId: async () => 'operation'});
  await revoked.open(input, source()); revoked.setConfirmation('1000'); await revoked.confirm();
  assert.equal(revoked.getSnapshot().phase, 'retired'); assert.equal(revoked.getSnapshot().preparation, null);
  assert.equal(revoked.getSnapshot().job, null); assert.equal(denied.requests.length, 2); revoked.dispose();
});
test('controller refuses foreign basis/action/total and expired ready descriptors before confirmation', async () => {
  for (const bad of [{...preview, basis: 'b'.repeat(64)}, {...preview, action: 'download'}, {...preview, expiresAt: '2000-01-01T00:00:00Z'}]) {
    const mock = apiWith(() => bad), controller = new SelectionActionController({api: mock.api, requestId: async () => {throw new Error('must not choose id');}});
    await controller.open(input, source()); await controller.confirm(); assert.equal(controller.getSnapshot().phase, 'error');
    assert.equal(mock.requests.length, 1); controller.dispose();
  }
  const mock = apiWith(() => preview), controller = new SelectionActionController({api: mock.api, requestId: async () => {throw new Error('must not choose id');}});
  await controller.open(input, {...source(), expectedTotal: 999});
  assert.equal(controller.getSnapshot().phase, 'error'); assert.equal(controller.getSnapshot().job, null);
  controller.dispose();
});
