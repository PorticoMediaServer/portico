import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {RouterProvider, createMemoryHistory, createRootRoute, createRouter} from '@tanstack/react-router';
import {ConnectionAway} from '../src/shell/ConnectionAway.tsx';

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

test('the shell feeds browsers the hub\'s sustained reachability, and full-screen surfaces hide the line', () => {
  const shell = readFileSync(new URL('../src/shell/Shell.tsx', import.meta.url), 'utf8');
  assert.match(shell, /chrome\.os !== undefined \? serverAway === true : reach === 'unreachable'/);
  const assembly = readFileSync(new URL('../src/shell/assembly.tsx', import.meta.url), 'utf8');
  assert.match(assembly, /reachability: \{getSnapshot: context\.freshness\.hub\.getReachability, subscribe: context\.freshness\.hub\.subscribeReachability\}/);
  const css = readFileSync(new URL('../src/shell/shell.module.css', import.meta.url), 'utf8');
  assert.match(css, /body:has\(\[data-watch-screen\]:not\(\[hidden\]\), \[data-reader-root\]\)\) \.away \{ display: none; \}/);
  assert.match(css, /\.away:not\(\[data-interactive\]\) \{ pointer-events: none; \}/);
});
