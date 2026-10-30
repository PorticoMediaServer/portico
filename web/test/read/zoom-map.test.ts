import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {registerHooks} from 'node:module';
import {transformTsx} from '../transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ReaderZoomMap} = await import('../../src/kit/reader/ReaderZoomMap.tsx');
const {ReaderChrome} = await import('../../src/kit/reader/ReaderChrome.tsx');

const viewport = {page: 61, pageWidth: 612, pageHeight: 792, x: .33, y: .035, width: .52, height: .75, scale: 2};
const page = createElement('img', {src: 'blob:page', alt: ''});

test('phone draws the zoom pill and the 62×94 page map with the visible rectangle', () => {
  const html = renderToStaticMarkup(createElement(ReaderZoomMap, {viewport, page, percent: '200%', presentation: 'phone'}));
  assert.match(html, /class="zoomLayer"[^>]*aria-hidden="true"/);
  assert.match(html, /class="zoomPill"[^>]*>200%<\/span>/);
  assert.match(html, /class="pageMap"><img src="blob:page" alt=""\/><i style="left:33%;top:3.5%;width:52%;height:75%"/);
});

test('tablet and desktop draw the lens with the level and hint; the rectangle is clamped to the page', () => {
  const html = renderToStaticMarkup(createElement(ReaderZoomMap, {viewport: {...viewport, x: -.2, width: 1.4}, page, percent: '150%', hint: 'Drag to move around.', presentation: 'tablet'}));
  assert.match(html, /class="zoomLens"[^>]*aria-hidden="true"/);
  assert.match(html, /<span style="left:0%;top:3.5%;width:100%;height:75%"/);
  assert.match(html, /<p><b>150%<\/b> Drag to move around.<\/p>/);
});

test('the reader draws a supplied zoom map only for fixed pages, outside the hideable chrome', () => {
  const props = {copy: {back: 'Back', previous: 'Previous', next: 'Next', progress: 'Position', showControls: 'Show controls'},
    direction: 'ltr' as const, tapTurnEnabled: true, pageTurnsEnabled: true, showPageArrows: false, onBack() {}, onTurn() {}, actions: [], children: null,
    fixedPageZoom: createElement('div', {'data-test-zoom': true})};
  const fixed = renderToStaticMarkup(createElement(ReaderChrome, {...props, fixedPage: true, initiallyVisible: false}));
  assert.match(fixed, /data-test-zoom/);
  assert.ok(fixed.indexOf('data-test-zoom') < fixed.indexOf('class="chrome"'), 'the map is not inside the inert controls');
  assert.doesNotMatch(renderToStaticMarkup(createElement(ReaderChrome, {...props, fixedPage: false})), /data-test-zoom/);
});
