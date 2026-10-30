import assert from 'node:assert/strict';
import test from 'node:test';
import {readingPreferencesPatch} from '../src/preference-effects.ts';
import {parsePreferenceSnapshot, type PreferenceField} from '../src/preferences.ts';

const fields: readonly PreferenceField[] = [
  {key: 'reading.theme', type: 'string', default: 'dark', allowedValues: ['dark', 'black', 'sepia', 'light'], scopes: ['profile-server'], group: 'reading', labelKey: 'preferences.reading.theme'},
  {key: 'reading.textSize', type: 'integer', default: 5, allowedValues: [1, 2, 3, 4, 5, 6, 7], scopes: ['profile-server'], group: 'reading', labelKey: 'preferences.reading.textSize'},
  {key: 'reading.pdfView', type: 'string', default: 'automatic', allowedValues: ['automatic', 'spread', 'width', 'page'], scopes: ['profile-server'], group: 'reading', labelKey: 'preferences.reading.pdfView'},
  {key: 'comics.direction', type: 'string', default: 'file', allowedValues: ['file', 'ltr', 'rtl', 'vertical'], scopes: ['profile-server'], group: 'comics', labelKey: 'preferences.comics.direction'},
];

function snapshot(registryFields = fields) {
  return parsePreferenceSnapshot({deviceClass: 'web', registryRevision: 'reading.fixture', registry: {revision: 'reading.fixture', fields: registryFields},
    documents: ['profile-server', 'profile-device-class'].map(scope => ({scope, revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {}})),
    effective: Object.fromEntries(registryFields.map(field => [field.key, field.default])),
    effectiveSource: Object.fromEntries(registryFields.map(field => [field.key, 'default'])), clampedFields: []});
}

test('Reading patches only submitted facade properties and preserves null as clearing', () => {
  const before = snapshot();
  const patch = readingPreferencesPatch(before, {theme: 'sepia', comicDirection: 'rtl', pdfView: null});
  assert.deepEqual(patch, {'reading.theme': 'sepia', 'comics.direction': 'rtl', 'reading.pdfView': null});
  assert.equal(Object.isFrozen(patch), true);
  assert.equal(before.effective['reading.theme'], 'dark');
  assert.deepEqual(readingPreferencesPatch(before, {}), {});
});

test('Reading patches validate the passed registry, scope and domain without extra keys', () => {
  const before = snapshot();
  for (const change of [{theme: 'neon'}, {textSize: 8}, {textSize: NaN}, {textSize: 1.5}, {pdfView: undefined}, {warp: true}, {'reading.theme': 'sepia'}]) {
    assert.throws(() => readingPreferencesPatch(before, change as never));
  }
  const narrower = snapshot(fields.map(field => field.key === 'reading.theme' ? {...field, allowedValues: ['dark', 'light']} : field));
  assert.throws(() => readingPreferencesPatch(narrower, {theme: 'sepia'}));
  const bounded = snapshot(fields.map(field => field.key === 'reading.textSize' ? {...field, min: 2, max: 6} : field));
  assert.throws(() => readingPreferencesPatch(bounded, {textSize: 1}));
  assert.throws(() => readingPreferencesPatch(bounded, {textSize: 7}));
  const wrongScope = snapshot(fields.map(field => field.key === 'reading.theme' ? {...field, scopes: ['profile-device-class'] as const} : field));
  assert.throws(() => readingPreferencesPatch(wrongScope, {theme: null}));
  const missing = snapshot(fields.filter(field => field.key !== 'reading.theme'));
  assert.throws(() => readingPreferencesPatch(missing, {theme: null}));
});
