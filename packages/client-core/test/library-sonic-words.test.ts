import test from 'node:test';
import assert from 'node:assert/strict';
import {createI18n} from '../../i18n/src/index.ts';
import {librarySonicStatus} from '../src/server-admin/library-sonic-words.ts';
import type {LibrarySonicDocument} from '../src/server-admin/library-sonic.ts';

const i18n = createI18n();
const doc = (over: Partial<LibrarySonicDocument> = {}): LibrarySonicDocument => ({
  libraryId: 'music', revision: 4, enabled: true, defaultEnabled: true, available: true, state: 'analyzing',
  done: 12400, total: 48000, policyAllows: true, policyRevision: 3, observedAtMs: 0, ...over,
});
const time = (at: number) => `at ${at}`;
const n = (value: number) => new Intl.NumberFormat().format(value);

test('Sonic status says only what the current document measured', () => {
  assert.equal(librarySonicStatus(doc({state: 'disabled', enabled: false}), i18n.t, time), undefined);
  assert.deepEqual(librarySonicStatus(doc({state: 'unavailable', available: false, enabled: true}), i18n.t, time),
    {text: 'Can’t run on this server: the analysis engine didn’t load.', tone: 'warn'});
  assert.deepEqual(librarySonicStatus(doc(), i18n.t, time),
    {text: `Analyzing music, ${n(12400)} of ${n(48000)}`, tone: 'quiet'});
  assert.deepEqual(librarySonicStatus(doc({state: 'paused', pausedUntilMs: 1788580000000}), i18n.t, time),
    {text: `Paused until at 1788580000000, ${n(12400)} of ${n(48000)}`, tone: 'quiet'});
  assert.deepEqual(librarySonicStatus(doc({state: 'paused'}), i18n.t, time), {text: 'Paused', tone: 'quiet'});
  assert.deepEqual(librarySonicStatus(doc({state: 'complete', done: 48000}), i18n.t, time),
    {text: `All ${n(48000)} songs analyzed`, tone: 'ok'});
  assert(Object.isFrozen(librarySonicStatus(doc(), i18n.t, time)));
});

test('Sonic status refuses impossible counts and unusable paused times without inventing output', () => {
  assert.equal(librarySonicStatus(doc({done: -1}), i18n.t, time), undefined);
  assert.equal(librarySonicStatus(doc({done: 1.5}), i18n.t, time), undefined);
  assert.equal(librarySonicStatus(doc({state: 'complete', total: Number.NaN}), i18n.t, time), undefined);
  let calls = 0;
  const spy = (at: number) => {calls++; return time(at);};
  assert.deepEqual(librarySonicStatus(doc({state: 'paused', pausedUntilMs: 9000000000000000}), i18n.t, spy), {text: 'Paused', tone: 'quiet'});
  assert.equal(calls, 0);
  assert.deepEqual(librarySonicStatus(doc({state: 'paused', pausedUntilMs: 1788580000000}), i18n.t, () => ''), {text: 'Paused', tone: 'quiet'});
  assert.deepEqual(librarySonicStatus(doc({state: 'paused', pausedUntilMs: 1788580000000}), i18n.t, () => {throw new Error('formatter');}), {text: 'Paused', tone: 'quiet'});
});
