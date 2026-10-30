import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
import type {SessionCardProps} from '../../src/kit/cards/FeatureCards';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {SessionCard} = await import('../../src/kit/cards/FeatureCards.tsx');
const viewerLine = (viewer: SessionCardProps['viewer']) => {
  const html = renderToStaticMarkup(createElement(SessionCard, {viewer, onNavigate: () => assert.fail('Unexpected navigation during static render'), model: {title: 'Supplied title', caption: []}, facts: [{id: 'network', text: 'Home network', role: 'context'}]}));
  return html.match(/<span class="viewer">(.*?)<\/span><span class="facts sessionFacts"/s)?.[1] ?? assert.fail(html);
};

test('a genuine username leads, with the actual differing profile quieter and device last', () => {
  const viewer = Object.freeze({name: 'Kids', username: 'sam_k', device: 'Living room TV'});
  assert.equal(viewerLine(viewer), '<strong>sam_k</strong><span class="sessionProfile">Kids</span><span>Living room TV</span>');
  assert.equal(viewer.name, 'Kids');
});

test('trim and case-only profile differences do not repeat the supplied username', () => {
  for (const name of ['sam_k', ' SAM_K ', 'Sam_K']) {
    assert.equal(viewerLine({name, username: 'sam_k', device: 'TV'}), '<strong>sam_k</strong><span>TV</span>');
  }
});

test('missing, empty and whitespace usernames retain the real legacy profile without invention', () => {
  for (const username of [undefined, '', '  ']) {
    assert.equal(viewerLine({name: 'Sam', username, device: 'TV'}), '<strong>Sam</strong><span>TV</span>');
  }
});

test('differing profile spelling is preserved, and an empty profile adds no secondary label', () => {
  assert.equal(viewerLine({name: ' Kids ', username: 'sam_k', device: 'TV'}), '<strong>sam_k</strong><span class="sessionProfile"> Kids </span><span>TV</span>');
  assert.equal(viewerLine({name: '  ', username: 'sam_k', device: 'TV'}), '<strong>sam_k</strong><span>TV</span>');
});

test('avatar stays separate and supplied identity is escaped as text', () => {
  const line = viewerLine({name: '<Kids>', username: '<sam_k>', device: '<TV>', avatar: createElement('i', {'data-avatar': true})});
  assert.equal(line, '<i data-avatar="true"></i><strong>&lt;sam_k&gt;</strong><span class="sessionProfile">&lt;Kids&gt;</span><span>&lt;TV&gt;</span>');
});
