import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import {transformTsx} from './transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {CollectionLink} = await import('../src/screens/detail/Detail.tsx');

// The server's `DetailCollection.memberCount` is `count(*)` over every visible collection
// member (catalog/item_recommendations.go), with no movie or show predicate. A collection
// holding one film and one show therefore arrives as 2, and the link may only repeat that real
// total. Naming films or movies here would claim a movie-only invariant the producer does not
// have; `next.browse.filmCount` stays for the movie library's own collection cards.
const collection = (memberCount: number) => ({id: 'col_dune', name: 'Dune collection', memberCount, position: 0, posterUrls: [] as string[]});
const render = (memberCount: number) => {
  const html = renderToStaticMarkup(createElement(CollectionLink, {collection: collection(memberCount), onOpen() {}}));
  return {html, text: html.replace(/<[^>]+>/g, '')};
};

test('a mixed collection keeps its real total and is never labelled as films', () => {
  const {html, text} = render(2);
  assert.equal(text, 'Dune collection' + defaultI18n.t('lib.count', {count: 2, pivot: 'other'}));
  assert.match(html, /<span class="collectionCount">2 titles<\/span>/);
  assert.doesNotMatch(text, /film|movie|show/i);
});

test('the collection count stays a catalogue plural at one and many', () => {
  assert.equal(render(1).text, 'Dune collection1 title');
  assert.equal(render(12).text, 'Dune collection12 titles');
  assert.doesNotMatch(render(12).text, /film|movie|show/i);
});
