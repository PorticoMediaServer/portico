import {test} from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {TrackTable} = await import('../../src/kit/browse/TrackTable.tsx');
const {SelectionCircle} = await import('../../src/kit/selection/SelectionCircle.tsx');
const {SuppliedLiveFacts, factsFor} = await import('../fixtures/card-state-facts.tsx');

test('TrackTable renders the supplied real selection circle and its selected semantics', () => {
  const row = {id: 'supplied-song', text: {title: 'Supplied song', caption: [], detail: []}, number: 1};
  const html = renderToStaticMarkup(createElement(TrackTable, {rows: [row], variant: 'album', label: 'Supplied album', onPlay() {}, onMore() {},
    renderSelection: item => createElement(SelectionCircle, {selected: item.id === row.id, accessibilityLabel: item.text.title, variant: 'row'}),
  }));
  assert.match(html, /class="trackSelection"><button[^>]+aria-label="Supplied song"[^>]+aria-pressed="true"[^>]+data-selected="true"/);
  assert.doesNotMatch(html, /aria-checked/);
});

test('LiveCard preserves the complete canonical programme facts as separate identified spans', () => {
  for (const kind of ['home', 'rich'] as const) {
    const facts = factsFor(kind);
    const html = renderToStaticMarkup(createElement(SuppliedLiveFacts, {kind}));
    const rendered = [...html.matchAll(/<span data-fact="([^"]+)" data-role="([^"]+)">([^<]*)<\/span>/g)]
      .map(([, id, role, text]) => ({id, role, text}));
    assert.deepEqual(rendered, facts);
    assert.ok(facts.length >= 4);
    assert.ok(facts.some(part => part.id === 'started'));
    assert.ok(facts.some(part => part.id === 'remaining'));
  }
});
