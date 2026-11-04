import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('phone Recordings views are frame 12\'s underline tabs (Upcoming, Recorded, Series rules, History); wider screens keep the chips', () => {
  const rules = read('../src/screens/live/SeriesRules.tsx'), screen = read('../src/screens/live/RecordingsScreen.tsx');
  assert.match(rules, /export function RecordingPhoneTabs\(/);
  assert.match(rules, /const shown = views\.filter\(item => item\.id !== 'storage'\);/);
  assert.match(rules, /<Tabs phone className=\{styles\.phoneViews\}/);
  assert.match(rules, /<RecordingPhoneTabs views=\{views\} value="rules" onChange=\{onView\}\/>/);
  assert.match(screen, /<RecordingPhoneTabs views=\{views\} value=\{view\} onChange=\{goView\}\/>/);
  const css = read('../src/kit/dvr/rules.module.css'), live = read('../src/screens/live/live.module.css');
  const base = css.indexOf('.phoneViews { display: none; }'), phone = css.lastIndexOf('@media (max-width: 599px) {');
  assert.ok(base > 0 && base < phone, 'hidden by default, before the phone block that shows it');
  assert.match(css.slice(phone), /\.phoneViews \{ display: block;[^}]*\}\n\s+\.chips \{ display: none; \}/);
  assert.match(live, /@media \(max-width: 599px\) \{ \.recordingsChips \{ display: none; \} \}/);
});
