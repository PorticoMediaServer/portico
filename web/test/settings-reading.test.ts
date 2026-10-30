import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {parsePreferenceSnapshot, type PreferenceSnapshot} from '../../packages/client-core/src/preferences.ts';
import {settingsSpec, type SettingsRow} from '@core/presentation/settings-spec.ts';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {readingSettingsAdapter} from '../src/screens/settings/reading-adapter.ts';

const recorded = JSON.parse(readFileSync(new URL('../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences;
const base = parsePreferenceSnapshot(recorded);
function drawnRows(): SettingsRow[] {
  const section = settingsSpec.find(candidate => candidate.scope === 'account' && candidate.id === 'reading');
  assert.ok(section, 'account/reading section');
  return (section.groups ?? []).flatMap(group => group.rows);
}
function override(key: string, value: string | number | boolean): PreferenceSnapshot {
  return {...base, effective: {...base.effective, [key]: value}, effectiveSource: {...base.effectiveSource, [key]: 'profile-server'}};
}

test('all ten drawn Reading rows read through the genuine registry, never a generic binding', () => {
  const rows = drawnRows();
  assert.deepEqual(rows.map(row => row.id), ['reading.theme', 'reading.font', 'reading.textSize', 'reading.lineSpacing', 'reading.margins',
    'reading.alignment', 'reading.layout', 'reading.twoPages', 'comics.fit', 'comics.direction']);
  for (const row of rows) {
    assert.equal(settingsBinding(row.id), undefined, row.id);
    const value = readingSettingsAdapter.read(row.id, base);
    assert.equal(value.source, 'default', row.id);
    assert.deepEqual(value.current, value.default, row.id);
  }
});

test('a stored Reading choice reports its current value and the drawn registry default separately', () => {
  const theme = readingSettingsAdapter.read('reading.theme', override('reading.theme', 'sepia'));
  assert.equal(theme.current, 'sepia');
  assert.equal(theme.default, 'dark');
  assert.equal(theme.source, 'profile-server');
  const size = readingSettingsAdapter.read('reading.textSize', override('reading.textSize', 6));
  assert.equal(size.current, 6);
  assert.equal(size.default, 5);
  const twoPages = readingSettingsAdapter.read('reading.twoPages', override('reading.twoPages', false));
  assert.equal(twoPages.current, false);
  assert.equal(twoPages.default, true);
});

test('changes and resets target only the canonical Reading preference key', () => {
  assert.deepEqual(readingSettingsAdapter.patch('reading.theme', 'light', base), {'reading.theme': 'light'});
  assert.deepEqual(readingSettingsAdapter.patch('reading.theme', null, base), {'reading.theme': null});
  assert.deepEqual(readingSettingsAdapter.patch('comics.direction', 'rtl', base), {'comics.direction': 'rtl'});
  assert.deepEqual(readingSettingsAdapter.patch('reading.textSize', 7, base), {'reading.textSize': 7});
});

test('unknown Reading rows fail closed instead of inventing a key', () => {
  assert.throws(() => readingSettingsAdapter.read('reading.notASetting', base), /unknown_reading_setting/);
  assert.throws(() => readingSettingsAdapter.patch('reading.notASetting', 1, base), /unknown_reading_setting/);
});
