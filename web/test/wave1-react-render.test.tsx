import assert from 'node:assert/strict';
import test from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {Button} from '../src/kit/controls/Button.tsx';
import {EpisodeDetailsLayout, SearchResultHero, TitleProse} from '../src/kit/title/TitleParts.tsx';

test('Button loading renders a spinner even without an icon prop', () => {
  const html = renderToStaticMarkup(createElement(Button, {loading: true}, 'Save'));
  assert.match(html, /class="[^"]*spinner[^"]*"/);
  assert.match(html, /aria-busy="true"/);
  assert.match(html, /disabled=""/);
  assert.match(html, />Save</);
});

test('Button without loading renders icon glyph when icon is given and no spinner', () => {
  const html = renderToStaticMarkup(createElement(Button, {icon: 'i-play'}, 'Play'));
  assert.doesNotMatch(html, /class="[^"]*spinner[^"]*"/);
  assert.doesNotMatch(html, /aria-busy=/);
  assert.match(html, /href="#i-play"/);
});

test('TitleProse parses inline markup into semantic elements instead of raw tags', () => {
  const html = renderToStaticMarkup(createElement(TitleProse, {markup: 'A <b>bold</b> and <i>italic</i><br/>tale'}));
  assert.match(html, /<strong>bold<\/strong>/);
  assert.match(html, /<em>italic<\/em>/);
  assert.match(html, /<br\/>/);
  assert.doesNotMatch(html, /&lt;b&gt;|&lt;i&gt;|&lt;br/);
});

test('SearchResultHero renders real links for title and backdrop and parses synopsis markup', () => {
  const html = renderToStaticMarkup(createElement(SearchResultHero, {
    title: 'Arrival',
    eyebrow: 'Movie',
    artwork: createElement('div', {'data-art': 'poster'}),
    facts: createElement('div', null, '2016'),
    synopsis: 'A <i>linguist</i> meets <b>visitors</b>.',
    actions: createElement('button', {type: 'button'}, 'Play'),
    href: '/item/movie-arrival',
    onOpen: () => {},
  }));
  assert.match(html, /<h3><a[^>]*href="\/item\/movie-arrival"[^>]*>Arrival<\/a><\/h3>/);
  assert.match(html, /<a[^>]*href="\/item\/movie-arrival"[^>]*aria-hidden="true"[^>]*tabIndex="-1"/i);
  assert.match(html, /<em>linguist<\/em>/);
  assert.match(html, /<strong>visitors<\/strong>/);
  assert.doesNotMatch(html, /&lt;i&gt;|&lt;b&gt;/);
});

test('EpisodeDetailsLayout parses synopsis markup instead of displaying raw tags', () => {
  const html = renderToStaticMarkup(createElement(EpisodeDetailsLayout, {
    title: 'Pilot',
    context: 'Season 1',
    facts: '48 min',
    synopsis: 'The <b>beginning</b> of <i>everything</i>.',
    actions: createElement('button', {type: 'button'}, 'Play'),
    onClose: () => {},
  }));
  assert.match(html, /<strong>beginning<\/strong>/);
  assert.match(html, /<em>everything<\/em>/);
  assert.doesNotMatch(html, /&lt;b&gt;|&lt;i&gt;/);
});
