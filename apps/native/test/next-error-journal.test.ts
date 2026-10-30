/** What a report to the owner holds, and what it never holds. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {ErrorJournal, MAX_ENTRIES, MAX_REPORT_CHARS, fatalRecord, readFatalRecord, redact, reportBody} from '../src-next/platform/error-journal.ts';

test('credentials never ride along', () => {
  assert.equal(redact('GET /v1/me failed with Authorization: Bearer abc.def-ghi_123'), 'GET /v1/me failed with Authorization: Bearer [removed]');
  assert.equal(redact('token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig-part'), 'token [removed]');
  assert.equal(redact('refresh 0123456789abcdef0123456789abcdef0123456789abcdef'), 'refresh [removed]');
  assert.equal(redact('fetch https://sam:hunter2@media.example.com/v1/items?token=abc&x=1 failed'), 'fetch https://media.example.com/v1/items?[removed] failed');
  assert.equal(redact('Playback of “Arrival” stopped: decoder error -12909'), 'Playback of “Arrival” stopped: decoder error -12909', 'what happened stays readable');
});

test('the journal keeps the newest lines, bounded, and one line for a repeated one', () => {
  let now = 0;
  const journal = new ErrorJournal(() => ++now);
  for (let i = 0; i < MAX_ENTRIES + 50; i++) journal.note('warn', 'line', i);
  assert.equal(journal.list().length, MAX_ENTRIES);
  assert.equal(journal.list()[0]!.text, 'line 50');
  journal.clear();
  for (let i = 0; i < 20; i++) journal.note('error', 'the same thing');
  assert.equal(journal.list().length, 1);
  journal.note('error', new Error('boom'), {code: 'x'}, 'word '.repeat(1000));
  assert.ok(journal.list()[1]!.text.startsWith('Error: boom {"code":"x"}'));
  assert.equal(journal.list()[1]!.text.length, 2000);
  journal.note('warn', '   ');
  assert.equal(journal.list().length, 2, 'an empty line is not kept');
});

test('a line is redacted as it comes in, so nothing sensitive is ever held', () => {
  const journal = new ErrorJournal(() => 1);
  journal.note('error', 'request failed', {headers: {Authorization: 'Bearer secret-token-value'}});
  assert.ok(!journal.list()[0]!.text.includes('secret-token-value'));
});

test('the error that ended a run survives being stored and read back', () => {
  const error = Object.assign(new TypeError('undefined is not a function'), {stack: 'TypeError: undefined is not a function\n    at render (index.bundle:1:2)\n    at Bearer abcdefghijklmnop'});
  const record = fatalRecord(error, '1.4.0', 1000);
  assert.equal(record.message, 'TypeError: undefined is not a function');
  assert.ok(!record.stack.includes('abcdefghijklmnop'));
  assert.deepEqual(readFatalRecord(JSON.parse(JSON.stringify(record))), record);
  for (const raw of [null, 'x', [], {}, {...record, at: 'now'}, {...record, stack: 7}, {...record, message: 'x'.repeat(3000)}]) assert.equal(readFatalRecord(raw), undefined);
  assert.equal(fatalRecord('just a string', '1.4.0', 1).message, 'just a string');
});

test('the report says what it is, includes the last crash, and keeps the newest lines when it must be cut', () => {
  const journal = new ErrorJournal(() => Date.parse('2026-10-13T10:00:00Z'));
  journal.note('warn', 'first');
  journal.note('error', 'second');
  const body = reportBody({appVersion: '1.4.0', platform: 'tvos', deviceClass: 'television', deviceName: 'Living Room', now: Date.parse('2026-10-13T10:05:00Z'), fatal: fatalRecord(new Error('boom'), '1.4.0', Date.parse('2026-10-13T09:00:00Z')), entries: journal.list()});
  assert.match(body, /^Portico app report\nSent: 2026-10-13T10:05:00.000Z\nApp: 1.4.0 on tvos \(television\)\nDevice: Living Room\n/);
  assert.match(body, /Closed unexpectedly: 2026-10-13T09:00:00.000Z \(app 1.4.0\)\nError: boom/);
  assert.match(body, /Recent warnings and errors:\n2026-10-13T10:00:00.000Z warn: first\n2026-10-13T10:00:00.000Z error: second$/);
  const big = new ErrorJournal(() => 0);
  for (let i = 0; i < 200; i++) big.note('error', 'line-' + String(i).padStart(4, '0') + ' ' + 'word '.repeat(380));
  const cut = reportBody({appVersion: '1', platform: 'ios', deviceClass: 'mobile', now: 0, entries: big.list()});
  assert.ok(cut.length <= MAX_REPORT_CHARS);
  assert.ok(cut.includes('line-0199 '), 'the newest line is kept');
  assert.ok(!cut.includes('line-0000 '), 'the oldest lines are the ones let go');
  assert.match(reportBody({appVersion: '1', platform: 'ios', deviceClass: 'mobile', now: 0, entries: []}), /No warnings or errors were logged in this run\.$/);
});

test('named short credentials and media capabilities are removed before storage or reporting', () => {
  const journal = new ErrorJournal(() => 1);
  journal.note('error', {accessToken: 'short-access', refreshToken: 'short-refresh', password: 'space secret', proof: 'short-proof'},
    'fetch https://media.test/v1/media/short-grant/file https://media.test/v1/downloads/artifacts/short-download/sub.vtt',
    'https://media.test/v1/media/subtitles/short-caption/res/1');
  for (const secret of ['short-access', 'short-refresh', 'space secret', 'short-proof', 'short-grant', 'short-download', 'short-caption']) {
    assert.equal(journal.list()[0]!.text.includes(secret), false, secret);
  }
});
