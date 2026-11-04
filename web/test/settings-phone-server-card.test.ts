import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {ServerHeader} from '../src/kit/shell/ServerIdentity.tsx';

const css = readFileSync(new URL('../src/kit/shell/identity.module.css', import.meta.url), 'utf8');

test('a server page\'s header carries 24a\'s phone card: "Server", the name, and whose area it is', () => {
  // An owner-only page (General, Backups…): "Owner only".
  const owner = renderToStaticMarkup(createElement(ServerHeader, {serverName: 'Portico demo', viewerRole: 'owner', audience: 'owner', health: 'normal'}));
  assert.match(owner, /class="[^"]*phoneCard[^"]*"><span[^>]*data-size="34"[^>]*>.*<\/span><div><small>Server<\/small><b>Portico demo<\/b><\/div><span[^>]*data-size="phone"[^>]*>Owner only<\/span><\/div>/);
  // A page shared with administrators (Dashboard, Libraries, People) says so for the owner and for an administrator.
  for (const props of [{viewerRole: 'owner', audience: 'administrators'}, {viewerRole: 'administrator', audience: 'administrators'}] as const) {
    const shared = renderToStaticMarkup(createElement(ServerHeader, {serverName: 'Portico demo', ...props}));
    assert.match(shared, /<b>Portico demo<\/b><\/div><span[^>]*data-size="phone"[^>]*>Owner and administrators<\/span>/);
    assert.doesNotMatch(shared, />Owner only</);
  }
});

test('the audience comes from the settings bindings, the one list the desktop pill also reads', () => {
  const bindings = readFileSync(new URL('../src/screens/settings/bindings.ts', import.meta.url), 'utf8');
  const settings = readFileSync(new URL('../src/screens/settings/Settings.tsx', import.meta.url), 'utf8');
  // Troubleshooting is shared when administrators have any part of it (troubleshooting/parts.ts, from routes.ts).
  assert.match(bindings, /audience: section => \['dashboard', 'libraries', 'people'\]\.includes\(section\.id\) \|\| section\.id === 'troubleshooting' && troubleshootingParts\('admin'\)\.size > 0 \? 'administrators' : 'owner',/);
  assert.match(settings, /const audience = bindings\.audience\(section\);/);
  assert.match(settings, /<ServerHeader serverName=\{bindings\.serverName\} viewerRole="owner" audience=\{audience\}/);
});

test('at phone width the card replaces the pill and the name and health line; wider, the card stays hidden', () => {
  assert.match(css, /^\.phoneCard \{ display: none; \}/m);
  const phone = css.slice(css.indexOf('@media (max-width: 599px) {'));
  assert.match(phone, /\.header > \.serverPill, \.header > \.nameLine \{ display: none; \}/);
  assert.match(phone, /\.phoneCard \{ display: flex;/);
});
