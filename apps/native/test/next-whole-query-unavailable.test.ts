import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {nativeWholeQueryUnavailableActions} from '../src-next/screens/library/whole-query-unavailable';

const every = {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: true, edit: true, refresh: true, delete: true};
const state = (count: number, mode = 'query') => ({target: {mode}, summary: {count, every, types: [{kind: 'movie', count}], library: {kind: 'single', libraryId: 'lib'}}});
test('large whole views keep truthful grants and unavailable Delete/Download controls with existing reasons', () => {
  const input = state(201), original = structuredClone(input), actions = nativeWholeQueryUnavailableActions(input as never);
  assert.deepEqual(actions, [{id: 'download', disabled: true, wholeQueryUnavailable: true, reason: defaultI18n.t('error.unavailable')}, {id: 'delete', disabled: true, wholeQueryUnavailable: true, reason: defaultI18n.t('next.selection.tooMany')}]);
  assert.deepEqual(input, original); assert.equal(input.summary.every.delete, true); assert.equal(input.summary.every.download, true);
});
test('bounded whole views and existing explicit behavior gain no new dispatcher or unavailable override', () => {
  for (const count of [0, 1, 199, 200]) assert.deepEqual(nativeWholeQueryUnavailableActions(state(count) as never), []);
  for (const count of [1, 200, 201, 50000]) assert.deepEqual(nativeWholeQueryUnavailableActions(state(count, 'explicit') as never), []);
  assert.deepEqual(nativeWholeQueryUnavailableActions(undefined), []);
});
test('an unavailable producer never invents a capability the server did not grant', () => {
  const input = {...state(50000), summary: {...state(50000).summary, every: {...every, delete: false}}};
  assert.deepEqual(nativeWholeQueryUnavailableActions(input as never).map(action => action.id), ['download']);
  input.summary.every.download = false;
  assert.deepEqual(nativeWholeQueryUnavailableActions(input as never), []);
});
