// Every link and redirect the chooser and the Portico Account pages make at web.getportico.tv
// goes to a page this app serves, so a page removed later can't strand a person on a missing one
// (the old `/welcome` start page was removed while the chooser still sent people there).
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync} from 'node:fs';
import {isServicesPath} from '../src/services/paths.ts';
import {componentModule} from './helpers/component-harness.mjs';

const src = new URL('../src/', import.meta.url);
const read = (path: string) => readFileSync(new URL(path, src), 'utf8');
const sources = ['screens/accounts/', 'services/', 'kit/accounts/'].flatMap(dir =>
  readdirSync(new URL(dir, src)).filter(name => /\.tsx?$/.test(name)).map(name => [dir + name, read(dir + name)] as const));

// The app's routes (router.tsx), with `$name` standing for one path segment.
const appRoutes = [...read('router.tsx').matchAll(/path: '([^']+)'/g)].map(match => new RegExp('^' + match[1].replace(/\$[A-Za-z]+/g, '[^/]+') + '$'));
const served = (path: string) => isServicesPath(path) || appRoutes.some(route => route.test(path));

test('every in-page link on the chooser and the account pages opens a page this app serves', async () => {
  const {chooserIntent} = await componentModule(new URL('../src/screens/accounts/ChooserStart.tsx', import.meta.url), {});
  // goTo, <Replace to>, and a page load on this address (location.assign/replace of a path).
  const targets = sources.flatMap(([file, text]) => [...text.matchAll(/(?:goTo\(|<Replace to=\{?|location\.(?:assign|replace)\()'(\/[^']*)'/g)].map(match => ({file, to: match[1]})));
  assert.ok(targets.length > 10, 'the links were found');
  for (const {file, to} of targets) {
    const url = new URL(to, 'https://web.getportico.tv');
    assert.ok(served(url.pathname), `${file}: ${to} is not a page this app serves`);
    if (url.hash) assert.ok(url.pathname === '/servers' && chooserIntent(url.hash), `${file}: ${to} names a part of the page nothing opens`);
  }
});

test('the three start pages each open from their address, and any other fragment shows the list', async () => {
  const {chooserIntent} = await componentModule(new URL('../src/screens/accounts/ChooserStart.tsx', import.meta.url), {});
  assert.deepEqual(['#join', '#setup', '#local', '#welcome', ''].map(chooserIntent), ['join', 'setup', 'local', undefined, undefined]);
});

test('a pasted invitation link opens Join here from its code and server, and is never followed', async () => {
  const {hooks} = await import('./helpers/component-harness.mjs');
  const host = hooks();
  let start: any;
  const {ChooserStart} = await componentModule(new URL('../src/screens/accounts/ChooserStart.tsx', import.meta.url), {
    react: {...host.react, useLayoutEffect: () => {}},
  });
  const followed: string[] = [], joined: unknown[] = [];
  Object.assign(globalThis, {location: {assign: (url: string) => {followed.push(url);}}});
  // The drawn start's own handlers (DocumentBootReady > ServicesStart).
  const draw = () => {start = host.render(() => ChooserStart({header: null, onJoin: (code: string, address?: string) => joined.push([code, address]), onLocal: () => {}})).props.children[0].props;};
  draw();
  start.onInvitationPaste('portico://join#code=K7QP-2MXD-9HRT-4WNC&server=https%3A%2F%2Fa1.ns.portico.direct%3A32400'); draw();
  start.onInvitationPaste('https://web.getportico.tv/join#code=K7QP2MXD9HRT4WNC&server=https%3A%2F%2Fa1.ns.portico.direct%3A32400'); draw();
  assert.deepEqual(joined, [['K7QP-2MXD-9HRT-4WNC', 'https://a1.ns.portico.direct:32400'], ['K7QP-2MXD-9HRT-4WNC', 'https://a1.ns.portico.direct:32400']]);
  assert.deepEqual(followed, [], 'no link is followed, and portico:// never reaches the browser');
});

test('a shared link opened at web.getportico.tv/join leaves the address bar, and Join opens with its code and server', async () => {
  const went: string[] = [], replaced: string[] = [], stored = new Map<string, string>();
  const {JoinArrival} = await componentModule(new URL('../src/services/JoinArrival.tsx', import.meta.url), {
    react: {useLayoutEffect: (run: () => void) => run(), default: {createElement: () => null}, createElement: () => null},
    '../platform/hosted-navigation': {goTo: (to: string) => {went.push(to);}},
    '../platform/invitation-carry': await import('../src/platform/invitation-carry.ts'),
  });
  Object.assign(globalThis, {location: {hash: '#code=K7QP-2MXD-9HRT-4WNC&server=https%3A%2F%2Fa1.ns.portico.direct%3A32400', pathname: '/join'},
    history: {state: null, replaceState: (_: unknown, __: string, url: string) => {replaced.push(url);}},
    sessionStorage: {getItem: (k: string) => stored.get(k) ?? null, setItem: (k: string, v: string) => {stored.set(k, v);}, removeItem: (k: string) => {stored.delete(k);}}});
  JoinArrival();
  assert.deepEqual(replaced, ['/join'], 'the fragment is out of the address and its history entry');
  assert.deepEqual(went, ['/servers']);
  const {takeCarriedInvitation} = await import('../src/platform/invitation-carry.ts');
  assert.deepEqual(takeCarriedInvitation(), {code: 'K7QP-2MXD-9HRT-4WNC', address: 'https://a1.ns.portico.direct:32400', kind: 'invitation'});
  (globalThis as any).location.hash = '#not-a-link'; went.length = 0; JoinArrival();
  assert.deepEqual(went, ['/servers#join'], 'a link that is not one opens Join to type it');
});
