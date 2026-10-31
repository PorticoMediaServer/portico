import test from 'node:test';
import assert from 'node:assert/strict';
import {isUnreadable, parseSetupAttempt, parseSetupRecord, readSetupLocal, reconcileSetup,
  type SetupAttempt, type SetupLocal, type SetupReading, type SetupRecord} from '../src/setup-store.ts';

const NOW = Date.parse('2026-10-31T12:00:00Z');
const record: SetupRecord = {version: 2, serverId: 'srv_1', requestId: 'req-1', setupToken: 'tok', tokenAt: NOW - 1000, name: 'Den', authMode: 'hosted'};
const attempt: SetupAttempt = {version: 2, serverId: 'srv_1', requestId: 'req-1', attemptId: 'att-1', origin: 'https://den.local', hostedOrigin: 'https://hosted.example',
  installationId: 'inst', state: 's', verifier: 'v', codeChallenge: 'c', createdAt: NOW - 1000, expiresAt: new Date(NOW + 600_000).toISOString()};
const handoff = {expected: {operationId: 'op'}, binding: {origin: 'o'}, transactionId: 't', expiresAt: '2026-10-31T12:30:00Z',
  claim: {payload: 'p', signature: 's'}, start: {payload: 'p', signature: 's'}};

type Phase = 'started' | 'awaiting_approval' | 'expired' | 'approved' | 'finishing' | 'exhausted' | 'installed' | 'installed_lapsed';
const server = (p: Phase, requestId = 'req-1'): SetupReading => ({status: p === 'expired' ? 'ready' : 'in_progress',
  setup: {phase: p, requestId, revision: '1', cancelling: false, renewable: false}});
const both: SetupLocal = {record, attempt};

// ---- parsing never throws -------------------------------------------------------------------------------------------------
test('a good record and a good attempt parse to themselves', () => {
  assert.deepEqual(parseSetupRecord(record), record);
  assert.deepEqual(parseSetupAttempt(attempt), attempt);
  assert.deepEqual(parseSetupAttempt({...attempt, handoff}), {...attempt, handoff});
  assert.deepEqual(parseSetupRecord({...record, authMode: 'local'}), {...record, authMode: 'local'});
});

test('unknown extra fields are dropped, never kept', () => {
  const parsed = parseSetupRecord({...record, extra: 'x', password: 'nope'});
  assert.ok(!isUnreadable(parsed));
  assert.deepEqual(Object.keys(parsed).sort(), Object.keys(record).sort());
});

const garbage: unknown[] = [undefined, null, 0, 1, NaN, '', 'text', '{"version":2}', true, false, [], [record], () => record, Symbol('x'), 10n,
  {}, {version: 2}, {...record, version: 1}, {...record, version: 3}, {...record, version: '2'}, {...record, version: null},
  {...record, serverId: 7}, {...record, serverId: ''}, {...record, requestId: undefined}, {...record, setupToken: {}}, {...record, tokenAt: '1'},
  {...record, tokenAt: -1}, {...record, tokenAt: 1.5}, {...record, tokenAt: Infinity}, {...record, name: 5}, {...record, authMode: 'both'}, {...record, authMode: undefined},
  // the v1 shapes these replace
  {version: 1, serverId: 'srv_1', requestId: 'req-1', setupToken: 'tok', name: 'Den', authMode: 'hosted'},
  {version: 1, kind: 'setup', attemptId: 'a', origin: 'o', hostedOrigin: 'h', pin: {}, expectedRevision: 1, setupRequestId: 'r', setupToken: 't', setupRevision: '1',
    installationId: 'i', state: 's', verifier: 'v', codeChallenge: 'c', createdAt: 1, expiresAt: 'x'},
  {...attempt, version: 1}, {...attempt, attemptId: 1}, {...attempt, origin: null}, {...attempt, createdAt: 'now'}, {...attempt, expiresAt: undefined},
  {...attempt, handoff: 'bad'}, {...attempt, handoff: {}}, {...attempt, handoff: {...handoff, claim: {payload: 1}}}, {...attempt, handoff: {...handoff, expected: null}},
  {...attempt, handoff: null},
  Object.create(null),
  new Proxy({}, {get() { throw new Error('boom'); }, has() { throw new Error('boom'); }, ownKeys() { throw new Error('boom'); }}),
  {get version(): number { throw new Error('getter boom'); }},
];

test('parse never throws on garbage, older versions and wrong types, and answers unreadable for all of it', () => {
  for (const raw of garbage) {
    assert.doesNotThrow(() => { parseSetupRecord(raw); parseSetupAttempt(raw); }, String(typeof raw));
    assert.deepEqual(parseSetupRecord(raw), {unreadable: true}, `record: ${String(typeof raw)}`);
    assert.deepEqual(parseSetupAttempt(raw), {unreadable: true}, `attempt: ${String(typeof raw)}`);
  }
  // a record is never mistaken for an attempt or the reverse
  assert.deepEqual(parseSetupRecord(attempt), {unreadable: true});
  assert.deepEqual(parseSetupAttempt(record), {unreadable: true});
});

test('a half-bound attempt is unreadable, not an attempt without its handoff', () => {
  assert.deepEqual(parseSetupAttempt({...attempt, handoff: {...handoff, start: undefined}}), {unreadable: true});
});

