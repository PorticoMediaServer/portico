import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import type {BrowseFieldCapability} from '../src/browse.ts';
import {appliedFilterLabel, fieldSelectionSummary, rangePresets, rangeSelectionLabel} from '../src/presentation/browse-toolbar.ts';
import {appliedFilterParts} from '../src/presentation/kit-model/filters.ts';

const t = defaultI18n.t;
const duration = {id: 'durationMs', type: 'number', operators: ['between', 'at-least', 'at-most'], labelKey: 'browse.field.durationSeconds'} as unknown as BrowseFieldCapability;
const raw = (text: string | undefined) => /\d{5,}/.test(text ?? '');

test('H10: every Duration preset reads as its own words in the field, the chip and the applied label', () => {
  for (const preset of rangePresets(duration, new Date())) {
    const words = t(preset.label.id, preset.label.values);
    assert.equal(fieldSelectionSummary(duration, preset.predicates, String, t), words, preset.id);
    for (const predicate of preset.predicates) {
      const chip = appliedFilterParts(predicate, () => 'Duration', {t});
      assert.equal(raw(chip.values.map(value => value.text).join(' ')), false, preset.id);
      assert.equal(raw(appliedFilterLabel(predicate, () => 'Duration', t)), false, preset.id);
    }
  }
  assert.equal(fieldSelectionSummary(duration, [{field: 'durationMs', operator: 'between', value: [3600000, 5400000]}], String, t), '60–90 min');
  assert.equal(appliedFilterLabel({field: 'durationMs', operator: 'between', value: [3600000, 5400000]}, () => 'Duration', t), 'Duration: 60–90 min');
});

test('H10: a range not drawn as a preset still says minutes, stars and percent, never the wire value', () => {
  assert.equal(rangeSelectionLabel('durationMs', [{field: 'durationMs', operator: 'at-least', value: 2700000}], t), 'Over 45 min');
  assert.equal(rangeSelectionLabel('personalRating', [{field: 'personalRating', operator: 'at-least', value: 8}], t), t('filter.preset.stars' as never, {count: 4}));
  assert.equal(rangeSelectionLabel('criticRating', [{field: 'criticRating', operator: 'at-least', value: 75}], t), '75% or higher');
  // Fields without a unit keep their ordinary wording.
  assert.equal(rangeSelectionLabel('year', [{field: 'year', operator: 'at-least', value: 1990}], t), undefined);
  assert.equal(rangeSelectionLabel('durationMs', [{field: 'durationMs', operator: 'equals', value: 1}], t), undefined);
});
