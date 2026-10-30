import assert from 'node:assert/strict';
import {test} from 'node:test';
import {defaultI18n} from '@i18n';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import type {ReaderChromeProps} from '../../src/kit/reader/ReaderChrome.tsx';
import {registerHooks} from 'node:module';
import {transformTsx} from '../transform-tsx.mjs';
import type {ReaderChromePresentation} from '../../src/screens/read/ReadReader.tsx';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ReaderChrome} = await import('../../src/kit/reader/ReaderChrome.tsx');

const fixedPageFooter: ReaderChromePresentation['fixedPageFooter'] = createElement('span', {'data-test-fixed-footer': true}, 'Observed fit');
const props: ReaderChromeProps = {
  copy: {back: 'Back', previous: 'Previous', next: 'Next', progress: 'Position', showControls: 'Show controls'},
  direction: 'ltr', tapTurnEnabled: true, pageTurnsEnabled: true, showPageArrows: false,
  onBack() {}, onTurn() {}, actions: [], children: null, pages: 'Observed page', fixedPageFooter,
};
const render = (overrides: Partial<ReaderChromeProps>) => renderToStaticMarkup(createElement(ReaderChrome, {...props, ...overrides}));

test('fixed-page footer uses the supplied assembly slot inside the existing hidden chrome', () => {
  const html = render({fixedPage: true, initiallyVisible: false});
  assert.match(html, /data-test-fixed-footer/);
  assert.doesNotMatch(html, /Observed page/);
  assert.match(html, /aria-hidden="true"/);
  assert.match(html, /inert=""/);
});

test('prose ignores a fixed-page slot and fixed pages without one retain the generic footer', () => {
  for (const overrides of [{fixedPage: false}, {fixedPage: true, fixedPageFooter: undefined}, {fixedPage: true, fixedPageFooter: null}]) {
    const html = render(overrides);
    assert.match(html, /Observed page/);
    assert.doesNotMatch(html, /data-test-fixed-footer/);
  }
});

test('comic chrome supplies its clear shade and custom footer without prose running labels', () => {
  const html = render({fixedPage: true, comic: true, presentation: 'tablet', title: 'Comic title', chapter: 'Chapter', remainingInSection: 'Remaining'});
  assert.match(html, /data-comic="true"/);
  assert.match(html, /data-presentation="tablet"/);
  assert.match(html, /data-reader-chrome-layer="shade"/);
  assert.match(html, /data-test-fixed-footer/);
  assert.doesNotMatch(html, /class="runningTitle"|class="runningFooter"|Observed page/);
});

test('a first-run hint makes the reader surface/chrome inert and withholds portal-capable auxiliary panels', () => {
  const html = render({hint: createElement('div', {role: 'dialog', 'aria-modal': true, 'data-reader-hint': true}, 'Got it'),
    initiallyVisible: true, panel: createElement('button', null, 'Background panel')});
  assert.match(html, /data-reader-surface[^>]*inert=""[^>]*aria-hidden="true"/);
  assert.match(html, /class="chrome"[^>]*inert=""[^>]*aria-hidden="true"/);
  assert.doesNotMatch(html, /Background panel/);
  assert.match(html, /role="dialog" aria-modal="true"/);
});


const {FixedPageFooter} = await import('../../src/screens/read/fixed-page.tsx');
const {ReadBookHero} = await import('../../src/kit/reader/ReadBookHero.tsx');
test('compact PDF footer draws observed labels, range and Pages/Contents/Fit pills without a thumbnail strip', () => {
  const snapshot: any = {status: 'ready', location: {format: 'pdf', page: 2, progression: .25, chapter: 'Actual heading'}, metadata: {pageCount: 9}, toc: [{title: 'Actual heading'}], zoom: {fit: 'width', scale: 1}};
  const controller: any = {subscribe: () => () => {}, getSnapshot: () => snapshot};
  const html = renderToStaticMarkup(createElement(FixedPageFooter, {controller, authority: {isCurrent: () => true} as any, format: 'pdf', reportError() {}, pages: '3 / 9', chapters: [{id: 'actual', start: 0, end: .5, current: true}], onPages() {}, onContents() {}, onFit() {}}));
  for (const label of ['Actual heading', '3 / 9', defaultI18n.t('next.reader.pagesStrip'), defaultI18n.t('next.reader.contents'), defaultI18n.t('next.reader.fitWidth')]) assert.ok(html.includes(label));
  assert.match(html, /type="range"[^>]*value="0.25"/);
  assert.doesNotMatch(html, /<img/);
  assert.equal((html.match(/<button/g) ?? []).length, 3);
});
test('series stack uses at most three supplied covers and never creates missing volumes', () => {
  const sources = Array.from({length: 5}, (_, index) => ({key: `cover-${index}`, scheduler: {} as any}));
  const base = {title: 'Actual series', facts: [], actions: null};
  const html = renderToStaticMarkup(createElement(ReadBookHero, {...base, seriesCovers: sources}));
  assert.match(html, /data-stack="3"/);assert.equal((html.match(/stackedCover/g) ?? []).length, 3);
  const missing = renderToStaticMarkup(createElement(ReadBookHero, {...base, seriesCovers: []}));
  assert.doesNotMatch(missing, /data-stack=|stackedCover/);
});
