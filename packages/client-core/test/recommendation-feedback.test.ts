import test from 'node:test';
import assert from 'node:assert/strict';
import {isRecommendationRow, notInterestedTarget, parseRecommendationsReset, RESET_RECOMMENDATIONS_PATH, resetRecommendations, setNotInterested} from '../src/recommendation-feedback.ts';
import {entryActionList, entryActions} from '../src/presentation/entry-actions.ts';

type Call = {path: string; method?: string; body?: any};
function fakeApi(answer: (call: Call) => unknown) {
  const calls: Call[] = [];
  return {calls, async request<T>(path: string, method?: string, body?: unknown): Promise<T> { const call = {path, method, body}; calls.push(call); return answer(call) as T; }};
}
const receipt = (call: Call, row: Record<string, unknown>) => ({serverId: 's1', viewerFence: 'f', operationId: call.body.operationId, updated: row.ok ? 1 : 0, failed: row.ok ? 0 : 1, results: [{itemId: call.body.items[0].itemId, ...row}]});

test('Not interested is one PUT of the title\'s own state, and Undo sends false', async () => {
  const api = fakeApi(call => ({watched: false, playCount: 0, favorite: false, inList: false, notInterested: call.body.notInterested}));
  assert.deepEqual(await setNotInterested(api, {itemId: 'm1', notInterested: true}), {revision: 1});
  assert.equal(api.calls[0]!.path, '/v1/items/m1/state');
  assert.equal(api.calls[0]!.method, 'PUT');
  assert.deepEqual(api.calls[0]!.body, {notInterested: true});
  await setNotInterested(api, {itemId: 'm1', notInterested: false});
  assert.deepEqual(api.calls[1]!.body, {notInterested: false});
});

test('a refusal rejects with the server\'s code, so the card comes back', async () => {
  const refused = Object.assign(new Error('gone'), {status: 404, code: 'not_found'});
  const api = fakeApi(() => {throw refused;});
  await assert.rejects(setNotInterested(api, {itemId: 'm1', notInterested: true}), (e: {code?: string}) => e.code === 'not_found');
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
