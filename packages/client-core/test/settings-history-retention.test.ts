import test from 'node:test';
import assert from 'node:assert/strict';
import {readSettingsBinding, settingsBinding, settingsBindingPatch} from '../src/presentation/settings-bindings.ts';
import {parseSettingsConfiguration} from '../src/settings-configuration.ts';
import {settingsSpec} from '../src/presentation/settings-spec.ts';

const binding = settingsBinding('history.keepHistory')!;
const choices = [['forever', 0], ['3y', 1095], ['1y', 365], ['180d', 180], ['90d', 90], ['30d', 30]] as const;
test('every published History retention choice round-trips through the server day field', () => {
  const page = settingsSpec.find(page => page.scope === 'server' && page.id === 'history')!;
  const row = page.advanced!.groups!.flatMap(group => group.rows).find(row => row.id === 'history.keepHistory')!;
  assert.equal(row.control.type, 'select');
  if (row.control.type !== 'select') throw new Error('History retention must be a select.');
  assert.deepEqual(row.control.options!.map(option => option.value), choices.map(([value]) => value));
  for (const [value, days] of choices) {
    const patch = settingsBindingPatch(binding, value);
    assert.deepEqual(patch, {form: 'runtime', values: {playHistoryDays: days}});
    assert.equal(readSettingsBinding(binding, {forms: {runtime: patch.values}})?.current, value);
    assert.equal(settingsBindingPatch(binding, readSettingsBinding(binding, {forms: {runtime: {playHistoryDays: days}}})!.current).values.playHistoryDays, days);
  }
});
test('History configuration maps current, effective and default independently', () => {
  const configuration = parseSettingsConfiguration({revision: 1, registryRevision: 'test', path: '/config/portico.toml', fields: [
    {id: 'history.keepHistory', key: 'history.keep_play_history', current: 1095, effective: 365, default: 0, source: 'file', configOnly: true, applicationMode: 'hot'},
  ]});
  assert.deepEqual(readSettingsBinding(binding, {configuration}, 'history.keepHistory'),
    {current: '3y', effective: '1y', default: 'forever', source: 'file', configOnly: true, applicationMode: 'hot'});
});
test('an unknown stored retention is preserved exactly rather than rounded to a preset', () => {
  for (const days of [17, 36500]) {
    const value = readSettingsBinding(binding, {forms: {runtime: {playHistoryDays: days}}})!.current;
    assert.equal(value, String(days));
    assert.deepEqual(settingsBindingPatch(binding, value), {form: 'runtime', values: {playHistoryDays: days}});
  }
  // Numeric input used by existing callers still targets the same server field.
  assert.equal(settingsBindingPatch(binding, 1095).values.playHistoryDays, 1095);
  for (const value of ['', 'custom', '2y', '__proto__', null, false, -1, 1.5, 36501, NaN, Infinity]) assert.throws(() => settingsBindingPatch(binding, value));
  for (const value of [-1, 1.5, 36501, '30', null]) assert.equal(readSettingsBinding(binding, {forms: {runtime: {playHistoryDays: value}}}), undefined);
});
