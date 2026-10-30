import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeSelectionActionUnavailable, nativeSelectionUnavailableActions, nativeSelectionUnavailableReason} from '../src-next/screens/library/selection-unavailable-actions';

const unavailableActions = {play: 'unsupported-sort', queue: 'unsupported-sort', metadataPreview: 'preview-limit'} as const;
const summary = {every: {play: true, queue: true, edit: true}, unavailableActions};
test('native maps only offered actions to the shared short reason and retains the Queue explanation menu', () => {
  const input = [{id: 'play'}, {id: 'queue'}, {id: 'playNext'}, {id: 'playLater'}, {id: 'edit'}, {id: 'myList'}] as const;
  const actions = nativeSelectionUnavailableActions(input, summary as never);
  const sort = nativeSelectionUnavailableReason('unsupported-sort'), preview = nativeSelectionUnavailableReason('preview-limit');
  assert.equal(sort, 'Unavailable with this sort order.');
  assert.equal(preview, 'Metadata preview supports up to 200 titles for this selection.');
  assert.deepEqual(actions.slice(0, 5), [{id: 'play', disabled: true, reason: sort}, {id: 'queue', reason: sort}, {id: 'playNext', disabled: true, reason: sort}, {id: 'playLater', disabled: true, reason: sort}, {id: 'edit', disabled: true, reason: preview}]);
  assert.deepEqual(actions[5], {id: 'myList'});assert.deepEqual(input[0], {id: 'play'});
  assert.deepEqual(nativeSelectionUnavailableActions([{id: 'edit', disabled: true, reason: 'Permission required'}], summary as never), [{id: 'edit', disabled: true, reason: 'Permission required'}]);
  assert.deepEqual(nativeSelectionUnavailableActions([], summary as never), [], 'never reveal an authority-hidden or unsupported surface action');
  assert.deepEqual(nativeSelectionUnavailableActions(input, {every: summary.every} as never), input);
});
test('native retained callbacks refuse each excluded consumer without changing grants', () => {
  for (const action of ['play', 'playNext', 'playLater', 'edit'] as const) {
    assert.equal(nativeSelectionActionUnavailable(summary as never, action), true);
    assert.equal(nativeSelectionActionUnavailable({every: summary.every} as never, action), false);
  }
  assert.deepEqual(summary.every, {play: true, queue: true, edit: true});
});
test('shared native offers keep unsupported Queue as an explanation menu while authority-disabled Queue stays disabled', async () => {
  const {nativeSelectionOffers} = await import('../src-next/shell/selection-actions');
  const names = ['play', 'play-next', 'play-later', 'playlist', 'my-list', 'mark-watched', 'mark-unwatched', 'collection', 'favorite', 'remove-my-list', 'edit', 'download', 'refresh', 'delete'];
  const rules = Object.fromEntries(names.map(name => [name, {state: 'hidden'}]));
  for (const name of ['play-next', 'play-later']) rules[name] = {state: 'disabled', reason: 'unsupported-sort'} as never;
  const actions = nativeSelectionOffers(rules as never, value => value === 'unsupported-sort' || value === 'preview-limit' ? nativeSelectionUnavailableReason(value) : value);
  assert.deepEqual(actions, [{id: 'playNext', disabled: true, reason: 'Unavailable with this sort order.'}, {id: 'playLater', disabled: true, reason: 'Unavailable with this sort order.'}, {id: 'queue', reason: 'Unavailable with this sort order.'}]);
  for (const name of ['play-next', 'play-later']) rules[name] = {state: 'disabled', reason: 'not-permitted'} as never;
  assert.equal(nativeSelectionOffers(rules as never, value => value).find(action => action.id === 'queue')?.disabled, true);
});
test('Playlist exclusion uses the existing sort reason only for an offered action and blocks retained dispatch', async () => {
  const summary = {every: {playlist: true}, unavailableActions: {playlist: 'unsupported-sort'}} as const;
  const actions = nativeSelectionUnavailableActions([{id: 'playlist'}], summary as never);
  assert.deepEqual(actions, [{id: 'playlist', disabled: true, reason: 'Unavailable with this sort order.'}]);
  assert.equal(nativeSelectionActionUnavailable(summary as never, 'playlist'), true);
  assert.deepEqual(nativeSelectionUnavailableActions([], summary as never), []);
  assert.deepEqual(nativeSelectionUnavailableActions([{id: 'playlist', disabled: true, reason: 'Permission required'}], summary as never), [{id: 'playlist', disabled: true, reason: 'Permission required'}]);
  assert.deepEqual(nativeSelectionUnavailableActions([{id: 'playlist'}], {every: summary.every} as never), [{id: 'playlist'}]);
  assert.equal(nativeSelectionActionUnavailable({every: summary.every} as never, 'playlist'), false);
  const {nativeSelectionOffers} = await import('../src-next/shell/selection-actions');
  const names = ['play', 'play-next', 'play-later', 'playlist', 'my-list', 'mark-watched', 'mark-unwatched', 'collection', 'favorite', 'remove-my-list', 'edit', 'download', 'refresh', 'delete'];
  const rules = Object.fromEntries(names.map(name => [name, {state: 'hidden'}]));rules.playlist = {state: 'disabled', reason: 'unsupported-sort'} as never;
  assert.deepEqual(nativeSelectionOffers(rules as never, () => nativeSelectionUnavailableReason('unsupported-sort')), [{id: 'playlist', disabled: true, reason: 'Unavailable with this sort order.'}]);
});
test('Delete exclusion maps the offered bounded control without replacing authority or the large-view reason', async () => {
  const summary = {count: 200, every: {delete: true}, unavailableActions: {delete: 'unsupported-sort'}} as const;
  assert.deepEqual(nativeSelectionUnavailableActions([{id: 'delete'}], summary as never), [{id: 'delete', disabled: true, reason: 'Unavailable with this sort order.'}]);
  assert.equal(nativeSelectionActionUnavailable(summary as never, 'delete'), true);
  assert.deepEqual(nativeSelectionUnavailableActions([], summary as never), []);
  const denied = [{id: 'delete', disabled: true, reason: 'Permission required'}] as const;
  assert.deepEqual(nativeSelectionUnavailableActions(denied, summary as never), denied);
  const limit = [{id: 'delete', disabled: true, reason: 'Delete at most 200 titles.'}] as const;
  assert.deepEqual(nativeSelectionUnavailableActions(limit, {...summary, count: 201} as never), limit);
  assert.equal(nativeSelectionActionUnavailable({...summary, count: 201} as never, 'delete'), false);
  assert.deepEqual(nativeSelectionUnavailableActions([{id: 'delete'}], {count: 200, every: summary.every} as never), [{id: 'delete'}]);
  const {nativeWholeQueryUnavailableActions} = await import('../src-next/screens/library/whole-query-unavailable');
  const large = nativeWholeQueryUnavailableActions({target: {mode: 'query'}, summary: {...summary, count: 201}} as never);
  assert.equal(large[0]?.id, 'delete');assert.equal(large[0]?.disabled, true);assert.notEqual(large[0]?.reason, 'Unavailable with this sort order.');
});
