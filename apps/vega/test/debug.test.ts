import test from 'node:test';
import assert from 'node:assert/strict';
import {register} from 'node:module';
register(new URL('./debug-loader.mjs', import.meta.url));
const sent: string[] = [], printed: unknown[][] = [];
class SinkRequest {
  onloadend?: () => void;
  open() {}
  send(body: string) { sent.push(body); this.onloadend?.(); }
}
(globalThis as {XMLHttpRequest?: unknown}).XMLHttpRequest = SinkRequest;
(globalThis as {require?: unknown}).require = () => ({portico: {debugConsole: true, debugSink: 'https://debug.test'}});
const savedWarn = console.warn;
console.warn = (...args) => { printed.push(args); };
const debug = await import('../src/debug.tsx');

test('Vega trace, errors and console messages reach neither sink nor console with secrets', () => {
  try {
    debug.trace('GET https://media.test/v1/media/short-grant/file?token=short-query');
    console.warn(new Error('refreshToken=short-refresh'), 'password="space secret"');
    debug.traced('Runtime', {run() { throw new Error('accessToken=short-access'); }}).run();
  } catch (e) {
    assert.ok(e instanceof Error);
  } finally { console.warn = savedWarn; }
  const all = sent.join('\n') + printed.flat().map(String).join(' ');
  for (const secret of ['short-grant', 'short-query', 'short-refresh', 'space secret', 'short-access']) assert.equal(all.includes(secret), false, secret);
});
