import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {compileFunction} from 'node:vm';
import {transformSync} from 'rolldown/utils';
import * as requests from '../src/download-coordination.ts';
import * as documentRequests from '../src/query/download-documents.ts';
import * as downloads from '../src/downloads.ts';
import {parseMediaItem} from '../src/media-item.ts';
import {serverNow} from '../src/server-clock.ts';
import {createAttachmentDownloadPort} from '../src/attachment-download.ts';

// Exact source slices and full-file digests captured from untouched main before extraction.
// Only TypeScript syntax/import declarations are removed for execution. Both paths run
// against the same real parsers, deferred transport, state callbacks and microtask schedule.
const original = JSON.parse(readFileSync(new URL('./fixtures/cc2-main-ordering.json', import.meta.url), 'utf8'));
const sheet = readFileSync(new URL('../../../apps/native/src-next/screens/downloads/ReadingDownloadSheet.tsx', import.meta.url), 'utf8');
const currentChoose = sheet.slice(sheet.indexOf('  const choose = async'), sheet.indexOf('  const shown: State'));
const currentLoading = sheet.slice(sheet.indexOf('    void (async () => {'), sheet.indexOf('    return () => {opening.dispose();'));
const deferred = () => {let resolve!: (value: unknown) => void, reject!: (error: unknown) => void; const promise = new Promise<unknown>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
const flush = async () => {for (let n = 0; n < 16; n++) await Promise.resolve();};
function run(code: string, ports: Record<string, unknown>, expression: string) {
  const source = stripTypeScriptTypes('function referenceBody() {\n' + code + '\nreturn ' + expression + ';\n}', {mode: 'transform'});
  return compileFunction(source + '\nreturn referenceBody();', Object.keys(ports))(...Object.values(ports));
}
const poison = (trace: string[], key = 'batchId') => Object.defineProperty({}, key, {enumerable: true, get() {trace.push('parse'); throw new Error('dto');}});
function chooseHarness(code: string, overrides: Record<string, unknown> = {}) {
  const trace: string[] = [], response = deferred(); let current = true;
  const policy = {policy: {allowDownloads: true}, source: {available: true}, options: [{quality: 'original', available: true}], storage: {remainingBytes: null}};
  const version = {sourceId: 'version', format: 'pdf', inventoryAvailable: true, size: 100, policy};
  const opening = {pending: false, signal: new AbortController().signal, current: () => current, versions: [version],
    attempts: {operationFor: () => {trace.push('intent'); return 'held';}, settle: () => {trace.push('settle');}},
    lease: {device: {getSnapshot: () => ({ready: true, freeBytes: 1000})}, service: {}, wantPublication: async () => {trace.push('want');}}};
  const ports = {...requests, ...overrides, parseDownloadBatch: downloads.parseDownloadBatch,
    context: {api: {request: () => {trace.push('request'); return response.promise;}}}, request: {itemId: 'item'},
    setState: (update: any) => {const state = update({opening}); if (state?.error) trace.push('error:' + state.error.message); if (Object.hasOwn(state, 'busy')) trace.push(state.busy === undefined ? 'idle' : 'busy');},
    close: () => trace.push('close')};
  const choose = run(code, ports, 'choose');
  return {trace, response, choose: () => choose(opening, version), retire: () => {current = false; trace.push('retire');}, pending: () => opening.pending};
}
for (const scenario of ['retired malformed answer', 'transport error before queued retirement', 'idempotency refusal after retirement'] as const) {
  test('main parity: native admission ' + scenario, async () => {
    const exercise = async (code: string) => {
      const h = chooseHarness(code), task = h.choose();
      if (scenario === 'retired malformed answer') {h.retire(); h.response.resolve(poison(h.trace));}
      if (scenario === 'transport error before queued retirement') {h.response.reject(new Error('transport')); queueMicrotask(h.retire);}
      if (scenario === 'idempotency refusal after retirement') {h.retire(); h.response.reject(Object.assign(new Error('idempotency'), {code: 'idempotency_key_reused'}));}
      await task; await flush(); assert.equal(h.pending(), false); return h.trace;
    };
    const expected = await exercise(original.choose), actual = await exercise(currentChoose);
    assert.deepEqual(actual, expected);
    if (scenario === 'retired malformed answer') {assert.ok(expected.includes('settle')); assert.ok(!expected.includes('parse'));}
    if (scenario === 'transport error before queued retirement') assert.ok(expected.indexOf('error:transport') < expected.indexOf('retire'));
  });
}

test('parity fixture detects an extra async request layer changing transport-versus-retirement order', async () => {
  const exercise = async (overrides: Record<string, unknown>) => {
    const h = chooseHarness(currentChoose, overrides), task = h.choose();
    h.response.reject(new Error('transport')); queueMicrotask(h.retire); await task; return h.trace;
  };
  const direct = await exercise({});
  const layered = await exercise({prepareItemDownloadResponse: async (...args: Parameters<typeof requests.prepareItemDownloadResponse>) => await requests.prepareItemDownloadResponse(...args)});
  assert.ok(direct.includes('error:transport'));
  assert.ok(!layered.includes('error:transport'));
});
function loadHarness(code: string, count: number) {
  const trace: string[] = [], responses = Array.from({length: count}, deferred); let current = true, index = 0;
  const versions = Array.from({length: count}, (_, n) => ({sourceId: 'version-' + n, format: 'pdf', size: 100}));
  const ports = {...requests, parseDownloadOptions: downloads.parseDownloadOptions, itemId: 'item', signal: new AbortController().signal,
    reader: {versions: async () => ({versions})}, lease: {device: {open: async () => {}}}, opening: {}, current: () => current,
    context: {api: {request: () => {trace.push('request'); return responses[index++]!.promise;}}},
    setState: (state: any) => {trace.push(state.error ? 'error:' + state.error.message : 'published');}};
  return {trace, responses, start: () => run(code.replace('void (async', 'return (async'), ports, 'undefined'), retire: () => {current = false; trace.push('retire');}};
}
for (const scenario of ['retired malformed answer', 'parallel parser and transport failures'] as const) {
  test('main parity: native reading options ' + scenario, async () => {
    const exercise = async (code: string) => {
      const h = loadHarness(code, scenario === 'retired malformed answer' ? 1 : 2), task = h.start(); await flush();
      if (scenario === 'retired malformed answer') {h.retire(); h.responses[0]!.resolve(poison(h.trace, 'itemId'));}
      else {h.responses[0]!.resolve(poison(h.trace, 'itemId')); queueMicrotask(() => h.responses[1]!.reject(new Error('transport')));}
      await task; await flush(); return h.trace;
    };
    const expected = await exercise(original.loading), actual = await exercise(currentLoading);
    assert.deepEqual(actual, expected);
    if (scenario === 'retired malformed answer') assert.ok(!expected.includes('parse'));
    else assert.ok(expected.includes('error:dto'));
  });
}
function originalBrowser(api: any, current: () => boolean, signal: AbortSignal) {
  let code = transformSync('main-browser-download.ts', original.browser, {tsconfig: false}).code;
  code = code.replace(/^import .*;\n/gm, '').replace('export function createBrowserDownloadPort', 'function createBrowserDownloadPort');
  return run(code, {...downloads, parseMediaItem, serverNow, ...documentRequests,
    // Main's generated builders and DOM adapter are its actual external ports.
    ...routeBuilders, document: {createElement: () => ({click() {}, remove() {}}), body: {append() {}}}}, 'createBrowserDownloadPort')(api, current, signal);
}
import * as routeBuilders from '../../contracts/src/routes.ts';
test('main parity: attachment parser rejection competes with the same queued fallback', async () => {
  const exercise = async (factory: typeof originalBrowser) => {
    const response = deferred(); let current = true;
    const port = factory({request: () => response.promise, mediaUrl: (path: string) => path}, () => current, new AbortController().signal);
    const answer = port.options({itemId: 'item', libraryId: 'library'}, new AbortController().signal, () => true).then(() => 'success', (e: Error) => e.message);
    const fallback = deferred(); response.reject(new Error('transport'));
    queueMicrotask(() => {current = false; fallback.resolve('fallback');});
    return Promise.race([answer, fallback.promise]);
  };
  const expected = await exercise(originalBrowser);
  assert.equal(await exercise((api, current, signal) => createAttachmentDownloadPort(api, current, signal, () => {})), expected);
});
