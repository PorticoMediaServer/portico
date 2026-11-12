import test from 'node:test';
import assert from 'node:assert/strict';
import {commitReadingMarkAll, readReadingMarkAll, farewellReadingSession, readingMarkAllNeedsReview} from '../src/query/reading-commands.ts';
import {createReadingMarkAllCommand} from '../src/reading-mark-all.ts';

const proof = {serverId: 'server', libraryId: 'library', viewerFence: '', revision: {catalog: 0, viewer: 0}, count: 3};
const operation = createReadingMarkAllCommand(proof, 'operation');
const job = {kind: 'set_state', target: {filter: 'library=library&kind=book,comic,issue'}, params: {watched: true}};
test('Mark all read counts the unread titles, then marks every one with the set_state job and waits for it', async () => {
  const calls: unknown[][] = [], states = ['running', 'done'];
  const api = {async request(...args: unknown[]) {calls.push(args);
    if (calls.length === 1) return {items: [], total: 3};
    return {id: 'job_1', kind: 'set_state', state: states.shift()};}};
  assert.deepEqual(await readReadingMarkAll(api, 'server', 'library'), proof);
  let committed = 0;
  assert.equal(await commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => true, onCommitted: () => committed++}, async () => {}), true);
  assert.deepEqual(calls, [['/v1/items?library=library&kind=book%2Ccomic%2Cissue&watched=false&limit=1&total=true', 'GET'], ['/v1/jobs', 'POST', job], ['/v1/jobs/job_1', 'GET']]);
  assert.equal(committed, 1, 'the library is read again only once the job has finished');
});
test('a job that fails says so and does not count as marked', async () => {
  let committed = 0;
  const api = {async request() {return {id: 'job_1', kind: 'set_state', state: 'failed', error: 'stopped'};}};
  await assert.rejects(commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => true, onCommitted: () => committed++}, async () => {}), {code: 'request_failed'});
  assert.equal(committed, 0);
});
test('a retired Reading operation is not read any further', async () => {
  const calls: unknown[][] = [];
  const api = {async request(...args: unknown[]) {calls.push(args);return {id: 'job_1', state: 'running'};}};
  assert.equal(await commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => false}), false);
  assert.equal(calls.length, 1);
});
test('uncertain Reading writes surface the original failure without retries', async () => {
  const error = new TypeError('lost response'), sent: unknown[] = [];
  const api = {async request(_path: string, _method?: string, body?: unknown) {sent.push(body);throw error;}};
  for (let attempt = 0; attempt < 2; attempt++) await assert.rejects(commitReadingMarkAll(api, 'server', 'library', proof, operation, {isCurrent: () => true}), value => value === error);
  assert.deepEqual(sent, [job, job]);
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
  const read = readReadingMarkAll({request: () => Promise.resolve({items: [], total: 3})}, 'server', 'library', value => {
    assert.deepEqual(value, proof);seen.push('receive');
  });
  await read.then(() => {seen.push('settled');});
  assert.deepEqual(seen, ['receive', 'settled']);
});
