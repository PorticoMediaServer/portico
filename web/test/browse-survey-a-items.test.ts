import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';
import {emptyFilterValues} from '../src/screens/browse/FilteredEmpty.tsx';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('#719 nits: the old Show all key and the phone Save view CSS are gone', () => {
  assert.doesNotMatch(read('../../packages/i18n/src/catalog/en-US/next/browse.ts'), /'next\.browse\.showAll':/);
  assert.doesNotMatch(read('../src/kit/browse/browse.module.css'), /\.phoneCount \.saveView/);
});

test('A19: on a window shorter than 960px the sort rows tighten so the whole menu, Order included, fits (desktop-browse #sort)', () => {
  assert.match(read('../src/kit/browse/browse.module.css'), /@media \(max-height: 960px\) \{ \.sortMenu button \{ height: 32px; \} \}/);
});

test('A20: the empty sentence names a filter\'s values as each operator means them, decades with their own "the"', () => {
  const words = (field: string, operator: string, values: readonly unknown[]) => emptyFilterValues(field, operator, values);
  // Decades.
  assert.equal(words('decade', 'in', [1990, 2000]), 'from the 1990s or 2000s');
  assert.equal(words('decade', 'contains-any', [1990, 2000]), 'from the 1990s or 2000s');
  assert.equal(words('decade', 'contains-all', [1990, 2000]), 'from the 1990s and 2000s');
  assert.equal(words('decade', 'not-in', [1990, 2000]), 'anything but the 1990s or 2000s');
  assert.equal(words('decade', 'not-equals', [1990]), 'anything but the 1990s');
  assert.equal(words('decade', 'between', [1990, 2000]), 'from the 1990s to the 2000s');
  assert.equal(words('decade', 'equals', [2010]), 'from the 2010s');
  // Other values.
  assert.equal(words('genre', 'contains-any', ['documentary', 'animation']), 'documentary or animation');
  assert.equal(words('genre', 'contains-all', ['documentary', 'animation']), 'documentary and animation');
  assert.equal(words('genre', 'not-in', ['documentary', 'animation']), 'anything but documentary or animation');
  assert.equal(words('year', 'between', [1990, 2000]), 'from 1990 to 2000');
  assert.equal(words('genre', 'equals', ['horror']), 'horror');
  // In the sentence.
  assert.equal(defaultI18n.t('next.browse.emptyNothingIn', {library: 'Movies', filters: words('decade', 'not-in', [1990, 2000])}), 'Nothing in Movies is anything but the 1990s or 2000s.');
});

test('A7: a search section counts the cards on its line against the total ("7 of 42")', () => {
  assert.match(read('../src/screens/search/AllResultsRow.tsx'), /result\.totalCount > Math\.min\(cap, entries\.length\) \? t\('web\.search\.someOf', \{shown: Math\.min\(cap, entries\.length\), total: result\.totalCount\}\)/);
});

test('A11: Music\'s Discover header offers Shuffle all beside the ⋯, a shuffled queue of the library\'s songs (desktop-listen #music)', () => {
  const route = read('../src/screens/browse/Library.route.tsx');
  assert.match(route, /library\.kind === 'music' && playRuntime \? <Button variant="glass" size="small" icon="i-shuffle"/);
  assert.match(route, /playRuntime\.playFromQueue\(\{query: \{libraryId: library\.id, pivot: 'songs'\}\}, \{signal: resources\.life\.signal\(\), isCurrent: [^}]*\}, \{shuffle: true\}\)/);
  assert.match(route, /header=\{<LibraryIdentity \{\.\.\.header\} action=\{shuffleAll\}\/>\}/);
  // Wide headers only: the phone's compact header keeps just the ⋯ (phone-web 6, phone-app).
  const library = read('../src/screens/browse/Library.tsx');
  assert.match(library, /<StickyBrowseHeader name=\{library\.name\} switcher=\{switcher\} menu=\{libraryMenu\}/);
  assert.match(library, /<LibraryHeader name=\{library\.name\} counts=\{counts\} switcher=\{switcher\} menu=\{menu\}/);
  assert.equal(defaultI18n.t('next.browse.shuffleAll'), 'Shuffle all');
});
