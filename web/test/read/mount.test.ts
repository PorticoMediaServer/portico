import {test, type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {ReaderController, sourceFromBlob, type ReaderAdapter, type ReaderDocument, type ReaderMount, type ReaderView} from '@portico/reader-core';
import {mountReaderSurface} from '../../src/screens/read/mount.ts';

const source = sourceFromBlob(new Blob([new Uint8Array([0])]), 'host-lifetime-unit-file', 'sha256:6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d');
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return {resolve, promise}; }
function environment(t: TestContext) {
  const original = {ResizeObserver: globalThis.ResizeObserver, requestAnimationFrame: globalThis.requestAnimationFrame, cancelAnimationFrame: globalThis.cancelAnimationFrame};
  const frames = new Map<number, FrameRequestCallback>(); let sequence = 0;
  class Observer {
    static all: Observer[] = []; disconnected = false;
    constructor(private callback: ResizeObserverCallback) { Observer.all.push(this); }
    observe() {} unobserve() {} disconnect() { this.disconnected = true; }
    fire() { if (!this.disconnected) this.callback([], this as unknown as ResizeObserver); }
  }
  globalThis.ResizeObserver = Observer as unknown as typeof ResizeObserver;
  globalThis.requestAnimationFrame = callback => { frames.set(++sequence, callback); return sequence; };
  globalThis.cancelAnimationFrame = id => { frames.delete(id); };
  t.after(() => Object.assign(globalThis, original));
  let current = true; const listeners = new Set<() => void>();
  const authority = {isCurrent: () => current, subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }};
  return {authority, listeners, frames, observers: Observer.all,
    retire() { current = false; for (const listener of [...listeners]) listener(); },
    resize() { for (const observer of Observer.all) observer.fire(); },
    flush() { const pending = [...frames.values()]; frames.clear(); for (const callback of pending) callback(0); }};
}
function fixture() {
  let mounted = 0, disposed = 0; const resized: {width: number; height: number}[] = [];
  let mount!: ReaderMount<object>;
  const view: ReaderView<object> = {async navigate() {}, async applyPreferences() {}, async resize(value) { resized.push(value); }, getLocation: () => null, dispose() { disposed++; }};
  const document: ReaderDocument<object> = {id: source.id, revision: source.revision, format: 'epub', metadata: {}, toc: [],
    async mount(options) { mount = options; mounted++; return view; }, async *search() {}, dispose() {}};
  const adapter: ReaderAdapter<object> = {format: 'epub', async open() { return document; }};
  const controller = new ReaderController({adapters: [adapter], preferences: {}});
  return {controller, adapter, document, view, resized, get mounted() { return mounted; }, get disposed() { return disposed; }, get mount() { return mount; }};
}

test('the actual mount waits for visible size and resizes only after a changed DOM extent', async t => {
  const env = environment(t), f = fixture(), dimensions = {clientWidth: 0, clientHeight: 0}, errors: string[] = [];
  const mounted = mountReaderSurface({controller: f.controller, source, format: 'epub', authority: env.authority, container: dimensions as HTMLElement, reportError: code => errors.push(code)});
  await tick(); assert.equal(f.mounted, 0);
  dimensions.clientWidth = 1180; dimensions.clientHeight = 820; env.resize(); await mounted.ready;
  assert.deepEqual(f.mount.viewport, {width: 1180, height: 820});
  env.flush(); await tick(); assert.equal(f.resized.length, 0);
  dimensions.clientWidth = 820; dimensions.clientHeight = 1180; env.resize(); env.flush(); await tick();
  assert.deepEqual(f.resized, [{width: 820, height: 1180}]);
  env.resize(); env.flush(); await tick(); assert.equal(f.resized.length, 1);
  await mounted.close(); assert.equal(f.controller.getSnapshot().status, 'closed');
  assert.equal(env.listeners.size, 0); assert.equal(env.frames.size, 0); assert.ok(env.observers.every(value => value.disconnected)); assert.deepEqual(errors, []);
});

test('retiring a hidden surface releases its wait without starting an adapter', async t => {
  const env = environment(t), f = fixture();
  const mounted = mountReaderSurface({controller: f.controller, source, format: 'epub', authority: env.authority, container: {clientWidth: 0, clientHeight: 0} as HTMLElement, reportError() { assert.fail('retirement is silent'); }});
  await tick(); env.retire(); await mounted.ready;
  assert.equal(f.mounted, 0); assert.equal(env.listeners.size, 0); assert.ok(env.observers.every(value => value.disconnected));
});

test('the host forwards authoritative catalogue omission into the actual core and renderer', async t => {
  const env = environment(t), f = fixture();
  Object.defineProperty(f.document, 'metadata', {value: {title: 'Embedded unit title', author: 'Embedded unit author', language: 'en', pageCount: 12}});
  const mounted = mountReaderSurface({controller: f.controller, source, format: 'epub', metadata: {title: 'Owner unit title'}, authority: env.authority,
    container: {clientWidth: 1440, clientHeight: 900} as HTMLElement, reportError() { assert.fail('unexpected metadata error'); }});
  await mounted.ready;
  assert.equal(f.mount.metadata?.title, 'Owner unit title');
  assert.equal(f.mount.metadata?.author, undefined); assert.equal(f.mount.metadata?.language, undefined);
  assert.equal(f.controller.getSnapshot().metadata?.author, undefined);
  assert.equal(f.controller.getSnapshot().metadata?.pageCount, 12);
  await mounted.close();
});

