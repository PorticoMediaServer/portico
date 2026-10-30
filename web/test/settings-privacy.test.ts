import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex, settingsSpec, type SettingsRow} from '@core/presentation/settings-spec.ts';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {hasWebPreferenceEffect} from '../src/screens/settings/effects.ts';
import {isPrivacyAction, privacyActionClaim, privacyActionRowIds, privacyAuthFailure, clearViewingHistory, runPrivacyAction} from '../src/screens/settings/privacy-actions.ts';

const row = (id: string) => {
  const location = settingsRowIndex.get(id);
  assert.ok(location, id);
  return location.row;
};
function sectionRows(): SettingsRow[] {
  const section = settingsSpec.find(candidate => candidate.scope === 'account' && candidate.id === 'privacy');
  assert.ok(section, 'account/privacy section');
  const flatten = (rows: readonly SettingsRow[]): SettingsRow[] => rows.flatMap(entry => [entry, ...flatten(entry.children ?? [])]);
  return [...(section.groups ?? []), ...(section.advanced?.groups ?? [])].flatMap(group => flatten(group.rows));
}

test('the four Privacy switches are exposed through their real server preferences', () => {
  const backed = {
    'privacy.pauseHistory': 'privacy.pauseWatchHistory',
    'privacy.showActivity': 'privacy.showActivityToMembers',
    'privacy.includeWatchTogether': 'privacy.includeInWatchTogether',
    'privacy.rememberSearches': 'search.rememberHistory',
  };
  for (const [rowId, key] of Object.entries(backed)) {
    const binding = settingsBinding(rowId);
    assert.equal(binding?.form, 'preferences', rowId);
    assert.equal(binding?.key, key, rowId);
    assert.equal(binding?.scope, 'profile-server', rowId);
    assert.equal(hasWebPreferenceEffect(row(rowId)), true, rowId);
  }
});

test('the Privacy level preset is offered only because all four targets have a real effect', () => {
  const level = row('privacy.level');
  assert.equal(level.control.options?.some(option => option.sets), true);
  assert.equal(hasWebPreferenceEffect(level), true);
  // The custom choice is derived from the four switches; it is not a fifth stored value.
  const custom = level.control.options?.find(option => option.value === 'custom');
  assert.ok(custom);
  assert.equal(custom.sets, undefined);
});

test('the section draws the two drawn groups and no invented row', () => {
  assert.deepEqual(sectionRows().map(entry => entry.id), [
    'privacy.level',
    'privacy.clearHistory', 'privacy.clearSearches', 'privacy.resetRecommendations',
    'privacy.showActivity', 'privacy.includeWatchTogether', 'privacy.pauseHistory', 'privacy.rememberSearches',
  ]);
});

test('only the three Clear data actions are claimed by the privacy surfaces', () => {
  const claimed = sectionRows().filter(entry => privacyActionClaim(entry.id)).map(entry => entry.id);
  assert.deepEqual(claimed, [...privacyActionRowIds]);
  assert.equal(privacyActionClaim('privacy.level'), undefined);
  assert.equal(privacyActionClaim('privacy.pauseHistory'), undefined);
  assert.equal(isPrivacyAction('privacy.clearHistory'), true);
  assert.equal(isPrivacyAction('privacy.level'), false);
});

type Call = Readonly<{path: string; method: string; body?: unknown}>;
function fakeApi(handler: (call: Call) => unknown) {
  const calls: Call[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    const call = {path, method, ...(body === undefined ? {} : {body})};
    calls.push(call);
    return handler(call) as T;
  }};
  return {api, calls};
}

test('Clear recent searches calls the checked delete route', async () => {
  const {api, calls} = fakeApi(() => undefined);
  await runPrivacyAction('privacy.clearSearches', {api, serverId: 's1', viewerId: 'v1', signal: new AbortController().signal});
  assert.deepEqual(calls, [{path: '/v1/search/history', method: 'DELETE'}]);
});

test('Reset recommendations posts one idempotent operation and checks the receipt', async () => {
  const {api, calls} = fakeApi(call => {
    const body = call.body as {operationId: string};
    assert.match(body.operationId, /^[A-Za-z0-9_-]{1,128}$/);
    return {serverId: 's1', operationId: body.operationId, clearedNotInterested: 2};
  });
  await runPrivacyAction('privacy.resetRecommendations', {api, serverId: 's1', viewerId: 'v1', signal: new AbortController().signal});
  assert.equal(calls.length, 1);
  assert.equal(calls[0]!.path, '/v1/me/recommendations:reset');
  assert.equal(calls[0]!.method, 'POST');
});

