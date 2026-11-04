import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import test from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';

// RD-9: the in-book Reading direction menu is offered on every archive comic (CBZ and CBR) and
// carries Vertical scroll, as desktop-read frame 9 draws it.
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('/kit/overlays/index.ts')) return module(`import {createElement} from 'react';
    export const Menu = ({open, title, children}) => open ? createElement('div', {role: 'menu', 'aria-label': title}, children) : null;
    export const MenuItem = ({selected, children}) => createElement('button', {role: 'menuitemradio', 'aria-checked': String(!!selected)}, children);
    export const MenuSeparator = () => null;`);
  return next(url, context);
}});
const {FixedPageMenus} = await import('../../src/screens/read/fixed-page.tsx');
test.after(() => loader.deregister());

const snapshot = {status: 'ready', zoom: {fit: 'page', scale: 1}, preferences: {comicDirection: 'vertical', twoPages: false}};
const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot};
const menu = (format: string) => renderToStaticMarkup(createElement(FixedPageMenus, {controller, authority: {isCurrent: () => true}, reportError: () => {}, format,
  open: 'direction', onOpenChange: () => {}, anchorRef: {current: null}, directionRef: {current: null}, moreRef: {current: null}} as never));

test('CBZ and CBR both offer the reading direction choices, Vertical scroll among them', () => {
  for (const format of ['cbz', 'cbr']) {
    const html = menu(format);
    assert.match(html, /aria-label="Reading direction"/, format);
    assert.deepEqual([...html.matchAll(/aria-checked="(true|false)">([^<]+)</g)].slice(-4).map(m => [m[2], m[1]]),
      [['From the file', 'false'], ['Left to right', 'false'], ['Right to left', 'false'], ['Vertical scroll', 'true']], format);
  }
  assert.doesNotMatch(menu('pdf'), /Reading direction/, 'a PDF has no comic direction');
});
