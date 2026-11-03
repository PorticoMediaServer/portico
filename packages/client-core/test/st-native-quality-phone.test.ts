import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsStructure, type SettingsPage, type SettingsRow} from '../src/presentation/settings-structure.ts';

// ST-6: the phone, where cellular playback and downloads happen, sets both of their saved qualities.
const keys = (platform: 'phone' | 'tv' | 'web') => {
  const page = settingsStructure({owner: false, platform, capabilities: {}}).headings.flatMap(heading => heading.pages).find(candidate => candidate.id === 'quality') as SettingsPage;
  return page.sections.flatMap(section => section.rows).filter((row): row is Extract<SettingsRow, {kind: 'preference'}> => row.kind === 'preference');
};

test('phone Quality offers On mobile data and Download quality, each kept per kind of device', () => {
  const rows = keys('phone');
  for (const key of ['quality.mobile', 'quality.downloads']) {
    const row = rows.find(value => value.key === key);
    assert.ok(row, key); assert.equal(row!.scope, 'deviceClass');
    assert.deepEqual((row!.control as {choices: unknown[]}).choices, (rows.find(value => value.key === 'quality.home')!.control as {choices: unknown[]}).choices, 'the same ladder as the home and away rows');
  }
});

test('TV has neither: no cellular data and no downloads', () => {
  assert.equal(keys('tv').some(row => row.key === 'quality.mobile' || row.key === 'quality.downloads'), false);
});
