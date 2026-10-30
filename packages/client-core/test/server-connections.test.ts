import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseRememberedServer} from '../src/server-connections.ts';

const future = '2099-01-01T00:00:00Z';
const horizon = '2099-04-01T00:00:00Z';
const session = (over: Record<string, unknown> = {}) => ({
  accessToken: 't'.repeat(43),
  expiresAt: future,
  viewer: {accountId: 'a', profileId: 'p', serverId: 'srv-1', authority: 'local', role: 'owner'},
  sessionFamilyId: 'fam-1',
  tokenGeneration: '1',
  authorizationHorizon: horizon,
  ...over,
});
const record = (over: Record<string, unknown> = {}) => ({
  version: '1',
  logicalOrigin: 'https://server.example',
  session: session(),
  pin: {serverId: 'srv-1', publicKey: 'A'.repeat(43), fingerprint: 'B'.repeat(43)},
  routes: {paired: ['https://server.example']},
  ...over,
});

test('a saved connection reads back; null stays empty', () => {
  assert.equal(parseRememberedServer(null), undefined);
  assert.equal(parseRememberedServer(undefined), undefined);
  const parsed = parseRememberedServer(record())!;
  assert.equal(parsed.version, '1');
  assert.equal(parsed.logicalOrigin, 'https://server.example');
  assert.equal(parsed.session.sessionFamilyId, 'fam-1');
});

test('empty, wrong-version and incomplete envelopes are refused', () => {
  for (const bad of [{}, [], 'x', 0, {version: '2'}, {version: '1'}]) {
    assert.throws(() => parseRememberedServer(bad), /Saved server connection is incomplete/);
  }
  assert.throws(() => parseRememberedServer(record({logicalOrigin: 'not a url'})));
  assert.throws(() => parseRememberedServer(record({routes: {paired: []}, pin: {serverId: 'other', publicKey: 'A'.repeat(43), fingerprint: 'B'.repeat(43)}})), /Saved server identity is incomplete/);
});

test('a pairing list longer than eight is refused; eight passes', () => {
  const nine = Array.from({length: 9}, (_, i) => `https://s${i}.example`);
  assert.throws(() => parseRememberedServer(record({routes: {paired: nine}})), /incomplete/);
  const eight = Array.from({length: 8}, (_, i) => `https://s${i}.example`);
  assert.equal(parseRememberedServer(record({routes: {paired: eight}}))!.routes.paired.length, 8);
});

test('renewal and selection ids are bounded', () => {
  assert.equal(parseRememberedServer(record({pendingRefresh: 'a'.repeat(128)}))!.pendingRefresh?.length, 128);
  for (const bad of ['', 'bad id!', 'x'.repeat(129)]) {
    assert.throws(() => parseRememberedServer(record({pendingRefresh: bad})), /renewal state is invalid/);
  }
  const selection = 'A'.repeat(32);
  assert.equal(parseRememberedServer(record({selectionId: selection}))!.selectionId, selection);
  for (const bad of ['short', 'x'.repeat(31), 'x'.repeat(33), 'bad id!'.padEnd(32, 'x')]) {
    assert.throws(() => parseRememberedServer(record({selectionId: bad})), /selection identity is invalid/);
  }
});

test('a hosted route for another account is refused', () => {
  const hosted = {origin: 'https://account.example', accountId: 'a', key: {keyId: 'k', publicKey: 'C'.repeat(43)}};
  assert.equal(parseRememberedServer(record({hosted}))!.hosted?.accountId, 'a');
  assert.throws(() =>
    parseRememberedServer(record({
      hosted,
      session: session({viewer: {accountId: 'someone-else', profileId: 'p', serverId: 'srv-1', authority: 'hosted', role: 'owner'}}),
    })),
  );
  assert.throws(() => parseRememberedServer(record({hosted: {origin: 'http://public.example', accountId: 'a', key: {keyId: 'k', publicKey: 'C'}}})));
});

test('signed route evidence is bounded and shaped', () => {
  const good = {payload: 'p'.repeat(10), signature: 's'.repeat(86)};
  assert.deepEqual(parseRememberedServer(record({routes: {paired: [], current: good}}))!.routes.current, good);
  // An empty payload survives the parse and fails later at verify time (routeBytes
  // rejects it); the saved sign-in itself stays readable.
  assert.equal(parseRememberedServer(record({routes: {paired: [], current: {payload: '', signature: 's'.repeat(86)}}}))!.routes.current?.payload, '');
  for (const bad of [
    {payload: 'p', signature: 'short'},
    {payload: 'p'.repeat(50001), signature: 's'.repeat(86)},
    'x',
  ]) {
    assert.throws(() => parseRememberedServer(record({routes: {paired: [], current: bad}})), /route evidence is incomplete/);
  }
});

test('an expired saved session still reads, a damaged one does not', () => {
  const past = session({expiresAt: '2026-01-01T00:00:00Z'});
  assert.equal(parseRememberedServer(record({session: past}))!.session.sessionFamilyId, 'fam-1');
  assert.throws(() => parseRememberedServer(record({session: {...past, sessionFamilyId: 'bad id!'}})));
});
