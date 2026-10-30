import {test} from 'node:test';
import assert from 'node:assert/strict';
import {activateCard, boundedProgress} from '../../src/kit/cards/interaction.ts';
import type {CardSelection, CardSelectionIntent} from '../../src/kit/cards/types.ts';

test('normal activation navigates without using a separate playback identity', () => {
  const called: string[] = [];
  const navigation = () => called.push('show:1');
  const play = () => called.push('episode:9');
  activateCard(navigation, undefined, {range: false, source: 'pointer'});
  assert.deepEqual(called, ['show:1']);
  play();
  assert.deepEqual(called, ['show:1', 'episode:9']);
});

test('Shift activation enters the real selection callback and preserves range intent', () => {
  const selected: CardSelectionIntent[] = [];
  let navigated = false;
  const selection: CardSelection = {active: false, checked: false, control: null, onToggle: intent => selected.push(intent), onExit: () => {}};
  activateCard(() => { navigated = true; }, selection, {range: true, source: 'pointer'});
  assert.equal(navigated, false);
  assert.deepEqual(selected, [{range: true, source: 'pointer'}]);
});

test('selection mode routes keyboard activation to the controlled owner without navigating', () => {
  const selected: CardSelectionIntent[] = [];
  let navigated = false;
  const selection: CardSelection = {active: true, checked: true, control: null, onToggle: intent => selected.push(intent), onExit: () => {}};
  activateCard(() => { navigated = true; }, selection, {range: false, source: 'keyboard'});
  assert.equal(navigated, false);
  assert.deepEqual(selected, [{range: false, source: 'keyboard'}]);
});

test('unknown and invalid progress cannot invent a resume state', () => {
  assert.equal(boundedProgress(undefined), undefined);
  assert.equal(boundedProgress(NaN), undefined);
  assert.equal(boundedProgress(Infinity), undefined);
  assert.equal(boundedProgress(0), 0);
  assert.equal(boundedProgress(-.5), 0);
  assert.equal(boundedProgress(2), 1);
  assert.equal(boundedProgress(.42), .42);
});
