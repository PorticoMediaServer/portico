import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';

const guide = readFileSync(new URL('../src/screens/live/GuideScreen.tsx', import.meta.url), 'utf8');
const rules = readFileSync(new URL('../src/screens/live/SeriesRules.tsx', import.meta.url), 'utf8');

test('Live TV\'s first tab is Guide and the wide view switch reads Grid, List (desktop-live, tablet-play)', () => {
  assert.equal(defaultI18n.t('channels.tabGuide'), 'Guide');
  assert.equal(defaultI18n.t('channels.viewGrid'), 'Grid');
  assert.match(guide, /items=\{\[\{value: 'guide', label: t\('channels\.tabGuide'\)\},/);
  assert.match(rules, /\{value: 'guide', label: t\('channels\.tabGuide'\)\},/);
  assert.doesNotMatch(guide + rules, /label: t\('channels\.title'\)/);
  assert.match(guide, /items=\{\[\{value: 'guide', label: t\('channels\.viewGrid'\), icon: 'i-timeline'\},\n\s+\{value: 'list', label: t\('channels\.viewList'\), icon: 'i-list'\}\]\}/);
});

test('the phone keeps phone-guide\'s List, Guide switch (it draws no tabs above the guide)', () => {
  assert.match(guide, /items=\{\[\{value: 'list', label: t\('channels\.viewList'\), icon: 'i-list'\}, \{value: 'guide', label: t\('channels\.viewGuide'\), icon: 'i-timeline'\}\]\}/);
});
