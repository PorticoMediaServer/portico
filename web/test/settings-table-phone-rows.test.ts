import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {settingsTablePhoneMetrics, settingsTablePhoneRowHeight, settingsTableWindow} from '../src/kit/settings/table-model.ts';

const css = readFileSync(new URL('../src/kit/settings/settings.module.css', import.meta.url), 'utf8');
const phone = css.slice(css.indexOf('/* Phone width (owner-people 24a, 33a)'));

test('at phone width a settings table draws each row as a card (owner-people 24a), not the squeezed desktop table', () => {
  assert.doesNotMatch(css, /\.table \{ min-width: 720px; \}/);
  assert.match(phone, /\.tableScroll \.table thead \{ position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset\(50%\); \}/);
  assert.match(phone, /\.tableScroll \.table tr\[data-table-row\] \{ display: grid; grid-template-columns: minmax\(0, max-content\) auto minmax\(0, 1fr\) auto;[^}]*height: 112px;/);
  assert.match(phone, /td\[data-kind='badge'\] \{ grid-column: 2; grid-row: 1;/);
  assert.match(phone, /td:has\(> input\[type='checkbox'\]\) \{ display: none; \}/);
  assert.match(phone, /td\.actionCell \{ grid-column: 4;/);
});

test('the phone row height is what windowing uses, so long lists render the right rows', () => {
  assert.equal(settingsTablePhoneRowHeight, 112);
  assert.deepEqual(settingsTablePhoneMetrics, {row: 112, header: 0});
  // 1,000 people scrolled to row 500 at 112px: the window starts three rows before it.
  const window = settingsTableWindow(1000, 500 * 112, 560, 3, settingsTablePhoneMetrics);
  assert.equal(window.first, 497);
  assert.equal(window.before, 497 * 112);
  const table = readFileSync(new URL('../src/kit/settings/SettingsTable.tsx', import.meta.url), 'utf8');
  assert.match(table, /const metrics = usePhone\(\) \? settingsTablePhoneMetrics : settingsTableMetrics;/);
  assert.match(table, /const top = index \* metrics\.row, bottom = top \+ metrics\.row \+ metrics\.header;/);
});
