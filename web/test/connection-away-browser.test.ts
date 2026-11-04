import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {RouterProvider, createMemoryHistory, createRootRoute, createRouter} from '@tanstack/react-router';
import {ConnectionAway, fullScreenSurfaceUp} from '../src/shell/ConnectionAway.tsx';

const render = (node: ReturnType<typeof createElement>) => {
  const router = createRouter({routeTree: createRootRoute({component: () => node}), history: createMemoryHistory({initialEntries: ['/']})});
  return router.load().then(() => renderToStaticMarkup(createElement(RouterProvider, {router})));
};

test('a browser\'s line is quiet and takes nothing: the server named in bold, no Downloads (states.html 4b)', async () => {
  const html = await render(createElement(ConnectionAway, {serverName: 'Portico demo', downloads: false}));
  assert.match(html, /role="status"/);
  assert.match(html, /Can(&#x27;|')t reach <strong>Portico demo<\/strong>\. Trying again\./);
  assert.doesNotMatch(html, /<a /);
  assert.doesNotMatch(html, /data-interactive/);
});

test('the desktop host keeps its Downloads link', async () => {
  const html = await render(createElement(ConnectionAway, {serverName: 'Portico demo'}));
  assert.match(html, /<a [^>]*href="\/saved\?view=downloads"/);
  assert.match(html, /data-interactive="true"/);
});

// A structural DOM, as in expanded-playback-focus.test.ts: attributes, ancestry and rects only.
class Node {
  children: Node[] = []; parentElement: Node | null = null; isConnected = true;
  attributes = new Map<string, string>();
  constructor(attributes: Record<string, string> = {}) {for (const [key, value] of Object.entries(attributes)) this.attributes.set(key, value);}
  append(...children: Node[]) {for (const child of children) {child.parentElement = this; this.children.push(child);}}
  matches(selectors: string): boolean {
    return selectors.split(',').some(selector => {
      const excluded = [...selector.matchAll(/:not\(([^)]+)\)/g)].map(match => match[1]);
      if (excluded.some(part => this.matches(part))) return false;
      const bare = selector.replace(/:not\([^)]+\)/g, '');
      if (bare === ':disabled') return false;
      const attributes = [...bare.matchAll(/\[([^=\]]+)(?:="([^"]*)")?\]/g)];
      return attributes.length > 0 && attributes.every(([, key, value]) => value === undefined ? this.attributes.has(key) : this.attributes.get(key) === value);
    });
  }
  closest(selector: string): Node | null {for (let at: Node | null = this; at; at = at.parentElement) if (at.matches(selector)) return at; return null;}
  querySelectorAll(selector: string): Node[] {return this.children.flatMap(child => [...(child.matches(selector) ? [child] : []), ...child.querySelectorAll(selector)]);}
  querySelector(selector: string) {return this.querySelectorAll(selector)[0] ?? null;}
  getClientRects() {return this.isConnected && !this.closest('[hidden]') ? [{}] : [];}
}
const page = (...surfaces: Node[]) => {const body = new Node(); body.append(new Node({'data-playback-route': ''}), ...surfaces); return body as unknown as Document;};

test('the browser line hides under every full-screen surface the player\'s focus owner knows, and readers', () => {
  assert.equal(fullScreenSurfaceUp(page()), false);
  assert.equal(fullScreenSurfaceUp(page(new Node({'data-watch-screen': ''}))), true);
  assert.equal(fullScreenSurfaceUp(page(new Node({'data-watch-screen': '', hidden: ''}))), false);
  // The music Now Playing and audiobook listening screens sit outside [data-watch-screen].
  assert.equal(fullScreenSurfaceUp(page(new Node({'data-watch-music': ''}))), true);
  assert.equal(fullScreenSurfaceUp(page(new Node({'data-watch-music': '', inert: ''}))), false);
  assert.equal(fullScreenSurfaceUp(page(new Node({'data-watch-audiobook': ''}))), true);
  const away = new Node({'data-watch-audiobook': ''}); away.isConnected = false;
  assert.equal(fullScreenSurfaceUp(page(away)), false);
  assert.equal(fullScreenSurfaceUp(page(new Node({'data-reader-root': ''}))), true);
});

test('only the browser line hides; the desktop host\'s line with Downloads is unchanged', () => {
  const away = readFileSync(new URL('../src/shell/ConnectionAway.tsx', import.meta.url), 'utf8');
  assert.match(away, /const covered = useFullScreenSurface\(!downloads\);/);
  assert.match(away, /hidden=\{covered \|\| undefined\}/);
  const css = readFileSync(new URL('../src/shell/shell.module.css', import.meta.url), 'utf8');
  assert.doesNotMatch(css, /:has\([^\n]*\.away/);
  assert.match(css, /\.away\[hidden\] \{ display: none; \}/);
  const focus = readFileSync(new URL('../src/screens/watch/expanded-focus.ts', import.meta.url), 'utf8');
  assert.match(focus, /export function fullPlaybackSurfaceUp\(document: Pick<Document, 'querySelectorAll'>\): boolean \{\n  return Array\.from\(document\.querySelectorAll<HTMLElement>\(surfaces\)\)\.some\(available\);/);
});

test('the shell feeds browsers the hub\'s sustained reachability', () => {
  const shell = readFileSync(new URL('../src/shell/Shell.tsx', import.meta.url), 'utf8');
  assert.match(shell, /chrome\.os !== undefined \? serverAway === true : reach === 'unreachable'/);
  const assembly = readFileSync(new URL('../src/shell/assembly.tsx', import.meta.url), 'utf8');
  assert.match(assembly, /reachability: \{getSnapshot: context\.freshness\.hub\.getReachability, subscribe: context\.freshness\.hub\.subscribeReachability\}/);
  const css = readFileSync(new URL('../src/shell/shell.module.css', import.meta.url), 'utf8');
  assert.match(css, /\.away:not\(\[data-interactive\]\) \{ pointer-events: none; \}/);
});
