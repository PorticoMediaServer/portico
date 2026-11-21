import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const rules = readFileSync(new URL('../src/kit/dvr/rules.module.css', import.meta.url), 'utf8');
const live = readFileSync(new URL('../src/screens/live/live.module.css', import.meta.url), 'utf8');

// The head, tabs and views of Live TV, Recordings and Series rules are the Live TV frame's, on its gutter. What the pages
// draw below them keeps to the shell gutter too.
test('the Recordings and Series rules bodies follow the shell gutter, as the frame does', () => {
  const body = rules.slice(rules.indexOf('.body {')).split('}')[0];
  assert.match(body, /var\(--shell-gutter\)/);
  for (const selector of ['.recordingsBanner', '.recBody']) {
    const rule = live.slice(live.indexOf(selector + ' {')).split('}')[0];
    assert.match(rule, /var\(--shell-gutter\)/, selector);
  }
});

test('on a phone the Recordings body sits on the shell gutter, not a hard-coded 18px', () => {
  assert.match(live, /\.recordingsBanner, \.recBody \{ padding-inline: var\(--shell-gutter\); \}/);
  assert.doesNotMatch(live, /padding-inline: 18px/);
});
