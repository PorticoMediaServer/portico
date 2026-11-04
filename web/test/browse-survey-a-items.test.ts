import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('#719 nits: the old Show all key and the phone Save view CSS are gone', () => {
  assert.doesNotMatch(read('../../packages/i18n/src/catalog/en-US/next/browse.ts'), /'next\.browse\.showAll':/);
  assert.doesNotMatch(read('../src/kit/browse/browse.module.css'), /\.phoneCount \.saveView/);
});

test('A19: on a window shorter than 960px the sort rows tighten so the whole menu, Order included, fits (desktop-browse #sort)', () => {
  assert.match(read('../src/kit/browse/browse.module.css'), /@media \(max-height: 960px\) \{ \.sortMenu button \{ height: 32px; \} \}/);
});

test('A20: a filter with several values reads as it means them in the empty sentence', () => {
  const empty = read('../src/screens/browse/FilteredEmpty.tsx');
  assert.match(empty, /defaultI18n\.list\(items, node\.operator === 'contains-all' \? 'and' : 'or'\)/);
  assert.equal(defaultI18n.list(['documentary', 'animation'], 'or'), 'documentary or animation');
});

test('A7: a search section counts the cards on its line against the total ("7 of 42")', () => {
  assert.match(read('../src/screens/search/AllResultsRow.tsx'), /result\.totalCount > Math\.min\(cap, entries\.length\) \? t\('web\.search\.someOf', \{shown: Math\.min\(cap, entries\.length\), total: result\.totalCount\}\)/);
});

test('A11: Music\'s Discover header offers Shuffle all beside the ⋯, a shuffled queue of the library\'s songs (desktop-listen #music)', () => {
  const route = read('../src/screens/browse/Library.route.tsx');
  assert.match(route, /library\.kind === 'music' && playRuntime \? <Button variant="glass" size="small" icon="i-shuffle"/);
  assert.match(route, /playRuntime\.playFromQueue\(\{query: \{libraryId: library\.id, pivot: 'songs'\}\}, \{signal: resources\.life\.signal\(\), isCurrent: [^}]*\}, \{shuffle: true\}\)/);
  assert.match(route, /header=\{<LibraryIdentity \{\.\.\.header\} action=\{shuffleAll\}\/>\}/);
  assert.equal(defaultI18n.t('next.browse.shuffleAll'), 'Shuffle all');
});
