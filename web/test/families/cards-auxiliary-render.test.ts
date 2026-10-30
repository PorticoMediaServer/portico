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
const {PosterCard, CoverCard, WideCard, EpisodeTile, NewPlaylistCard} = await import('../../src/kit/cards/MediaCards.tsx');
const {QuickTile} = await import('../../src/kit/cards/FeatureCards.tsx');
const common = {model: {title: 'Supplied title', caption: []}, onNavigate() {}, onPlay() {}};

function actionSize(html: string, glyph: string, size: number) {
  assert.match(html, new RegExp(`class="play"[^>]*><svg[^>]*width="${size}"[^>]*height="${size}"[^>]*>.*?#${glyph}`));
}

test('card action glyphs follow their authored role without shrinking wide or episode playback', () => {
  actionSize(renderToStaticMarkup(createElement(PosterCard, common)), 'i-play', 20);
  actionSize(renderToStaticMarkup(createElement(CoverCard, common)), 'i-book', 20);
  actionSize(renderToStaticMarkup(createElement(QuickTile, common)), 'i-play', 16);
  actionSize(renderToStaticMarkup(createElement(QuickTile, {...common, playing: true, onPause() {}})), 'i-pause', 16);
  actionSize(renderToStaticMarkup(createElement(WideCard, common)), 'i-play', 22);
  actionSize(renderToStaticMarkup(createElement(EpisodeTile, {...common, artworkKind: 'still'})), 'i-play', 22);
});

test('artwork actions are real buttons outside the primary target with their original semantics', () => {
  const html = renderToStaticMarkup(createElement(PosterCard, {...common, onMore() {}, moreExpanded: true,
    onNotInterested() {}, selection: {active: false, checked: false, onToggle() {}, onExit() {}},
  }));
  const buttons = [...html.matchAll(/<button\b[^>]*>[\s\S]*?<\/button>/g)].map(match => match[0]);
  assert.equal(buttons.length, 5);
  assert.ok(buttons.every(button => (button.match(/<button\b/g) ?? []).length === 1));
  assert.match(buttons[0]!, /aria-labelledby="([^"]+)"[^>]*>[\s\S]*?<span id="\1" class="accessible">Supplied title<\/span>/);
  assert.match(buttons[2]!, /aria-haspopup="menu" aria-expanded="true"/);
  assert.match(buttons[4]!, /aria-pressed="false"/);
  assert.match(buttons[4]!, /aria-describedby="[^"]+"/);
  const selecting = renderToStaticMarkup(createElement(PosterCard, {...common,
    selection: {active: true, checked: true, onToggle() {}, onExit() {}},
  }));
  assert.doesNotMatch(selecting, /class="play"/);
  assert.match(selecting, /class="primary"[^>]*aria-pressed="true"/);
});

test('new playlist retains the canonical plus in its existing disc at the authored 30px size', () => {
  const html = renderToStaticMarkup(createElement(NewPlaylistCard, {model: common.model, onCreate() {}}));
  assert.match(html, /class="createGlyph"><svg[^>]*width="30"[^>]*height="30"[^>]*>.*?#i-plus/);
  assert.equal((html.match(/<button\b/g) ?? []).length, 1);
});
