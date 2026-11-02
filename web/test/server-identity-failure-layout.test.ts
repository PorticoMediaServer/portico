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
const {ServerIdentityFailure} = await import('../src/kit/accounts/ServerIdentityFailure.tsx');
const noop = () => {}, forget = async () => {};

// server-identity.html 6b and 1a: a full-page state (states.html), not a fixed-height card.
test('a saved server that changed draws the full-page state with its three ways out', () => {
  const markup = renderToStaticMarkup(createElement(ServerIdentityFailure, {serverName: 'Portico demo', failure: 'local-changed', onRetry: noop, onChooseServer: noop, onForget: forget}));
  assert.match(markup, /class="layout page"/);
  assert.match(markup, /<h1[^>]*>Portico couldn’t confirm this is Portico demo<\/h1>/);
  assert.match(markup, /data-tone="error"/);
  for (const label of ['Try again', 'Choose another server', 'Forget Portico demo']) assert.match(markup, new RegExp(`>${label}<`), label);
  assert.doesNotMatch(markup, /identityFailurePanel/);
});

test('an unconfirmed Portico Account server offers no Forget', () => {
  const markup = renderToStaticMarkup(createElement(ServerIdentityFailure, {serverName: 'The cottage', failure: 'unconfirmed', onRetry: noop, onChooseServer: noop, onForget: forget}));
  assert.match(markup, /data-tone="warning"/);
  assert.doesNotMatch(markup, /Forget/);
});
