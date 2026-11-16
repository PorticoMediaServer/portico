import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {compileFunction} from 'node:vm';
import {transformSync} from 'rolldown/utils';
import * as documentRequests from '../src/query/download-documents.ts';
import * as downloads from '../src/downloads.ts';
import {parseMediaItem} from '../src/media-item.ts';
import {serverNow} from '../src/server-clock.ts';
import {createAttachmentDownloadPort} from '../src/attachment-download.ts';

// Exact source slices and full-file digests captured from untouched main before extraction.
// Only TypeScript syntax/import declarations are removed for execution. Both paths run
// against the same real parsers, deferred transport, state callbacks and microtask schedule.
const original = JSON.parse(readFileSync(new URL('./fixtures/cc2-main-ordering.json', import.meta.url), 'utf8'));
const deferred = () => {let resolve!: (value: unknown) => void, reject!: (error: unknown) => void; const promise = new Promise<unknown>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
function run(code: string, ports: Record<string, unknown>, expression: string) {
  const source = stripTypeScriptTypes('function referenceBody() {\n' + code + '\nreturn ' + expression + ';\n}', {mode: 'transform'});
  return compileFunction(source + '\nreturn referenceBody();', Object.keys(ports))(...Object.values(ports));
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
