import test from 'node:test';
import assert from 'node:assert/strict';
import {isRecommendationRow, notInterestedTarget, parseRecommendationsReset, RESET_RECOMMENDATIONS_PATH, resetRecommendations, setNotInterested} from '../src/recommendation-feedback.ts';
import {personalBatchBody} from '../src/personal-saved.ts';
import {entryActionList, entryActions} from '../src/presentation/entry-actions.ts';

type Call = {path: string; method?: string; body?: any};
function fakeApi(answer: (call: Call) => unknown) {
  const calls: Call[] = [];
  return {calls, async request<T>(path: string, method?: string, body?: unknown): Promise<T> { const call = {path, method, body}; calls.push(call); return answer(call) as T; }};
}
const receipt = (call: Call, row: Record<string, unknown>) => ({serverId: 's1', viewerFence: 'f', operationId: call.body.operationId, updated: row.ok ? 1 : 0, failed: row.ok ? 0 : 1, results: [{itemId: call.body.items[0].itemId, ...row}]});

test('Not interested rides the personal batch, and Undo sends false', async () => {
  const api = fakeApi(call => receipt(call, {ok: true, personal: {revision: call.body.items[0].notInterested ? 4 : 5, notInterested: call.body.items[0].notInterested}}));
  assert.deepEqual(await setNotInterested(api, {itemId: 'm1', notInterested: true, serverId: 's1', operationId: 'op-1'}), {revision: 4});
  assert.equal(api.calls[0]!.path, '/v1/items/personal-state:batch');
  assert.equal(api.calls[0]!.method, 'PUT');
  assert.deepEqual(api.calls[0]!.body, {operationId: 'op-1', items: [{itemId: 'm1', notInterested: true}]});
  await setNotInterested(api, {itemId: 'm1', notInterested: false, serverId: 's1', operationId: 'op-2'});
  assert.deepEqual(api.calls[1]!.body.items, [{itemId: 'm1', notInterested: false}]);
});

test('a refused row rejects with its code, so the card comes back', async () => {
  const api = fakeApi(call => receipt(call, {ok: false, code: 'not_found', message: 'gone'}));
  await assert.rejects(setNotInterested(api, {itemId: 'm1', notInterested: true, serverId: 's1', operationId: 'op-1'}), (e: {code?: string}) => e.code === 'not_found');
  // Another server's receipt is not an answer.
  const other = fakeApi(call => ({...receipt(call, {ok: true}), serverId: 's2'}));
  await assert.rejects(setNotInterested(other, {itemId: 'm1', notInterested: true, serverId: 's1', operationId: 'op-1'}));
});

test('a show, album or book card names its own id; a film or episode the item', () => {
  assert.equal(notInterestedTarget({id: 'card', kind: 'show', navigation: {entityId: 'show-1'}}), 'show-1');
  assert.equal(notInterestedTarget({id: 'album-1', kind: 'album'}), 'album-1');
  assert.equal(notInterestedTarget({id: 'm1', kind: 'movie', navigation: {entityId: 'm1'}}), 'm1');
  assert.equal(notInterestedTarget({id: 'e1', kind: 'episode', navigation: {entityId: 'show-1'}}), 'e1');
});

test('only recommendation rows offer it', () => {
  assert.equal(isRecommendationRow({id: 'recommended', kind: 'recommendation'}), true);
  assert.equal(isRecommendationRow({id: 'trending_now', kind: 'recommendation'}), true);
  for (const kind of ['continue', 'saved', 'recent', 'featured', 'ondeck']) assert.equal(isRecommendationRow({id: 'x', kind}), false, kind);
  // Discover sections carry no kind: their ids say it.
  assert.equal(isRecommendationRow({id: 'recommended'}), true);
  assert.equal(isRecommendationRow({id: 'for_you:genre:g:drama'}), true);
  assert.equal(isRecommendationRow({id: 'recently_added'}), false);
  assert.equal(isRecommendationRow({id: 'continue_watching'}), false);
});

test('the batch body carries notInterested alone', () => {
  assert.deepEqual(personalBatchBody('op', [{itemId: 'a', notInterested: true}]).items, [{itemId: 'a', notInterested: true}]);
  assert.throws(() => personalBatchBody('op', [{itemId: 'a'}]));
});

test('the actions menu offers Not interested only when asked, with the personal actions', () => {
  const list = entryActionList(entryActions({playable: true, watchlisted: false, notInterested: true}));
  const action = list.find(a => a.id === 'notInterested')!;
  assert.equal(action.label, 'entry.notInterested');
  assert.equal(action.group, 'personal');
  assert.equal(entryActionList(entryActions({playable: true, watchlisted: false})).some(a => a.id === 'notInterested'), false);
});

test('Reset recommendations: one POST carrying the operation id; the receipt must match', async () => {
  const api = fakeApi(call => ({serverId: 's1', viewerFence: 'f', operationId: call.body.operationId, clearedNotInterested: 3, receiptLifetimeSeconds: 2592000}));
  assert.deepEqual(await resetRecommendations(api, {serverId: 's1', operationId: 'reset-1'}), {operationId: 'reset-1', clearedNotInterested: 3});
  assert.deepEqual(api.calls, [{path: RESET_RECOMMENDATIONS_PATH, method: 'POST', body: {operationId: 'reset-1'}}]);
  await assert.rejects(resetRecommendations(api, {serverId: 's1', operationId: 'bad id'}));
  assert.throws(() => parseRecommendationsReset({serverId: 's1', operationId: 'other', clearedNotInterested: 0}, 's1', 'reset-1'));
  // The count is only shown: one that cannot be read is 0, and the receipt still stands.
  assert.deepEqual(parseRecommendationsReset({serverId: 's1', operationId: 'reset-1', clearedNotInterested: -1, future: true}, 's1', 'reset-1'), {operationId: 'reset-1', clearedNotInterested: 0});
  assert.throws(() => parseRecommendationsReset({serverId: 'other', operationId: 'reset-1', clearedNotInterested: 0}, 's1', 'reset-1'));
});
