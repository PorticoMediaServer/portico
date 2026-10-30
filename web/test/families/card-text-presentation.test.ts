import {test} from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import type {CardProps, CardTextPresentation, CardTextRun} from '../../src/kit/cards';
import {transformTsx} from '../transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {CoverCard, PosterCard, SquareCard, CircleCard} = await import('../../src/kit/cards/index.ts');
const render = (props: Partial<CardProps> = {}) => renderToStaticMarkup(createElement(CoverCard, {
  model: {title: 'Dune', caption: [{id: 'author', role: 'identity', text: 'Frank Herbert'}]}, onNavigate() {}, ...props,
}));

// These assignments are checked by the focused API type-check, alongside actual rendering.
const invalidElementRun: CardTextRun = {
  // @ts-expect-error Runs cannot contain React elements.
  text: createElement('button', {}, 'Interactive'),
};
const invalidHtmlRun: CardTextRun = {
  text: 'Dune',
  // @ts-expect-error There is no arbitrary HTML/attribute escape hatch.
  dangerouslySetInnerHTML: {__html: '<button>Interactive</button>'},
};
const invalidStyleRun: CardTextRun = {
  text: 'Dune',
  // @ts-expect-error Only the two authored inline treatments are supported.
  emphasis: 'bold',
};
void [invalidElementRun, invalidHtmlRun, invalidStyleRun];

test('authored percentage and match decorate only exact canonical text and keep the accessible title plain', () => {
  const textPresentation: CardTextPresentation = {
    title: [{text: 'Dune', emphasis: 'match'}],
    extraCaption: [{progress: [{text: '46%', emphasis: 'secondary'}, {text: ' read'}]}],
  };
  const html = render({textPresentation, model: {title: 'Dune', caption: [{id: 'author', role: 'identity', text: 'Frank Herbert'}], progress: .46},
    extraCaption: [[{id: 'progress', role: 'reason', text: '46% read'}]],
    selection: {active: true, checked: false, onToggle() {}, onExit() {}},
  });
  assert.match(html, /aria-labelledby="([^"]+)"[^>]*>[\s\S]*?<span id="\1" class="accessible">Dune<\/span>/);
  assert.match(html, /aria-label="Select Dune"/);
  assert.match(html, /<span class="title"><mark class="textMatch">Dune<\/mark><\/span>/);
  assert.match(html, /data-fact="progress" data-role="reason"><b class="textSecondary">46%<\/b> read<\/span>/);
  assert.doesNotMatch(html, /aria-label="[^"]*<(?:mark|b)/);
  assert.doesNotMatch(html, /<button\b[^>]*>(?:(?!<\/button>)[\s\S])*<button\b/);
});

test('partial matched title retains ordinary remainder and whole ordinary book reason', () => {
  const html = render({model: {title: 'Dune Messiah', caption: []}, extraCaption: [[{id: 'series', role: 'reason', text: 'Dune, book 2'}]],
    textPresentation: {title: [{text: 'Dune', emphasis: 'match'}, {text: ' Messiah'}]},
  });
  assert.match(html, /<mark class="textMatch">Dune<\/mark> Messiah<\/span>/);
  assert.match(html, /data-fact="series" data-role="reason">Dune, book 2<\/span>/);
  for (const title of ['Dune', 'Unmatched title']) assert.match(render({model: {title, caption: []}}), new RegExp(`<span class="title">${title}<\\/span>`));
  assert.doesNotMatch(render({extraCaption: [[{id: 'series', role: 'reason', text: 'Dune, book 4'}]]}), /<mark|<b /);
});

test('localized fact text, ids, roles, row and fact order remain canonical with credit-only emphasis', () => {
  const html = render({model: {title: 'Dune', caption: [{id: 'year', role: 'context', text: '2021'}, {id: 'credit', role: 'context', text: 'as Lady Jessica'}]},
    extraCaption: [[{id: 'localized', role: 'reason', text: 'lu à 46 %'}], [{id: 'credit', role: 'reason', text: 'as Lady Jessica'}]],
    textPresentation: {caption: {credit: [{text: 'as '}, {text: 'Lady Jessica', emphasis: 'secondary'}], absent: [{text: 'invented'}]},
      extraCaption: [{localized: [{text: 'lu à '}, {text: '46 %', emphasis: 'secondary'}]}, undefined, {absent: [{text: 'invented'}]}]},
  });
  assert.match(html, /data-fact="credit" data-role="context">as <b class="textSecondary">Lady Jessica<\/b><\/span>/);
  assert.match(html, /data-fact="localized" data-role="reason">lu à <b class="textSecondary">46 %<\/b><\/span>/);
  assert.match(html, /data-fact="credit" data-role="reason">as Lady Jessica<\/span>/);
  assert.deepEqual([...html.matchAll(/data-fact="([^"]+)" data-role="([^"]+)"/g)].map(([, id, role]) => [id, role]), [['year', 'context'], ['credit', 'context'], ['localized', 'reason'], ['credit', 'reason']]);
  assert.doesNotMatch(html, /invented|data-role="identity"/);
});

test('missing, stale, reordered, unsupported and nontext runs fall back to canonical plain rendering', () => {
  const plain = render();
  const malformed = [undefined, [], [{text: 'Different title', emphasis: 'match'}], [{text: 'Dune ', emphasis: 'match'}],
    [{text: 'ne'}, {text: 'Du'}], [{text: 'Dune', emphasis: 'bold'}], [{text: createElement('button', {}, 'Dune')}], [null], 'Dune'];
  for (const title of malformed) assert.equal(render({textPresentation: {title: title as readonly CardTextRun[]}}), plain);
  const props: Partial<CardProps> = {extraCaption: [[{id: 'reason', role: 'reason', text: '46% read'}]]};
  const canonical = render(props);
  assert.equal(render({...props, textPresentation: {caption: {author: [{text: 'Wrong author'}]}, extraCaption: [{reason: [{text: '46%read', emphasis: 'secondary'}]}]}}), canonical);
});

test('inline content stays escaped and cannot introduce interactive markup or attributes', () => {
  const text = '<button onclick="go()">Dune & friends</button>';
  const html = render({model: {title: text, caption: []}, textPresentation: {title: [{text, emphasis: 'match'}]}});
  assert.match(html, /<mark class="textMatch">&lt;button onclick=&quot;go\(\)&quot;&gt;Dune &amp; friends&lt;\/button&gt;<\/mark>/);
  assert.equal((html.match(/<button\b/g) ?? []).length, 1);
  assert.doesNotMatch(html, /<mark[^>]+onclick/);
});

test('all CardCaption media consumers forward only the declared optional presentation', () => {
  const props = {model: {title: 'Dune', caption: []}, onNavigate() {}, textPresentation: {title: [{text: 'Dune', emphasis: 'match'}]}} satisfies CardProps;
  for (const element of [createElement(CoverCard, props), createElement(PosterCard, props), createElement(SquareCard, props), createElement(CircleCard, {...props, covers: []})]) {
    const html = renderToStaticMarkup(element);
    assert.match(html, /<mark class="textMatch">Dune<\/mark>/);
    assert.match(html, /aria-labelledby="([^"]+)"[^>]*>[\s\S]*?<span id="\1" class="accessible">Dune<\/span>/);
    assert.doesNotMatch(html, /textPresentation=/i);
  }
});
