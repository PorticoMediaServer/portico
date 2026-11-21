import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const now = Date.UTC(2026, 10, 4, 12);
const idle = {generation: 1, scope: {}, query: null, phase: 'idle', data: null, groups: [], pagination: {cursor: null, canPrevious: false}, error: null};

async function search({hasLiveTV, typed = false, live, at = now}: {hasLiveTV: boolean; typed?: boolean; live?: any; at?: number}) {
  const host = hooks(), loads: string[] = [];
  const {useSearch} = await screenModule(new URL('../src-next/pages/shared/search/useSearch.ts', import.meta.url), {
    react: host.react,
    '../../../../../../packages/client-core/src/timer': {repeat: () => ({stop() {}, runNow() {}})},
    '../../../../../../packages/client-core/src/server-clock': {serverNow: () => at},
    '../../../../../../packages/client-core/src/presentation/search-model':
      await import('../../../packages/client-core/src/presentation/search-model.ts'),
  });
  const snapshot = {text: typed ? 'matrix' : '', group: 'all', search: idle, groups: [], recent: []};
  const result = host.render(() => useSearch({
    destination: {subscribe: () => () => {}, getSnapshot: () => snapshot},
    runtime: {text: {t: (key: string) => key}, reportError: () => {}, subscribe: () => () => {}, getSnapshot: () => ({hasLiveTV})},
    explore: {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready'})},
    activation: {subscribe: () => () => {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})},
    actions: {},
    onNow: {subscribe: () => () => {}, getSnapshot: () => live ?? {phase: 'ready', cards: [], at}, load: async () => {loads.push('load');}},
  }));
  return {result, loads};
}

test('On now shows on the empty Search page of a server with Live TV, and not while typing or without Live TV', async () => {
  assert.equal((await search({hasLiveTV: true})).result.showLive, true);
  assert.equal((await search({hasLiveTV: true, typed: true})).result.showLive, false);
  assert.equal((await search({hasLiveTV: false})).result.showLive, false);
});

test('once the first programme has ended and before the re-read answers, the cards are drawn at that programme\'s end', async () => {
  const live = {phase: 'ready', cards: [], at: now - 60_000, until: now - 10_000};
  const {result} = await search({hasLiveTV: true, live, at: now});
  assert.equal(result.liveNow, now - 10_001, 'a card keeps the programme it showed instead of "No information"');
  assert.equal((await search({hasLiveTV: true, live: {...live, until: now + 60_000}})).result.liveNow, now);
});
