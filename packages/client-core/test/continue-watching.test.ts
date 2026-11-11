import test from 'node:test';
import assert from 'node:assert/strict';
import {setContinueDismissed} from '../src/continue-watching.ts';

type Call = {path: string; method?: string; body?: unknown};
function fakeApi(answer: () => unknown = () => ({watched: false, playCount: 0, favorite: false, inList: false, notInterested: false})) {
  const calls: Call[] = [];
  return {calls, async request<T>(path: string, method?: string, body?: unknown): Promise<T> {calls.push({path, method, body}); return answer() as T;}};
}

test('Remove from Continue Watching is one PUT of the title\'s own state, and Undo puts it back', async () => {
  const api = fakeApi();
  assert.deepEqual(await setContinueDismissed(api, {itemId: 'movie 1', dismissed: true}), {revision: 1});
  await setContinueDismissed(api, {itemId: 'movie 1', dismissed: false, revision: 7});
  assert.deepEqual(api.calls, [{path: '/v1/items/movie%201/state', method: 'PUT', body: {continueDismissed: true}}, {path: '/v1/items/movie%201/state', method: 'PUT', body: {continueDismissed: false}}]);
});

test('a title is required, and a refusal keeps the server\'s code', async () => {
  await assert.rejects(setContinueDismissed(fakeApi(), {itemId: '', dismissed: true}));
  const refused = Object.assign(new Error('gone'), {status: 404, code: 'not_found'});
  await assert.rejects(setContinueDismissed(fakeApi(() => {throw refused;}), {itemId: 'm', dismissed: true}), error => error === refused);
});
