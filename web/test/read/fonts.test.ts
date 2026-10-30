import {test, type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {loadReadFonts} from '../../src/screens/read/fonts.ts';

async function environment(t: TestContext) {
  const literata = await readFile(new URL('../../../packages/reader-core/assets/Literata[opsz,wght].ttf', import.meta.url));
  const manrope = await readFile(new URL('../../src/kit/fonts/manrope-var-latin.woff2', import.meta.url));
  const original = {fetch: globalThis.fetch, FontFace: globalThis.FontFace};
  const requests: {url: string; credentials: RequestCredentials | undefined}[] = [], faces = new Set<FontFace>();
  let finish: (() => void) | undefined, hold = false;
  class Face {
    constructor(readonly family: string, readonly bytes: ArrayBuffer) {}
    load() { return hold ? new Promise<Face>(resolve => { finish = () => resolve(this); }) : Promise.resolve(this); }
  }
  globalThis.FontFace = Face as unknown as typeof FontFace;
  globalThis.fetch = async (input, options) => {
    const url = String(input); requests.push({url, credentials: options?.credentials});
    if (url.endsWith('Literata[opsz,wght].ttf')) return new Response(literata);
    if (url.endsWith('manrope-var-latin.woff2')) return new Response(manrope);
    throw new Error('unexpected test asset request');
  };
  t.after(() => Object.assign(globalThis, original));
  const document = {fonts: {add(face: FontFace) { faces.add(face); }, delete(face: FontFace) { return faces.delete(face); }}} as unknown as Document;
  return {document, requests, faces, literata, hold() { hold = true; }, release() { finish?.(); }, get waiting() { return finish !== undefined; }};
}

test('actual main font bytes are verified once, shared with EPUB and owned by the viewer lifetime', async t => {
  const env = await environment(t), life = new AbortController();
  const resource = await loadReadFonts({document: env.document, signal: life.signal}); t.after(resource.dispose);
  assert.equal(env.requests.length, 2); assert.ok(env.requests.every(request => request.credentials === 'omit'));
  assert.equal(resource.fonts.literata.size, 955132); assert.equal(resource.fonts.manrope.size, 24836);
  assert.equal(createHash('sha256').update(new Uint8Array(await resource.fonts.literata.arrayBuffer())).digest('hex'), 'b41138c9373112f32abb589cc22e8674b06ed4048b0c513be922bdd26f274440');
  assert.equal(env.faces.size, 1);
  life.abort(); assert.equal(env.faces.size, 0); resource.dispose();
});

test('a font that finishes loading after retirement is never registered in the app document', async t => {
  const env = await environment(t), life = new AbortController(); env.hold();
  const pending = loadReadFonts({document: env.document, signal: life.signal});
  while (!env.waiting) await new Promise<void>(resolve => setImmediate(resolve));
  life.abort(); env.release(); await assert.rejects(pending);
  assert.equal(env.faces.size, 0);
});

test('the actual public font loaders need no WebCrypto in an HTTP host', async t => {
  const env = await environment(t), descriptor = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
  Object.defineProperty(globalThis, 'crypto', {value: Object.freeze({}), configurable: true});
  t.after(() => { if (descriptor) Object.defineProperty(globalThis, 'crypto', descriptor); else Reflect.deleteProperty(globalThis, 'crypto'); });
  const resource = await loadReadFonts({document: env.document, signal: new AbortController().signal}); t.after(resource.dispose);
  assert.equal(resource.fonts.literata.size, 955132); assert.equal(env.faces.size, 1);
  resource.dispose(); assert.equal(env.faces.size, 0);
});

test('trusted font loading and late cancellation need no modern AbortSignal helpers', async t => {
  const env = await environment(t);
  const any = Object.getOwnPropertyDescriptor(AbortSignal, 'any'), throwing = Object.getOwnPropertyDescriptor(AbortSignal.prototype, 'throwIfAborted');
  Object.defineProperty(AbortSignal, 'any', {value: undefined, configurable: true});
  Object.defineProperty(AbortSignal.prototype, 'throwIfAborted', {value: undefined, configurable: true});
  t.after(() => {if (any) Object.defineProperty(AbortSignal, 'any', any); if (throwing) Object.defineProperty(AbortSignal.prototype, 'throwIfAborted', throwing);});
  const ready = await loadReadFonts({document: env.document, signal: new AbortController().signal});
  assert.equal(env.faces.size, 1); ready.dispose(); assert.equal(env.faces.size, 0);
  env.hold(); const life = new AbortController(), pending = loadReadFonts({document: env.document, signal: life.signal});
  while (!env.waiting) await new Promise<void>(resolve => setImmediate(resolve));
  life.abort(); env.release(); await assert.rejects(pending); assert.equal(env.faces.size, 0);
});

test('font bootstrap deadline aborts stalled headers and a fresh call can retry', async t => {
  const env = await environment(t); t.mock.timers.enable({apis: ['setTimeout']});
  const healthyFetch = globalThis.fetch, seen: AbortSignal[] = [];
  globalThis.fetch = (_input, options) => new Promise<Response>((_, reject) => {
    const signal = options!.signal!; seen.push(signal);
    signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), {once: true});
  });
  const pending = loadReadFonts({document: env.document, signal: new AbortController().signal});
  const failure = assert.rejects(pending, (error: unknown) => (error as {code?: string}).code === 'source_unavailable');
  assert.equal(seen.length, 2); t.mock.timers.tick(30_000); await failure;
  assert.ok(seen.every(signal => signal.aborted)); assert.equal(env.faces.size, 0);
  globalThis.fetch = healthyFetch;
  const retry = await loadReadFonts({document: env.document, signal: new AbortController().signal});
  assert.equal(env.faces.size, 1); t.mock.timers.tick(30_000); assert.equal(env.faces.size, 1); retry.dispose();
});

test('stalled partial font bodies abort under the same deadline', async t => {
  const env = await environment(t); t.mock.timers.enable({apis: ['setTimeout']});
  const seen: AbortSignal[] = [];
  globalThis.fetch = async (_input, options) => {
    const signal = options!.signal!; seen.push(signal);
    const stream = new ReadableStream<Uint8Array>({start(controller) {
      controller.enqueue(new Uint8Array([0]));
      signal.addEventListener('abort', () => controller.error(new DOMException('aborted', 'AbortError')), {once: true});
    }});
    return new Response(stream);
  };
  const pending = loadReadFonts({document: env.document, signal: new AbortController().signal});
  const failure = assert.rejects(pending, (error: unknown) => (error as {code?: string}).code === 'source_unavailable');
  await new Promise<void>(resolve => setImmediate(resolve)); assert.equal(seen.length, 2);
  t.mock.timers.tick(30_000); await failure; assert.ok(seen.every(signal => signal.aborted)); assert.equal(env.faces.size, 0);
});

test('never-resolving FontFace.load times out and a late face cannot be installed', async t => {
  const env = await environment(t); t.mock.timers.enable({apis: ['setTimeout']}); env.hold();
  const pending = loadReadFonts({document: env.document, signal: new AbortController().signal});
  const failure = assert.rejects(pending, (error: unknown) => (error as {code?: string}).code === 'source_unavailable');
  while (!env.waiting) await new Promise<void>(resolve => setImmediate(resolve));
  t.mock.timers.tick(30_000); await failure; env.release(); await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(env.faces.size, 0);
});
