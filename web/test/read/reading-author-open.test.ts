import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from '../helpers/component-harness.mjs';
import {createRenderer} from '../helpers/interaction-harness.mjs';
import {redirectOnce} from '../../src/shell/navigation.ts';
import {authorPredicate} from '../../src/screens/read/facets.ts';

// An author's card in a Reading library opens the books grid filtered to them. The library page
// redraws while the router is on its way, each time with a new navigate; the redirect is asked once
// per author (#3259's rule), or it would restart its own navigation and never arrive.
test("opening an author's card navigates once, however often the library page redraws meanwhile", async () => {
  const host = createRenderer(), went: unknown[] = [];
  const {ReadingAuthorOpen} = await componentModule(new URL('../../src/screens/read/ReadingAuthorOpen.tsx', import.meta.url), {
    react: host.react, '../../kit/states': {Skeleton: 'Skeleton'}, '../../shell/navigation': {redirectOnce}, './facets': {authorPredicate},
  });
  try {
    let authorId = 'per_frank';
    const draw = () => host.render(() => ReadingAuthorOpen({authorId, pivot: 'books', navigate: (value: unknown, options: unknown) => went.push([value, options])}));
    for (let redraw = 0; redraw < 5; redraw++) draw();
    assert.equal(went.length, 1, 'five redraws, one navigation');
    assert.deepEqual(went[0], [{view: 'browse', pivot: 'books', query: authorPredicate('per_frank')}, {replace: true}]);
    authorId = 'per_martha'; draw(); draw();
    assert.equal(went.length, 2, 'another author is a new navigation, also once');
  } finally {host.dispose();}
});
