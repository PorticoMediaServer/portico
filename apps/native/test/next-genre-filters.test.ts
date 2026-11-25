import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('a genre page\'s Filters shows the live result count on Show results, disables textEntry on TV, and Apply closes only when the draft applies', async () => {
  const host = hooks(), t = (key: string, values?: any) => values ? `${key}:${JSON.stringify(values)}` : key;
  let isTV = false;
  const seen: unknown[] = [];
  const {useGenreFilters} = await screenModule(new URL('../src-next/pages/shared/search/useGenreFilters.ts', import.meta.url), {react: host.react,
    '../../../kit/theme': {useTheme: () => ({isTV})},
    '../../../../../../packages/client-core/src/presentation/kit-model': {browseToolbarModel: (_c: unknown, _k: unknown, _p: unknown, _s: unknown, env: unknown) => {seen.push(env); return {groups: []};}},
    '../../../../../../packages/client-core/src/search-genres': {searchGenreBrowsePivot: 'titles'},
    '../../../screens/library/filter-controls': {libraryFilterChange: () => [], libraryFilterField: () => ({})}});
  let applies = false, disposed = 0, closed = 0;
  const state = {open: true, predicates: [], facets: {}, count: {phase: 'ready', value: {total: 12, totalIsLowerBound: false}}};
  const draft = {subscribe: () => () => {}, getSnapshot: () => state, refresh() {}, dispose: () => {disposed++;}, setOpenFields() {}, apply: () => applies, clear() {}, search() {}, retryFacet() {}};
  const render = () => host.render(() => useGenreFilters({draft, capabilities: {library: {kind: 'movie'}, fields: []}, sort: {}, fixed: [], owner: false, onClose: () => {closed++;},
    runtime: {text: {t, locale: 'en-US'}, getSnapshot: () => ({authority: 'a'}), error: () => ({}), reportError() {}}}));
  const view: any = render();
  assert.equal((seen.at(-1) as {textEntry: boolean}).textEntry, true);
  assert.equal(view.resultLabel, 'next.browse.native.showResults:{"results":"lib.count:{\\"count\\":12,\\"pivot\\":\\"titles\\"}"}');
  view.apply(); assert.equal(closed, 0, 'a draft that does not apply keeps the sheet open');
  applies = true; view.apply(); assert.equal(closed, 1); assert.equal(disposed, 1);
  isTV = true;
  render();
  assert.equal((seen.at(-1) as {textEntry: boolean}).textEntry, false);
});
