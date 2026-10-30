import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {defaultI18n} = await import('../../packages/i18n/src/index.ts');
const {NotFoundState} = await import('../src/shell/NotFound.tsx');
const {LastResort, crashDetailsText} = await import('../src/shell/Errors.tsx');

const details = {when: '10/24/2026, 4:12 PM', app: 'Portico for web 1.4.0', browser: 'Chrome on a Mac', where: '/library/movies?view=grid', message: 'Cannot read properties of undefined'};

test('states.html#12: the not-found page names the page and offers only Home and Search', () => {
  const markup = renderToStaticMarkup(createElement(NotFoundState, {onHome: () => {}, onSearch: () => {}}));
  assert.match(markup, new RegExp(defaultI18n.t('next.states.notFound').replace(/'/g, '&#x27;')));
  assert.match(markup, /The link may be out of date/);
  assert.match(markup, />Go to Home</);
  assert.match(markup, />Search</);
  // The colour-only backdrop is the shared state backdrop, never a 404 glyph or artwork.
  assert.doesNotMatch(markup, /404/);
  assert.doesNotMatch(markup, /<img/);
});

test('states.html#24: the last-resort page reloads, copies named facts, and never prints the error', () => {
  const markup = renderToStaticMarkup(createElement(LastResort, {details}));
  assert.match(markup, /Something went wrong/);
  assert.match(markup, /Portico ran into a problem it couldn&#x27;t recover from/);
  assert.match(markup, />Reload</);
  assert.match(markup, />Copy details</);
  for (const label of ['What Copy details includes', 'When', 'App', 'Browser', 'Where']) assert.match(markup, new RegExp(label));
  assert.match(markup, /Portico for web 1\.4\.0/);
  assert.match(markup, /Chrome on a Mac/);
  assert.match(markup, /\/library\/movies\?view=grid/);
  // The frame keeps the failure itself off the screen; only the copied text carries it.
  assert.doesNotMatch(markup, /Cannot read properties of undefined/);
  assert.match(crashDetailsText(details), /What happened: Cannot read properties of undefined/);
  // No invented reference: the app has no crash-report point to return one yet.
  assert.doesNotMatch(markup, /Reference/);
});
