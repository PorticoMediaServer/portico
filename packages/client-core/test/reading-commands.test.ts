import test from 'node:test';
import assert from 'node:assert/strict';
import {commitReadingMarkAll, readReadingMarkAll, farewellReadingSession, readingMarkAllNeedsReview} from '../src/query/reading-commands.ts';
import {createReadingMarkAllCommand} from '../src/reading-mark-all.ts';

const proof = {serverId: 'server', libraryId: 'library', viewerFence: 'a'.repeat(64), revision: {catalog: 2, viewer: 1}, count: 3};
const operation = createReadingMarkAllCommand(proof, 'operation');
test('Reading projection and commit preserve the original command and acknowledge only validated completion', async () => {
  const calls: unknown[][] = [], acknowledgements: string[] = [];
  const api = {async request(...args: unknown[]) {calls.push(args);return calls.length === 1 ? proof : {...proof, revision: {catalog: 2, viewer: 2}, operationId: operation.operationId, expectedRevision: operation.expectedRevision, changed: 3};}};
  assert.deepEqual(await readReadingMarkAll(api, 'server', 'library'), proof);
  assert.equal(await commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => true, acknowledge: async id => {acknowledgements.push(id);}}), true);
  assert.deepEqual(calls, [['/v1/libraries/library/reading/mark-all', 'GET'], ['/v1/libraries/library/reading/mark-all', 'POST', operation]]);
  assert.equal(calls[1][2], operation);assert.deepEqual(acknowledgements, ['operation']);
});
test('a retired Reading operation never acknowledges or starts receipt recovery', async () => {
  const calls: unknown[][] = [];let acknowledged = false;
  const api = {async request(...args: unknown[]) {calls.push(args);return {mutationReceipt: {}};}};
  assert.equal(await commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => false, acknowledge: async () => {acknowledged = true;}}), false);
  assert.equal(calls.length, 1);assert.equal(acknowledged, false);
});
test('uncertain Reading writes keep their body and surface the original failure without retries', async () => {
  const error = new TypeError('lost response'), sent: unknown[] = [];
  const api = {async request(_path: string, _method?: string, body?: unknown) {sent.push(body);throw error;}};
  for (let attempt = 0; attempt < 2; attempt++) await assert.rejects(commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => true}), value => value === error);
  assert.deepEqual(sent, [operation, operation]);assert.equal(sent[0], sent[1]);
  assert.equal(readingMarkAllNeedsReview({code: 'mutation_needs_review'}), true);
  assert.equal(readingMarkAllNeedsReview(error), false);
});
test('Reading page leave uses the existing optional farewell delivery exactly once', () => {
  const calls: unknown[][] = [];
  farewellReadingSession({farewell: (...args) => {calls.push(args);}}, 'session');
  farewellReadingSession({}, 'session');
  assert.deepEqual(calls, [['/v1/reading/sessions/session', 'DELETE']]);
});
