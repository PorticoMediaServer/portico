import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex, settingsSpec, type SettingsRow} from '@core/presentation/settings-spec.ts';
import {settingsBinding, settingsBindingPatch} from '@core/presentation/settings-bindings.ts';
import type {PreferenceField, PreferenceSnapshot} from '../../packages/client-core/src/preferences.ts';
import {hasWebPreferenceEffect} from '../src/screens/settings/effects.ts';

function section(): {groups: readonly {rows: readonly SettingsRow[]}[]} {
  const found = settingsSpec.find(candidate => candidate.scope === 'account' && candidate.id === 'playback');
  assert.ok(found, 'account/playback section');
  return {groups: found.groups ?? []};
}
const row = (id: string) => {
  const location = settingsRowIndex.get(id);
  assert.ok(location, id);
  return location.row;
};
const fields: readonly PreferenceField[] = [
  {key: 'playback.passoutProtection', type: 'boolean', default: true, scopes: ['profile-server'], group: 'playback', labelKey: 'x'},
  {key: 'playback.passoutAfterEpisodes', type: 'integer', default: 3, min: 2, max: 5, scopes: ['profile-server'], group: 'playback', labelKey: 'x'},
  ...['intro', 'credits', 'recap'].map(name => ({key: `playback.${name}Skip`, type: 'string' as const, default: 'ask', allowedValues: ['ask', 'auto', 'off'], scopes: ['profile-server' as const], group: 'playback', labelKey: 'x'})),
];
function snapshot(): PreferenceSnapshot {
  return {deviceClass: 'web', registry: {revision: '1', fields},
    documents: [{scope: 'profile-server', revision: 1, digest: 'digest', activeRevision: 1, values: {}}],
    effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: '1'};
}

test('the drawn Next episode and Skipping groups keep the authored rows and children', () => {
  const [next, skipping] = section().groups;
  assert.deepEqual(next!.rows.map(entry => entry.id), ['playback.autoplayNext', 'playback.stillWatching']);
  assert.deepEqual(skipping!.rows.map(entry => entry.id), ['playback.skipping', 'playback.skipBackMs', 'playback.skipForwardMs']);
  assert.deepEqual(next!.rows[0]!.children?.map(child => child.id), ['playback.upNextCountdownMs', 'playback.startNextDuringCredits']);
  for (const child of next!.rows[0]!.children ?? []) assert.deepEqual(child.enableWhen, {id: 'playback.autoplayNext', in: [true]});
  assert.deepEqual(next!.rows[1]!.control.options?.map(option => option.value), [0, 2, 3, 4, 5]);
  assert.deepEqual(skipping!.rows[0]!.control.columns?.map(column => column.value), ['ask', 'auto', 'off']);
  assert.deepEqual(skipping!.rows[0]!.control.items?.map(item => [item.id, item.default]), [['intro', 'ask'], ['credits', 'ask'], ['recap', 'ask']]);
  assert.deepEqual(skipping!.rows[1]!.control.options?.map(option => option.value), [5000, 10000, 15000, 30000]);
  assert.deepEqual(skipping!.rows[2]!.control.options?.map(option => option.value), [10000, 15000, 30000, 60000]);
});

test('every drawn row is exposed through the real profile-server binding and web consumer', () => {
  for (const id of ['playback.autoplayNext', 'playback.upNextCountdownMs', 'playback.startNextDuringCredits', 'playback.stillWatching', 'playback.skipping', 'playback.skipBackMs', 'playback.skipForwardMs']) {
    assert.equal(settingsBinding(id)?.form, 'preferences', id);
    assert.equal(settingsBinding(id)?.scope, 'profile-server', id);
    assert.equal(hasWebPreferenceEffect(row(id)), true, id);
  }
});

test('Still watching writes the actual protection toggle and episode count', () => {
  const binding = settingsBinding('playback.stillWatching')!;
  const sources = {preferences: snapshot()};
  assert.deepEqual(settingsBindingPatch(binding, 0, sources), {form: 'preferences', values: {'playback.passoutProtection': false}});
  assert.deepEqual(settingsBindingPatch(binding, 4, sources), {form: 'preferences', values: {'playback.passoutProtection': true, 'playback.passoutAfterEpisodes': 4}});
  for (const invalid of [1, 6, '3', true]) assert.throws(() => settingsBindingPatch(binding, invalid, sources));
});

test('the Skipping matrix writes exactly its three skip modes', () => {
  const binding = settingsBinding('playback.skipping')!;
  const sources = {preferences: snapshot()};
  assert.deepEqual(settingsBindingPatch(binding, {intro: 'auto', credits: 'off', recap: 'ask'}, sources), {form: 'preferences',
    values: {'playback.introSkip': 'auto', 'playback.creditsSkip': 'off', 'playback.recapSkip': 'ask'}});
  assert.throws(() => settingsBindingPatch(binding, {intro: 'never', credits: 'off', recap: 'ask'}, sources));
  assert.throws(() => settingsBindingPatch(binding, {intro: 'ask', credits: 'off'}, sources));
});