test('authority retirement aborts a pending real controller open and discards its late document', async t => {
  const env = environment(t), f = fixture(), pending = deferred<ReaderDocument<object>>(); let signal!: AbortSignal;
  f.adapter.open = async options => { signal = options.signal; return pending.promise; };
  const mounted = mountReaderSurface({controller: f.controller, source, format: 'epub', authority: env.authority, container: {clientWidth: 1440, clientHeight: 900} as HTMLElement, reportError() { assert.fail('retirement is silent'); }});
  await tick(); env.retire(); assert.equal(signal.aborted, true);
  pending.resolve(f.document); await mounted.ready;
  assert.equal(f.mounted, 0); assert.equal(f.controller.getSnapshot().status, 'closed'); assert.equal(env.listeners.size, 0);
});

test('StrictMode-style reconnect waits for disposal and an old cleanup cannot close its successor', async t => {
  const env = environment(t), f = fixture(), oldDisposal = deferred<void>();
  const options = {controller: f.controller, source, format: 'epub' as const, authority: env.authority, container: {clientWidth: 1440, clientHeight: 900} as HTMLElement, reportError() { assert.fail('unexpected host error'); }};
  const first = mountReaderSurface(options); await first.ready;
  f.view.dispose = () => oldDisposal.promise;
  const closing = first.close(); const second = mountReaderSurface({...options, settled: closing});
  await tick(); assert.equal(f.mounted, 1);
  oldDisposal.resolve(); await second.ready; assert.equal(f.mounted, 2);
  await first.close(); assert.equal(f.controller.getSnapshot().status, 'ready');
  await second.close(); assert.equal(f.controller.getSnapshot().status, 'closed');
});

test('an abandoned reconnect preserves the outstanding disposal fence for a third mount', async t => {
  const env = environment(t), f = fixture(), disposal = deferred<void>();
  const options = {controller: f.controller, source, format: 'epub' as const, authority: env.authority, container: {clientWidth: 1440, clientHeight: 900} as HTMLElement, reportError() { assert.fail('unexpected host error'); }};
  const first = mountReaderSurface(options); await first.ready;
  f.view.dispose = () => disposal.promise;
  const firstClose = first.close();
  const abandoned = mountReaderSurface({...options, settled: firstClose});
  const abandonedClose = abandoned.close();
  const third = mountReaderSurface({...options, settled: abandonedClose});
  await tick(); assert.equal(f.mounted, 1);
  disposal.resolve(); await abandoned.ready; await third.ready;
  assert.equal(f.mounted, 2); await first.close(); await abandoned.close();
  assert.equal(f.controller.getSnapshot().status, 'ready');
  await third.close(); assert.equal(f.controller.getSnapshot().status, 'closed');
});

test('actual publication session cancellation closes a cached reader and releases a hidden-size wait', async t => {
  const env = environment(t), f = fixture(), session = new AbortController();
  const options = {controller: f.controller, source, format: 'epub' as const, authority: env.authority, reportError() {assert.fail('session retirement is silent');}};
  const mounted = mountReaderSurface({...options, signal: session.signal, container: {clientWidth: 390, clientHeight: 844} as HTMLElement});
  await mounted.ready; assert.equal(f.controller.getSnapshot().status, 'ready');
  session.abort(); await mounted.close();
  assert.equal(f.controller.getSnapshot().status, 'closed'); assert.equal(f.disposed, 1); assert.equal(env.listeners.size, 0);
  const hidden = new AbortController();
  const waiting = mountReaderSurface({...options, signal: hidden.signal, container: {clientWidth: 0, clientHeight: 0} as HTMLElement});
  await tick(); hidden.abort(); await waiting.ready; await waiting.close();
  assert.equal(f.mounted, 1); assert.equal(env.listeners.size, 0); assert.ok(env.observers.every(observer => observer.disconnected));
  const aborted = mountReaderSurface({...options, signal: hidden.signal, container: {clientWidth: 390, clientHeight: 844} as HTMLElement});
  await aborted.ready; await aborted.close(); assert.equal(f.mounted, 1);
});

test('an old publication signal cannot close a replacement mounted in the same controller', async t => {
  const env = environment(t), f = fixture(), old = new AbortController();
  const mounted = mountReaderSurface({controller: f.controller, source, format: 'epub', signal: old.signal, authority: env.authority,
    container: {clientWidth: 390, clientHeight: 844} as HTMLElement, reportError() {assert.fail('replacement retirement is silent');}});
  await mounted.ready;
  await f.controller.open({source, format: 'epub', container: {} as HTMLElement, viewport: {width: 820, height: 1180}});
  const replacement = f.controller.getSnapshot(); assert.equal(replacement.status, 'ready');
  old.abort(); await mounted.close(); assert.strictEqual(f.controller.getSnapshot(), replacement);
  assert.equal(env.listeners.size, 0); await f.controller.close();
});
