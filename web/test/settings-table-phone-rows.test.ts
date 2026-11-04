import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {settingsTablePhoneMetrics, settingsTablePhoneRowHeight, settingsTableWindow} from '../src/kit/settings/table-model.ts';

const css = readFileSync(new URL('../src/kit/settings/settings.module.css', import.meta.url), 'utf8');
const phone = css.slice(css.indexOf('/* Phone width (owner-people 24a, 33a)'));
const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('at phone width a settings table draws each row as a card (owner-people 24a), not the squeezed desktop table', () => {
  assert.match(phone, /\[data-phone-cards\] \.tableScroll \.table thead \{ position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset\(50%\); \}/);
  assert.match(phone, /\[data-phone-cards\] \.tableScroll \.table tr\[data-table-row\] \{ display: grid; grid-template-columns: minmax\(0, max-content\) auto minmax\(0, 1fr\) auto;[^}]*height: var\(--phone-row-height, 112px\);/);
  assert.match(phone, /td\[data-kind='badge'\]:not\(td\[data-kind='badge'\] ~ td\) \{ grid-column: 2; grid-row: 1;/);
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
  assert.match(table, /const metrics = phone && cards \? \{row: cards\.rowHeight, header: settingsTablePhoneMetrics\.header\} : settingsTableMetrics;/);
  assert.match(table, /style=\{cards \? \{'--phone-row-height': `\$\{cards\.rowHeight\}px`\} as CSSProperties : undefined\}/);
  assert.match(table, /const top = index \* metrics\.row, bottom = top \+ metrics\.row \+ metrics\.header;/);
});

test('only People and Invitations draw cards; Play history and the settings grids keep the desktop table, scrolled', () => {
  const block = css.slice(css.indexOf('@media (max-width: 599px) {\n  .table { min-width: 720px; }'));
  assert.ok(block.length < css.length, 'the scrolled desktop table stays the phone default');
  for (const line of phone.split('\n').filter(line => line.includes('{') && line.startsWith('  '))) assert.match(line, /^\s+\[data-phone-(cards|actions)/, line);
  assert.match(read('../src/screens/settings/People.tsx'), /<SettingsTable phoneCards /);
  assert.match(read('../src/screens/settings/PeopleInvitations.tsx'), /<SettingsTable phoneCards=\{\{rowHeight: 156, actionsLine: true\}\} /);
  assert.doesNotMatch(read('../src/screens/dashboard/History.tsx'), /phoneCards/);
});

test('a second main cell or badge takes its own line; Invitations\' actions sit under the text', () => {
  assert.match(phone, /td\[data-kind='main'\]:not\(td\[data-kind='main'\] ~ td\) \{ grid-column: 1; grid-row: 1; \}/);
  assert.match(phone, /td:not\(\[data-kind='main'\]:not\(td\[data-kind='main'\] ~ td\), \[data-kind='badge'\]:not\(td\[data-kind='badge'\] ~ td\), \.actionCell\) \{ padding-left: 46px;/);
  assert.match(phone, /\[data-phone-actions='line'\] \.tableScroll \.table tr\[data-table-row\] > td\.actionCell \{ grid-column: 1 \/ -1;/);
});
