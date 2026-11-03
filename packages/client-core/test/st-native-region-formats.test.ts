import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {settingsStructure, type SettingsPage, type SettingsRow} from '../src/presentation/settings-structure.ts';

// ST-4: the native Region page draws the four format rows the native runtime applies, with every published value.
const go = readFileSync(new URL('../../../server/internal/operations/preferences.go', import.meta.url), 'utf8');
const published = (key: string) => {
  const match = go.match(new RegExp('prefEnum\\("' + key.replace('.', '\\.') + '",\\s*"[^"]*",\\s*\\[\\]string\\{([^}]*)\\}'));
  return match ? [...match[1]!.matchAll(/"([^"]*)"/g)].map(entry => entry[1]!) : undefined;
};

for (const platform of ['phone', 'tv'] as const) test(`${platform}: Region offers formats, date order, first day and size units`, () => {
  const page = settingsStructure({owner: false, platform, capabilities: {}}).headings.flatMap(heading => heading.pages).find(candidate => candidate.id === 'region') as SettingsPage;
  const rows = page.sections.flatMap(section => section.rows) as Extract<SettingsRow, {kind: 'preference'}>[];
  const choices = (key: string) => (rows.find(row => row.key === key)?.control as {choices?: {value: string}[]} | undefined)?.choices?.map(choice => choice.value);
  for (const key of ['region.dateOrder', 'region.firstDay', 'region.sizeUnits']) assert.deepEqual(choices(key), published(key), key);
  assert.equal(choices('region.formats')?.[0], 'auto');
  assert.equal(rows.some(row => row.key === 'region.timeZone'), false, 'no time zone: every client uses the device zone');
});
