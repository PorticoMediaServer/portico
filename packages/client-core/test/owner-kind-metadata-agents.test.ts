import test from 'node:test';
import assert from 'node:assert/strict';
import {readOwnerKindMetadataAgents} from '../src/owner-library-requests.ts';
import {kindMetadataAgentsPath} from '../src/server-admin/metadata-agent.ts';

for (const kind of ['movie', 'book']) test(`Add Library metadata choices preserve the original request/parse boundary for ${kind}`, async () => {
  let resolve!: (value: unknown) => void;const calls: unknown[][] = [];
  const answer = new Promise<unknown>(done => {resolve = done;});
  const returned = readOwnerKindMetadataAgents({request: <T>(...args: unknown[]) => {calls.push(args);return answer as Promise<T>;}}, kind);
  assert.equal(returned, answer, 'no added async continuation or early parsing');
  assert.deepEqual(calls, [[kindMetadataAgentsPath(kind)]], 'keep the original omitted method/body/signal');
  const raw = {kind};resolve(raw);assert.equal(await returned, raw);
});

test('Add Library metadata choices retain transport rejection identity and deferred settlement', async () => {
  let reject!: (reason: unknown) => void, settled = false;
  const answer = new Promise<never>((_done, fail) => {reject = fail;});
  const returned = readOwnerKindMetadataAgents({request: () => answer}, 'movie');
  void returned.then(() => {settled = true;}, () => {settled = true;});
  await Promise.resolve();assert.equal(settled, false);
  const refusal = {code: 'original_transport'};reject(refusal);await assert.rejects(returned, error => error === refusal);
});
