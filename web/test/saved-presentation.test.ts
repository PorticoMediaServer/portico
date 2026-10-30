import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {parsePreferenceSnapshot, type PreferenceValue} from '@core/preferences.ts';
import {savedPresentation} from '../src/screens/saved/presentation.ts';

const wire = JSON.parse(await readFile(new URL('../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences;
const snapshot = (values: Record<string, PreferenceValue> = {}) => parsePreferenceSnapshot({...structuredClone(wire), effective: {...wire.effective, ...values}});

test('Saved consumes the published appearance facade and changes only on a new canonical snapshot', () => {
  let current = true;
  const writes: string[] = [];
  const input = {baseCardWidth: 172, systemReducedMotion: false, isCurrent: () => current, setView: (view: 'grid' | 'list') => { writes.push(view); }};
  const before = savedPresentation(snapshot(), input);
  assert.equal(before.view, 'grid');
  assert.equal(before.cardWidth, 172);
  assert.equal(before.backdrops, true);
  assert.equal(before.reducedMotion, false);
  before.setView('list');
  assert.deepEqual(writes, ['list']);
  assert.equal(before.view, 'grid', 'a requested write is not a committed preference');

  const committed = savedPresentation(snapshot({'appearance.libraryView': 'list', 'appearance.cardSizePercent': 125, 'appearance.showBackdrops': false, 'appearance.motion': 'reduce'}), input);
  assert.equal(committed.view, 'list');
  assert.equal(committed.cardWidth, 215);
  assert.equal(committed.backdrops, false);
  assert.equal(committed.backdrop, undefined);
  assert.equal(committed.reducedMotion, true);
  assert.equal(Object.isFrozen(committed), true);
  current = false;
  before.setView('list'); committed.setView('grid');
  assert.deepEqual(writes, ['list'], 'retired parent callbacks never write into another viewer');
});

test('Saved respects system reduced motion and requires actual width and published preferences', () => {
  const input = {baseCardWidth: 172, systemReducedMotion: true, isCurrent: () => true, setView() {}};
  assert.equal(savedPresentation(snapshot(), input).reducedMotion, true);
  for (const baseCardWidth of [0, -1, NaN, Infinity]) assert.throws(() => savedPresentation(snapshot(), {...input, baseCardWidth}));
  const missing = structuredClone(wire);
  delete missing.effective['appearance.libraryView'];
  missing.registry.fields = missing.registry.fields.filter((field: {key: string}) => field.key !== 'appearance.libraryView');
  delete missing.effectiveSource['appearance.libraryView'];
  assert.throws(() => savedPresentation(parsePreferenceSnapshot(missing), input), /does not publish/);
});
