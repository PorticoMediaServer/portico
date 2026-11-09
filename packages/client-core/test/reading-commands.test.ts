import test from 'node:test';
import assert from 'node:assert/strict';
import {commitReadingMarkAll, readReadingMarkAll, farewellReadingSession, readingMarkAllNeedsReview} from '../src/query/reading-commands.ts';
import {createReadingMarkAllCommand} from '../src/reading-mark-all.ts';

const proof = {serverId: 'server', libraryId: 'library', viewerFence: 'a'.repeat(64), revision: {catalog: 2, viewer: 1}, count: 3};
const operation = createReadingMarkAllCommand(proof, 'operation');
test('Reading projection and commit preserve the original command', async () => {
  const calls: unknown[][] = [];
  const api = {async request(...args: unknown[]) {calls.push(args);return calls.length === 1 ? proof : {...proof, revision: {catalog: 2, viewer: 2}, operationId: operation.operationId, expectedRevision: operation.expectedRevision, changed: 3};}};
  assert.deepEqual(await readReadingMarkAll(api, 'server', 'library'), proof);
  assert.equal(await commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => true}), true);
  assert.deepEqual(calls, [['/v1/libraries/library/reading/mark-all', 'GET'], ['/v1/libraries/library/reading/mark-all', 'POST', operation]]);
  assert.equal(calls[1][2], operation);
});
test('a retired Reading operation is not read any further', async () => {
  const calls: unknown[][] = [];
  const api = {async request(...args: unknown[]) {calls.push(args);return {};}};
  assert.equal(await commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => false}), false);
  assert.equal(calls.length, 1);
});
test('uncertain Reading writes keep their body and surface the original failure without retries', async () => {
  const error = new TypeError('lost response'), sent: unknown[] = [];
  const api = {async request(_path: string, _method?: string, body?: unknown) {sent.push(body);throw error;}};
  for (let attempt = 0; attempt < 2; attempt++) await assert.rejects(commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => true}), value => value === error);
  assert.deepEqual(sent, [operation, operation]);assert.equal(sent[0], sent[1]);
  assert.equal(readingMarkAllNeedsReview({code: 'revision_mismatch'}), true);
  assert.equal(readingMarkAllNeedsReview(error), false);
});
test('Reading page leave uses the existing optional farewell delivery exactly once', () => {
  const calls: unknown[][] = [];
  farewellReadingSession({farewell: (...args) => {calls.push(args);}}, 'session');
  farewellReadingSession({}, 'session');
  assert.deepEqual(calls, [['/v1/reading/sessions/session', 'DELETE']]);
});

test('Reading preserves the original asynchronous POST callback error ordering', async () => {
  const competing = new Error('competing-read-failure');
  const commit = commitReadingMarkAll({request: () => Promise.resolve(null)}, 'server', 'library', proof, operation, {isCurrent: () => true});
  const other = Promise.resolve().then(() => {}).then(() => {throw competing;});
  await assert.rejects(Promise.all([commit, other]), error => error === competing);
});

test('Reading projection publishes in its original parse continuation before operation settlement', async () => {
  const seen: string[] = [];
  const read = readReadingMarkAll({request: () => Promise.resolve(proof)}, 'server', 'library', value => {
    assert.deepEqual(value, proof);seen.push('receive');
  });
  await read.then(() => {seen.push('settled');});
  assert.deepEqual(seen, ['receive', 'settled']);
});
