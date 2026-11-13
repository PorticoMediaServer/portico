import test from 'node:test';
import assert from 'node:assert/strict';
import {readOwnerKindMetadataAgents} from '../src/owner-library-requests.ts';
import {parseKindMetadataAgents} from '../src/server-admin/metadata-agent.ts';

for (const kind of ['movie', 'book']) test(`Add Library metadata choices preserve the original request/parse boundary for ${kind}`, async () => {
  let resolve!: (value: unknown) => void;const calls: unknown[][] = [];
  const answer = new Promise<unknown>(done => {resolve = done;});
  const returned = readOwnerKindMetadataAgents({request: <T>(...args: unknown[]) => {calls.push(args);return answer as Promise<T>;}}, kind);
  assert.equal(returned, answer, 'no added async continuation or early parsing');
  assert.deepEqual(calls, [['/v1/admin/libraries']], 'the owner list supplies choices without a kind route');
  const raw = {kind};resolve(raw);assert.equal(await returned, raw);
});

test('empty-library answers supply only the requested kind’s live providers and preserve the abort signal', async () => {
  const signal = new AbortController().signal, calls: unknown[][] = [];
  const raw = {items: [], metadataProviders: {
    movies: [{id: 'tmdb', available: true}, {id: 'tvdb', available: false, reason: 'TheTVDB needs the owner key.'}, {id: 'local', available: true}],
    anime: [{id: 'tmdb', available: true}, {id: 'tvdb', available: true}, {id: 'anilist', available: true}, {id: 'local', available: true}],
    books: [{id: 'local', available: true}],
  }};
  const answer = Promise.resolve(raw);
  const returned = readOwnerKindMetadataAgents({request: <T>(...args: unknown[]) => {calls.push(args); return answer as Promise<T>;}}, 'anime', signal);
  assert.equal(returned, answer);
  assert.deepEqual(calls, [['/v1/admin/libraries', 'GET', undefined, signal]]);
  const anime = parseKindMetadataAgents(await returned, 'anime');
  assert.deepEqual(anime.agents.map(p => [p.id, p.available]), [['tmdb', true], ['tvdb', true], ['anilist', true], ['local', true]]);
  assert.equal(anime.agents[2].name, 'AniList');
  assert.equal(parseKindMetadataAgents(raw, 'movie').agents[1].description, 'TheTVDB needs the owner key.');
  assert.deepEqual(parseKindMetadataAgents(raw, 'book').agents.map(p => p.id), ['local']);
  assert.throws(() => parseKindMetadataAgents(raw, 'unknown'), /metadata choices/);
});

test('Add Library metadata choices retain transport rejection identity and deferred settlement', async () => {
  let reject!: (reason: unknown) => void, settled = false;
  const answer = new Promise<never>((_done, fail) => {reject = fail;});
  const returned = readOwnerKindMetadataAgents({request: () => answer}, 'movie');
  void returned.then(() => {settled = true;}, () => {settled = true;});
  await Promise.resolve();assert.equal(settled, false);
  const refusal = {code: 'original_transport'};reject(refusal);await assert.rejects(returned, error => error === refusal);
});
