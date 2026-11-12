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
  const targets = sources.flatMap(([file, text]) => [...text.matchAll(/(?:goTo\(|<Replace to=\{?)'(\/[^']*)'/g)].map(match => ({file, to: match[1]})));
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
