import test from 'node:test';
import assert from 'node:assert/strict';
import {createLocalDeviceHint} from '../src/platform/local-device-entry.ts';

function browser(href: string) {
  const entry = {__TSR_key: 'original-entry', __TSR_index: 2};
  const calls: {state: unknown; href: string | URL | null | undefined}[] = [];
  return {location: {href}, history: {state: entry, replaceState(state: unknown, _title: string, href?: string | URL | null) {calls.push({state, href});}}, entry, calls};
}
test('local URL code is scrubbed before one origin-bound display hint is exposed', () => {
  const hint = createLocalDeviceHint(), page = browser('https://server.example/device#code=ABCD-EFGH');
  assert.equal(hint.capture(page.location, page.history), true);
  assert.deepEqual(page.calls, [{state: page.entry, href: 'https://server.example/device'}]);
  assert.equal(hint.take('https://server.example'), 'ABCD-EFGH');
  assert.equal(hint.take('https://server.example'), undefined);
});
test('malformed, duplicate and unknown fragments are scrubbed without a code or arbitrary destination', () => {
  for (const hash of ['code=ABCD-EFGH&code=JKLM-NPQR', 'code=ABCD-EFGH&next=https://other.example', 'code=ABCD-I012', 'token=private', 'code=%00ABCD-EFGH', 'code=ABCD-EFGH%0A']) {
    const hint = createLocalDeviceHint(), page = browser('https://server.example/device#' + hash);
    hint.capture(page.location, page.history);assert.equal(page.calls.length, 1);assert.equal(hint.take('https://server.example'), undefined);
  }
});
test('another origin or a failed URL scrub cannot receive an old hint', () => {
  const hint = createLocalDeviceHint(), page = browser('https://server.example/device#code=ABCD-EFGH');
  hint.capture(page.location, page.history);assert.equal(hint.take('https://other.example'), undefined);
  hint.capture(page.location, page.history);
  assert.throws(() => hint.capture(page.location, {state: page.entry, replaceState() {throw new Error('history unavailable');}}));
  assert.equal(hint.take('https://server.example'), undefined);
});
test('account callbacks and other destinations are never parsed or scrubbed by local approval', () => {
  const hint = createLocalDeviceHint();
  for (const path of ['/auth/portico-return', '/settings', '/device/other']) {
    const page = browser('https://server.example' + path + '#code=ABCD-EFGH');
    assert.equal(hint.capture(page.location, page.history), false);assert.equal(page.calls.length, 0);
  }
});
