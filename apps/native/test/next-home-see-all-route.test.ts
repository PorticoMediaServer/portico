import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {validateHomeSeeAll} from '../../../packages/client-core/src/home.ts';
import {homeSeeAllRoute} from '../src-next/shell/routes.ts';

// N4-1: every server See all destination opens a real page, never an empty row route.
test('See all opens the page the server names, and only a row opens the row page', () => {
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'row', rowId: 'recently_added'}), 'Recently added'),
    {kind: 'home-see-all', destination: {type: 'row', rowId: 'recently_added'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'guide'}), 'On now'), {kind: 'shell', destination: {kind: 'live'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'guide', group: 'news', sourceId: 'src'}), 'On now'), {kind: 'shell', destination: {kind: 'live'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'saved_view', resourceId: 'view_1'}), 'Sci-fi'),
    {kind: 'saved-view', view: {view: 'resource', resourceId: 'view_1'}});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'collection', resourceId: 'col_1', libraryId: 'lib_1'}), 'Pixar'),
    {kind: 'collection', libraryId: 'lib_1', collectionId: 'col_1', title: 'Pixar'});
  assert.deepEqual(homeSeeAllRoute(validateHomeSeeAll({type: 'library', libraryId: 'lib_1', pivot: 'titles', sort: [{field: 'added', direction: 'desc'}]}), 'New'),
    {kind: 'shell', destination: {kind: 'library', libraryId: 'lib_1', definition: {libraryId: 'lib_1', pivot: 'titles', query: null, sort: [{field: 'added', direction: 'desc'}], presentation: 'grid'}}});
});

test('Home headings, On now and the TV Hold OK menu all open See all through the one mapping', () => {
  const assembly = readFileSync(new URL('../src-next/shell/assembly.tsx', import.meta.url), 'utf8');
  assert.match(assembly, /seeAll: \(destination, title\) => open\(homeSeeAllRoute\(destination, title\)\)/);
  assert.doesNotMatch(assembly, /kind: 'home-see-all', destination\}/);
  const home = readFileSync(new URL('../src-next/screens/home/HomeScreen.tsx', import.meta.url), 'utf8');
  assert.equal(home.match(/runtime\.actions\.seeAll\(row\.seeAll!, title\)/g)?.length, 2, 'ordinary rows and the live On now row');
  const menu = readFileSync(new URL('../src-next/shell/HomeSelectionOverlays.tsx', import.meta.url), 'utf8');
  assert.match(menu, /runtime\.actions\.seeAll\(row\.seeAll!, rowTitle\)/);
});
