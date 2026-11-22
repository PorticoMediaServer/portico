import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as i18n from '../../../packages/i18n/src';
import * as text from '../../../packages/client-core/src/presentation/kit-model/text';
import * as presentation from '../src-next/screens/read/periodical-presentation';

// phone-read 24: a magazine archive's head reads "Experimenter Publishing · Monthly · 1926 to 1929 · 38 issues" and its
// Read button names the issue it opens.
test('the magazine head lists publisher, frequency, years and issues, and Read names its issue', async () => {
  const h = hooks();
  const {useMagazineHead} = await screenModule(new URL('../src-next/pages/shared/read/useMagazineHead.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': i18n, '../../../../../../packages/client-core/src/presentation/kit-model/text': text,
    '../../../screens/read/periodical-presentation': presentation, './usePeriodicalActions': {usePeriodicalActions: () => ({busy: false, toggleListed() {}})},
  });
  const issue = {item: {id: 'i1', inventoryAvailable: true}, issueDate: '1929-12'} as any, opened: unknown[] = [];
  const state = {publisher: 'Experimenter Publishing', frequency: 'monthly', issueCount: 38, years: ['1929', '1928', '1927', '1926'].map(value => ({value, count: 1}))} as any;
  const head = h.render(() => useMagazineHead({state, issue, offer: () => undefined, actions: {actions: {show() {}}} as any, runtime: {reportError() {}} as any, onOpen: value => opened.push(value)}));
  assert.deepEqual(head.facts.map((fact: any) => fact.text), ['Experimenter Publishing', 'Monthly', '1926 to 1929', '38 issues']);
  assert.equal(head.primary.label, 'Read December 1929');
  head.primary.onPress(); assert.deepEqual(opened, [issue]);
  // Without them (the demo's dated files), only what is known.
  const bare = h.render(() => useMagazineHead({state: {years: [{value: '1846', count: 3}], issueCount: 3} as any, issue, offer: () => undefined,
    actions: {actions: {show() {}}} as any, runtime: {reportError() {}} as any, onOpen() {}}));
  assert.deepEqual(bare.facts.map((fact: any) => fact.text), ['1846', '3 issues']);
});
