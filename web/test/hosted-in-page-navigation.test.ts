import test from 'node:test';
import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import {join} from 'node:path';
import {goTo, inPageClick, registerInPageNavigation} from '../src/platform/hosted-navigation.ts';

// H14 (Justin, 29 Oct): "Switching pages shouldn't tear the whole page down." Every move between
// the Portico Account pages, the server chooser, Join, Set up and a joined server stays in one
// document at web.getportico.tv.
test('in-page moves use the registered router and never reload the document', () => {
  const saved = globalThis.location, loads: string[] = [], moves: [string, boolean][] = [];
  globalThis.location = {assign: (to: string) => {loads.push(to);}, replace: (to: string) => {loads.push(to);}} as unknown as Location;
  // A module-level sentinel survives only while the document does.
  const sentinel = Symbol('document');
  (globalThis as {__h14?: symbol}).__h14 = sentinel;
  const off = registerInPageNavigation((to, replace) => {moves.push([to, replace]);});
  try {
    for (const to of ['/account?tab=security', '/account?tab=servers', '/welcome#join', '/welcome#setup', '/servers', '/']) goTo(to);
    goTo('/servers', {replace: true});
    assert.deepEqual(loads, [], 'no page load');
    assert.equal(moves.length, 7); assert.deepEqual(moves.at(-1), ['/servers', true]);
    assert.equal((globalThis as {__h14?: symbol}).__h14, sentinel, 'the same document throughout');
    assert.throws(() => goTo('https://elsewhere.example/'), /Only a path/);
    assert.throws(() => goTo('//elsewhere.example/'), /Only a path/);
    let prevented = false;
    const click = (extra = {}) => ({defaultPrevented: false, button: 0, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, preventDefault: () => {prevented = true;}, ...extra});
    inPageClick(click(), '/account?tab=notifications'); assert.equal(prevented, true);
    prevented = false; inPageClick(click({metaKey: true}), '/'); assert.equal(prevented, false, 'a new-tab click stays the browser’s');
  } finally {off(); globalThis.location = saved;}
  // Without an app (a captured link opened alone) the same call is an ordinary page load.
  globalThis.location = {assign: (to: string) => {loads.push(to);}, replace: (to: string) => {loads.push('replace:' + to);}} as unknown as Location;
  try {goTo('/servers'); goTo('/account', {replace: true}); assert.deepEqual(loads, ['/servers', 'replace:/account']);}
  finally {globalThis.location = saved;}
});

test('the account pages, chooser and their kit never load a same-origin page directly', () => {
  const roots = ['src/services', 'src/screens/accounts', 'src/kit/accounts'];
  const files = roots.flatMap(root => readdirSync(new URL('../' + root, import.meta.url), {recursive: true}).map(name => join(root, String(name)))).filter(name => /\.tsx?$/.test(name));
  // Only Services pages and the chooser are in scope; the server's own sign-in screens keep
  // their documented cross-origin and same-tab return navigations.
  const scope = files.filter(name => name.startsWith('src/services/') || name.startsWith('src/kit/accounts/') || name.endsWith('HostedServers.tsx') || name.endsWith('HostedServicesHost.tsx'));
  const offenders = scope.flatMap(name => [...readFileSync(new URL('../' + name, import.meta.url), 'utf8').matchAll(/location\.(?:assign|replace)\(\s*['"`]\//g)].map(() => name));
  assert.deepEqual(offenders, [], 'use goTo() from platform/hosted-navigation');
});
