import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('Search all books searches the words as typed, opens a hit, and asks for more only at the end', async () => {
  const host = hooks(), calls: unknown[][] = [];
  let snapshot: any = {phase: 'ready', query: 'whale', witness: 'w1', hits: [{itemId: 'b', sourceId: 's', sectionIndex: 0, utf16Offset: 4, excerpt: 'the whale', matchStart: 4, matchLength: 5}]};
  const port = {subscribe: () => () => {}, getSnapshot: () => snapshot, search: (...args: unknown[]) => calls.push(['search', ...args]),
    open: (...args: unknown[]) => calls.push(['open', ...args]), more: (...args: unknown[]) => calls.push(['more', ...args]), retry() {}};
  const hook = await screenModule(new URL('../src-next/pages/shared/read/useReadingSearch.ts', import.meta.url), {react: host.react});
  const {ReadingSearchPage} = await screenModule(new URL('../src-next/pages/touch/read/ReadingSearchPage.tsx', import.meta.url), {react: host.react,
    '../../../kit/reading/ReadingSearchParts': {ReadingSearchFrame: 'Frame', ReadingSearchHead: 'Head', ReadingSearchResults: 'Results'},
    '../../../kit/states': {StateView: 'StateView'}, '../../shared/read/useReadingSearch': hook});
  const runtime = {text: {t: (key: string) => key}, artwork: () => undefined, error: () => ({title: 'error'})};
  let tree: any;
  const render = () => host.render(() => {tree = ReadingSearchPage({port, opening: 'opening', runtime, query: 'moby', onBack() {}});});
  render();
  const head = tree.props.children[0], results = tree.props.children[1];
  assert.equal(head.type, 'Head');
  assert.equal(head.props.value, 'moby', 'the words start as the route gave them');
  results.props.onOpen(snapshot.hits[0]);
  results.props.onEnd();
  head.props.onSubmit();
  assert.deepEqual(calls.map(call => call[0]), ['open', 'more', 'search']);
  assert.deepEqual(calls[2], ['search', 'opening', 'moby']);
  // While the first page loads there are no hits yet (the skeleton), and an error replaces the answers.
  snapshot = {phase: 'loading'}; render();
  assert.equal(tree.props.children[1].props.hits, undefined);
  snapshot = {phase: 'error', error: new Error('x'), query: 'moby'}; render();
  assert.equal(tree.props.children[1].type, 'StateView');
});
