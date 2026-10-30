import test from 'node:test';
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
const {PosterCard, SquareCard, CoverCard, EpisodeTile} = await import('../../src/kit/cards/MediaCards.tsx');
const {LiveCard, CollectionBanner} = await import('../../src/kit/cards/FeatureCards.tsx');

function primaryText(html: string, attribute: 'aria-labelledby' | 'aria-describedby') {
  const tag = html.match(/<button\b[^>]*class="primary"[^>]*>/)?.[0];
  assert.ok(tag);
  const id = tag.match(new RegExp(`${attribute}="([^"]+)"`))?.[1];
  assert.ok(id, `the primary action has ${attribute}`);
  const escaped = id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const text = html.match(new RegExp(`<span id="${escaped}" class="accessible">([^<]*)<\\/span>`))?.[1];
  assert.ok(text, 'the reference resolves to actual canonical copy');
  return text;
}
const common = {model: {title: 'Dune', caption: [{id: 'year', role: 'context' as const, text: '2021'}]}, onNavigate() {}, onPlay() {}, onMore() {}};

test('card navigation names its title and describes the supplied metadata and states', () => {
  const html = renderToStaticMarkup(createElement(PosterCard, {...common, watched: true, isNew: true}));
  assert.equal(primaryText(html, 'aria-labelledby'), 'Dune');
  assert.equal(primaryText(html, 'aria-describedby'), '2021, New, Watched');
  assert.match(html, /aria-label="Play: Dune"/);
  assert.match(html, /aria-label="More: Dune"/);
  const older = renderToStaticMarkup(createElement(PosterCard, {...common, model: {...common.model, caption: [{id: 'year', role: 'context', text: '1984'}]}}));
  assert.equal(primaryText(older, 'aria-labelledby'), 'Dune');
  assert.equal(primaryText(older, 'aria-describedby'), '1984');
  const collection = renderToStaticMarkup(createElement(CollectionBanner, {...common, covers: [], onAction() {}}));
  assert.match(collection, /aria-label="Play all: Dune"/);
});

test('book completion, episode metadata and guide identity remain audible', () => {
  for (const element of [createElement(SquareCard, {...common, variant: 'audiobook', watched: true}), createElement(CoverCard, {...common, watched: true, watchedLabel: 'Finished'})]) {
    const html = renderToStaticMarkup(element);
    assert.equal(primaryText(html, 'aria-describedby'), '2021, Finished');
  }
  const episode = renderToStaticMarkup(createElement(EpisodeTile, {...common, artworkKind: 'still', length: '50 min', status: [{id: 'state', role: 'status', text: 'Unwatched'}], airDate: {id: 'air-date', role: 'context', text: '26 October 2026'}}));
  assert.equal(primaryText(episode, 'aria-describedby'), '2021, 50 min, Unwatched, 26 October 2026');
  const live = renderToStaticMarkup(createElement(LiveCard, {channel: {sourceId: 'source', id: 'news', name: 'News', number: '24'}, programme: {title: 'Evening news', facts: [{id: 'time', role: 'context', text: '18:00–19:00'}], live: true}, onNavigate() {}}));
  assert.equal(primaryText(live, 'aria-labelledby'), 'Evening news');
  assert.equal(primaryText(live, 'aria-describedby'), 'News, 24, 18:00–19:00, Live');
});
