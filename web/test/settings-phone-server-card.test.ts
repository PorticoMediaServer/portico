import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {ServerHeader} from '../src/kit/shell/ServerIdentity.tsx';

const css = readFileSync(new URL('../src/kit/shell/identity.module.css', import.meta.url), 'utf8');

test('a server page\'s header carries 24a\'s phone card: "Server", the name, and whose area it is', () => {
  const owner = renderToStaticMarkup(createElement(ServerHeader, {serverName: 'Portico demo', viewerRole: 'owner', audience: 'owner', health: 'normal'}));
  assert.match(owner, /class="[^"]*phoneCard[^"]*"><span[^>]*data-size="34"[^>]*>.*<\/span><div><small>Server<\/small><b>Portico demo<\/b><\/div><span[^>]*data-size="phone"[^>]*>Owner only<\/span><\/div>/);
  const administrator = renderToStaticMarkup(createElement(ServerHeader, {serverName: 'Portico demo', viewerRole: 'administrator', audience: 'administrators'}));
  assert.match(administrator, /<b>Portico demo<\/b><\/div><span[^>]*data-size="phone"[^>]*>Administrator<\/span>/);
});

test('at phone width the card replaces the pill and the name and health line; wider, the card stays hidden', () => {
  assert.match(css, /^\.phoneCard \{ display: none; \}/m);
  const phone = css.slice(css.indexOf('@media (max-width: 599px) {'));
  assert.match(phone, /\.header > \.serverPill, \.header > \.nameLine \{ display: none; \}/);
  assert.match(phone, /\.phoneCard \{ display: flex;/);
});