test('Clear viewing history reads the fenced revision before it clears it', async () => {
  let reads = 0;
  const {api, calls} = fakeApi(call => {
    if (call.path.startsWith('/v1/personal-history?') && call.method === 'GET') {
      reads += 1;
      return {serverId: 's1', viewerFence: 'fence-1', revision: 7, nextCursor: '', entries: []};
    }
    if (call.path === '/v1/personal-history/actions' && call.method === 'POST') {
      const body = call.body as {expectedRevision: number; action: string; operationId: string};
      assert.equal(body.expectedRevision, 7);
      assert.equal(body.action, 'clear-history');
      return {serverId: 's1', viewerFence: 'fence-1', receipt: {operationId: body.operationId, revision: 8}};
    }
    throw new Error(`unexpected ${call.method} ${call.path}`);
  });
  await runPrivacyAction('privacy.clearHistory', {api, serverId: 's1', viewerId: 'v1', signal: new AbortController().signal});
  assert.equal(reads, 2, 'one read before the clear and the service refresh after it');
  assert.deepEqual(calls.filter(call => call.method === 'POST').map(call => call.path), ['/v1/personal-history/actions']);
});

const historyDocument = {serverId: 's1', viewerFence: 'fence-1', revision: 7, nextCursor: '', entries: []};
const signalOptions = (signal: AbortSignal) => ({serverId: 's1', viewerId: 'v1', signal});

test('the history producer refusal codes are recognized as auth failures without a status', () => {
  for (const error of [{code: 'unauthorized'}, {code: 'forbidden'}, {code: 'not_permitted'}, {status: 401}, {status: 403}]) assert.equal(privacyAuthFailure(error), true, JSON.stringify(error));
  for (const error of [undefined, null, {code: 'internal'}, {status: 500}, new Error('denied')]) assert.equal(privacyAuthFailure(error), false, String(error));
});

test('a refused history read propagates its normalized unauthorized code', async () => {
  const {api, calls} = fakeApi(() => {throw Object.assign(new Error('denied'), {code: 'unauthorized'});});
  await assert.rejects(clearViewingHistory({api, ...signalOptions(new AbortController().signal)}), (error: unknown) => (error as {code?: string}).code === 'unauthorized');
  assert.deepEqual(calls.map(call => call.method), ['GET']);
});

test('a refused history clear propagates its normalized forbidden code after the fenced read', async () => {
  const {api, calls} = fakeApi(call => {
    if (call.method === 'GET') return historyDocument;
    throw Object.assign(new Error('denied'), {code: 'forbidden'});
  });
  await assert.rejects(clearViewingHistory({api, ...signalOptions(new AbortController().signal)}), (error: unknown) => (error as {code?: string}).code === 'forbidden');
  assert.deepEqual(calls.map(call => call.method), ['GET', 'POST']);
});

test('aborting during the history read retires the service before any clear', async () => {
  const controller = new AbortController();
  let posts = 0, reads = 0;
  const api = {request: <T>(_path: string, method = 'GET', _body?: unknown, signal?: AbortSignal) => {
    if (method === 'POST') {posts += 1; return Promise.resolve({} as T);}
    reads += 1;
    return new Promise<T>((_resolve, reject) => {
      const cancel = () => reject(Object.assign(new Error('cancelled'), {code: 'cancelled'}));
      if (signal?.aborted) cancel(); else signal?.addEventListener('abort', cancel, {once: true});
    });
  }};
  const pending = clearViewingHistory({api, ...signalOptions(controller.signal)});
  while (!reads) await new Promise(resolve => setTimeout(resolve, 1));
  controller.abort();
  await assert.rejects(pending, (error: unknown) => (error as Error).name === 'AbortError');
  assert.equal(posts, 0);
});

test('aborting a pending history clear cancels the mutation with AbortError', async () => {
  const controller = new AbortController();
  let postStarted: () => void = () => {};
  const started = new Promise<void>(resolve => {postStarted = resolve;});
  const api = {request: <T>(_path: string, method = 'GET', _body?: unknown, signal?: AbortSignal) => {
    if (method === 'GET') return Promise.resolve(historyDocument as T);
    postStarted();
    return new Promise<T>((_resolve, reject) => {
      const cancel = () => reject(Object.assign(new Error('cancelled'), {code: 'cancelled'}));
      if (signal?.aborted) cancel(); else signal?.addEventListener('abort', cancel, {once: true});
    });
  }};
  const pending = clearViewingHistory({api, ...signalOptions(controller.signal)});
  await started;
  controller.abort();
  await assert.rejects(pending, (error: unknown) => (error as Error).name === 'AbortError');
});
