import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const rules = readFileSync(new URL('../src/kit/dvr/rules.module.css', import.meta.url), 'utf8');
const live = readFileSync(new URL('../src/screens/live/live.module.css', import.meta.url), 'utf8');

test('the Recordings head and body follow the shell gutter, as the Channels tab does', () => {
  for (const selector of ['.header', '.tabList', '.chips', '.body']) {
    const rule = rules.slice(rules.indexOf(selector + ' {')).split('}')[0];
    assert.match(rule, /var\(--shell-gutter, var\(--gutter-desktop\)\)/, selector);
  }
  for (const selector of ['.recordingsBar', '.recordingsBanner', '.recBody']) {
    const rule = live.slice(live.indexOf(selector + ' {')).split('}')[0];
    assert.match(rule, /var\(--shell-gutter, var\(--gutter-desktop\)\)/, selector);
  }
});

test('on a phone the Recordings head stays one line: title size, no wrap, the source choice keeps its width', () => {
  const phone = rules.slice(rules.indexOf('@media (max-width: 599px) {\n  .header {'));
  assert.match(phone, /\.header \{ padding-inline: var\(--shell-gutter, var\(--gutter-desktop\)\); gap: 8px; flex-wrap: nowrap; \}/);
  assert.match(phone, /\.header h1 \{ font-size: var\(--type-title-size\); white-space: nowrap; \}/);
  assert.match(phone, /\.header \.source \{ flex: none; \}/);
});

test('on a phone the Channels and Recordings heads, chips and body all sit on the shell gutter, not a hard-coded 18px', () => {
  const phone = live.slice(live.lastIndexOf('@media (max-width: 599px)', live.indexOf('.recordingsBar, .recordingsBanner, .recBody { padding-inline:')));
  assert.match(live, /\.guideHeader, \.guideChips, \.guideTime \{ padding-inline: var\(--shell-gutter, var\(--gutter-desktop\)\); \}/);
  assert.match(phone, /\.recordingsBar, \.recordingsBanner, \.recBody \{ padding-inline: var\(--shell-gutter, var\(--gutter-desktop\)\); \}/);
  assert.doesNotMatch(live, /padding-inline: 18px/);
});