test('reading both stored values: either unreadable makes the whole read unreadable; absent is just absent', () => {
  assert.deepEqual(readSetupLocal(record, attempt), {record, attempt});
  assert.deepEqual(readSetupLocal(record, undefined), {record});
  assert.deepEqual(readSetupLocal(undefined, attempt), {attempt});
  assert.deepEqual(readSetupLocal(null, null), {});
  assert.deepEqual(readSetupLocal(record, {version: 1}), {unreadable: true});
  assert.deepEqual(readSetupLocal('junk', attempt), {unreadable: true});
  assert.deepEqual(readSetupLocal({version: 1}, {version: 1}), {unreadable: true});
});

// ---- the §6.3 table, one case per row -------------------------------------------------------------------------------------
test('R-13: an unreadable, older or malformed record is retired, whatever the server says', () => {
  const unreadable: SetupLocal = {unreadable: true};
  for (const reading of [server('started'), server('installed'), {status: 'ready'} as SetupReading, {status: 'complete'} as SetupReading, {status: 'unknown'} as SetupReading])
    assert.equal(reconcileSetup(unreadable, reading, NOW), 'retire');
  assert.equal(reconcileSetup({...both, unreadable: true}, server('started'), NOW), 'retire');
});

test('R-14: a requestId the server does not know is retired', () => {
  assert.equal(reconcileSetup(both, server('started', 'someone-else'), NOW), 'retire');
  assert.equal(reconcileSetup({record}, server('awaiting_approval', 'someone-else'), NOW), 'retire');
  assert.equal(reconcileSetup(both, server('installed', 'someone-else'), NOW), 'retire');
});

test('R-14: a token the server does not accept (no own setup in the answer) is retired', () => {
  assert.equal(reconcileSetup(both, {status: 'in_progress'}, NOW), 'retire');
  assert.equal(reconcileSetup(both, {status: 'in_progress', retryAfterSeconds: 600}, NOW), 'retire');
});

test('server open is retired: the next submit starts a fresh bootstrap', () => {
  assert.equal(reconcileSetup(both, {status: 'ready'}, NOW), 'retire');
  assert.equal(reconcileSetup({record}, {status: 'ready'}, NOW), 'retire');
});

test('server expired is retired (never approved, window passed)', () => {
  assert.equal(reconcileSetup(both, server('expired'), NOW), 'retire');
  assert.equal(reconcileSetup({record}, server('expired'), NOW), 'retire');
});

test('server complete is retired: an account exists, the record has no job left', () => {
  assert.equal(reconcileSetup(both, {status: 'complete'}, NOW), 'retire');
  assert.equal(reconcileSetup({record}, {status: 'complete'}, NOW), 'retire');
});

test('a live own setup is kept, in every live phase', () => {
  for (const p of ['started', 'awaiting_approval', 'approved', 'finishing', 'exhausted', 'installed', 'installed_lapsed'] as const) {
    assert.equal(reconcileSetup(both, server(p), NOW), 'keep', p);
    assert.equal(reconcileSetup({record}, server(p), NOW), 'keep', `${p} without an attempt`);
  }
});

test('a Hosted record never pairs with a local one: a local record with a Portico phase is retired', () => {
  assert.equal(reconcileSetup({record: {...record, authMode: 'local'}}, server('started'), NOW), 'retire');
});

test('an attempt that has lapsed, or belongs to another request or server, is retired alone while the record stays', () => {
  const lapsed = {...attempt, expiresAt: new Date(NOW - 1).toISOString()};
  const atNow = {...attempt, expiresAt: new Date(NOW).toISOString()};
  const bad = {...attempt, expiresAt: 'not a date'};
  for (const stale of [lapsed, atNow, bad, {...attempt, requestId: 'req-0'}, {...attempt, serverId: 'srv_2'}]) {
    assert.equal(reconcileSetup({record, attempt: stale}, server('installed'), NOW), 'retire-attempt');
    assert.equal(reconcileSetup({record, attempt: stale}, server('installed_lapsed'), NOW), 'retire-attempt');
    assert.equal(reconcileSetup({record, attempt: stale}, server('awaiting_approval'), NOW), 'retire-attempt');
  }
});

test('an attempt with no record is an orphan: retired alone', () => {
  assert.equal(reconcileSetup({attempt}, server('started'), NOW), 'retire-attempt');
  assert.equal(reconcileSetup({attempt}, {status: 'ready'}, NOW), 'retire-attempt');
  assert.equal(reconcileSetup({}, {status: 'ready'}, NOW), 'keep');
});

test('an attempt matching the record is kept with it; a record-level mismatch retires both', () => {
  assert.equal(reconcileSetup({record, attempt}, server('installed'), NOW), 'keep');
  assert.equal(reconcileSetup({record, attempt: {...attempt, requestId: 'req-9'}}, server('started', 'someone-else'), NOW), 'retire');
});

test('a newer server (an unknown status or phase) never destroys anything on a guess', () => {
  assert.equal(reconcileSetup(both, {status: 'unknown'}, NOW), 'keep');
  assert.equal(reconcileSetup(both, {status: 'in_progress', setup: 'unknown'}, NOW), 'keep');
  assert.equal(reconcileSetup({record}, {status: 'unknown'}, NOW), 'keep');
});

test('reconcile is pure and never throws on any local shape', () => {
  const frozen = Object.freeze({record: Object.freeze({...record}), attempt: Object.freeze({...attempt})});
  assert.equal(reconcileSetup(frozen, server('started'), NOW), 'keep');
  assert.equal(reconcileSetup(frozen, server('started'), NOW), 'keep');
  assert.doesNotThrow(() => reconcileSetup({record, attempt: {...attempt, expiresAt: ''}}, server('started'), Number.NaN));
});
