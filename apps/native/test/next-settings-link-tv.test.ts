import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// N2-6: Settings › Device › Link a TV opens the code entry ("Enter the code shown on your TV"), as Play On does;
// Back from it returns to the server list (ServersRoute's own close sets the panel back to 'servers').
test('Link a TV opens the TV code entry, not Switch server', () => {
  const ports = readFileSync(new URL('../src-next/screens/settings-live/settings-ports.ts', import.meta.url), 'utf8');
  assert.match(ports, /linkTV: \(\) => open\(\{kind: 'servers', initialPanel: 'consent-kind'\}\)/);
  const routes = readFileSync(new URL('../src-next/screens/accounts/AccountRoutes.tsx', import.meta.url), 'utf8');
  assert.match(routes, /const close = \(\) => \{[^}]*setPanel\('servers'\);\}/, 'Back from the code entry shows the server list');
});
