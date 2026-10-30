import assert from 'node:assert/strict';
import test from 'node:test';
import {nativeSelectionCommand, nativeSelectionOffers} from '../src-next/shell/selection-actions';
import type {SelectionActionRules} from '../../../packages/client-core/src/selection-model/index';

function rules(overrides: Partial<SelectionActionRules> = {}): SelectionActionRules {
  return {...Object.fromEntries(['play', 'play-next', 'play-later', 'playlist', 'my-list', 'mark-watched', 'mark-unwatched', 'collection', 'favorite', 'remove-my-list', 'edit', 'download', 'refresh', 'delete'].map(action => [action, {state: 'hidden'}])), ...overrides} as SelectionActionRules;
}

test('native selection maps renderer names to canonical commands without granting missing actions', () => {
  assert.equal(nativeSelectionCommand('myList'), 'my-list');
  assert.equal(nativeSelectionCommand('playNext'), 'play-next');
  assert.equal(nativeSelectionCommand('watched'), 'mark-watched');
  assert.equal(nativeSelectionCommand('more'), undefined);
  assert.deepEqual(nativeSelectionOffers(rules(), value => value), []);
});

test('mixed-type edit remains an executable explanation while denied actions stay disabled', () => {
  const offers = nativeSelectionOffers(rules({edit: {state: 'explain', reason: 'mixed-types'}, delete: {state: 'disabled', reason: 'one-library-required'}}), value => value);
  assert.deepEqual(offers.find(offer => offer.id === 'edit'), {id: 'edit', reason: 'mixed-types'});
  assert.deepEqual(offers.find(offer => offer.id === 'delete'), {id: 'delete', disabled: true, reason: 'one-library-required'});
  assert.equal(offers.some(offer => offer.id === 'more'), true);
});

test('queue exists only for actual supplied insertion offers and preserves their individual gates', () => {
  const offers = nativeSelectionOffers(rules({'play-next': {state: 'disabled', reason: 'not-permitted'}, 'play-later': {state: 'ready'}}), value => value);
  assert.equal(offers.find(offer => offer.id === 'playNext')?.disabled, true);
  assert.equal(offers.find(offer => offer.id === 'playLater')?.disabled, undefined);
  assert.deepEqual(offers.find(offer => offer.id === 'queue'), {id: 'queue'});
  assert.equal(offers.some(offer => offer.id === 'play'), false);
});
