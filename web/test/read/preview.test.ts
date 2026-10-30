import {test} from 'node:test';
import assert from 'node:assert/strict';
import {ReaderController, sourceFromBlob, type ReaderDocument, type ReaderPageImage} from '@portico/reader-core';
import {loadReadPagePreview} from '../../src/screens/read/preview.ts';

const source = sourceFromBlob(new Blob([new Uint8Array([0])]), 'preview-lifetime-unit-file', 'sha256:6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d');
const png = new Uint8Array(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jC1sAAAAASUVORK5CYII=', 'base64'));
function fixture() {
  let current = true; const listeners = new Set<() => void>();
  const authority = {isCurrent: () => current, subscribe(fn: () => void) { listeners.add(fn); return () => { listeners.delete(fn); }; }};
  const document: ReaderDocument<object> = {id: source.id, revision: source.revision, format: 'pdf', metadata: {pageCount: 1}, toc: [],
    async mount() { return {async navigate() {}, async applyPreferences() {}, async resize() {}, getLocation: () => null, dispose() {}}; },
    async *search() {}, async page() { return {mime: 'image/png', bytes: png, width: 1, height: 1}; }, dispose() {}};
  const controller = new ReaderController({adapters: [{format: 'pdf', async open() { return document; }}], preferences: {}});
  return {controller, document, authority, listeners, retire() { current = false; for (const listener of [...listeners]) listener(); }};
}
const open = (controller: ReaderController<object>) => controller.open({source, format: 'pdf', container: {} as HTMLElement, viewport: {width: 1440, height: 900}});

test('a preview owns its Blob URL and authority retirement revokes it exactly with its lifetime', async t => {
  const f = fixture(); await open(f.controller); t.after(() => f.controller.close());
  const image = await loadReadPagePreview({controller: f.controller, index: 0, width: 300, authority: f.authority, signal: new AbortController().signal});
  t.after(image.dispose);
  assert.deepEqual(new Uint8Array(await (await fetch(image.url)).arrayBuffer()), png);
  f.retire(); assert.equal(f.listeners.size, 0);
  await assert.rejects(fetch(image.url)); image.dispose();
});

test('retirement cancels pending canonical preview I/O and discards a late result', async t => {
  const f = fixture(); await open(f.controller); t.after(() => f.controller.close());
  let resolve!: (image: ReaderPageImage) => void, active!: AbortSignal;
  f.document.page = async (_index, options) => { active = options.signal; return new Promise<ReaderPageImage>(r => { resolve = r; }); };
  const pending = loadReadPagePreview({controller: f.controller, index: 0, width: 300, authority: f.authority, signal: new AbortController().signal});
  f.retire(); assert.equal(active.aborted, true);
  await assert.rejects(pending, error => error instanceof Error && 'code' in error && error.code === 'aborted');
  resolve({mime: 'image/png', bytes: png, width: 1, height: 1});
  assert.equal(f.listeners.size, 0);
});

test('closing the current book revokes an already delivered preview without a second document', async t => {
  const f = fixture(); await open(f.controller); t.after(() => f.controller.close());
  const signal = new AbortController();
  const image = await loadReadPagePreview({controller: f.controller, index: 0, width: 300, authority: f.authority, signal: signal.signal});
  t.after(image.dispose); await f.controller.close();
  await assert.rejects(fetch(image.url)); assert.equal(f.listeners.size, 0);
});

test('preview ownership and parent cancellation work without modern AbortSignal helpers', async t => {
  const any = Object.getOwnPropertyDescriptor(AbortSignal, 'any'), throwing = Object.getOwnPropertyDescriptor(AbortSignal.prototype, 'throwIfAborted');
  Object.defineProperty(AbortSignal, 'any', {value: undefined, configurable: true});
  Object.defineProperty(AbortSignal.prototype, 'throwIfAborted', {value: undefined, configurable: true});
  t.after(() => {if (any) Object.defineProperty(AbortSignal, 'any', any); if (throwing) Object.defineProperty(AbortSignal.prototype, 'throwIfAborted', throwing);});
  const f = fixture(); await open(f.controller); t.after(() => f.controller.close());
  const parent = new AbortController();
  const image = await loadReadPagePreview({controller: f.controller, index: 0, width: 300, authority: f.authority, signal: parent.signal});
  t.after(image.dispose); parent.abort(); await assert.rejects(fetch(image.url)); assert.equal(f.listeners.size, 0);
  const aborted = new AbortController(); aborted.abort();
  await assert.rejects(loadReadPagePreview({controller: f.controller, index: 0, width: 300, authority: f.authority, signal: aborted.signal}));
  assert.equal(f.listeners.size, 0);
});
