// Settings › Switch server: what tells two same-named saved servers apart, and which of Sign out and Forget each offers.
import test from 'node:test';
import assert from 'node:assert/strict';
import {savedServerRows} from '../src-next/platform/saved-server-rows.ts';
import {screenModule} from './helpers/screen-harness.mjs';

const saved = (serverId: string, origin: string, horizon: string, lastGood?: string) => ({signIn: 'local', connection: {
  pin: {serverId, fingerprint: 'fp-' + serverId}, logicalOrigin: origin, routes: {paired: [], ...(lastGood ? {lastGood} : {})},
  session: {sessionFamilyId: 'family-' + serverId, authorizationHorizon: horizon}}} as any);
const now = Date.parse('2026-11-13T12:00:00Z');
const names: Record<string, string> = {a: 'Portico', b: 'Portico', c: 'Cottage'};

test('two saved servers with the same name each show where this device reaches them; a lone name keeps its usual line', () => {
  const rows = savedServerRows([saved('a', 'http://127.0.0.1:32511', '2026-12-01T00:00:00Z'), saved('b', 'https://nas.example:32500', '2026-12-01T00:00:00Z', 'https://192.168.1.20:32500'), saved('c', 'https://cottage.example', '2026-12-01T00:00:00Z')],
    id => names[id]!, () => 'accounts.servers.local', now);
  assert.deepEqual(rows.map(row => [row.name, row.sharesName, row.address]), [['Portico', true, '127.0.0.1:32511'], ['Portico', true, '192.168.1.20:32500'], ['Cottage', false, 'cottage.example']]);
});

test('a sign-in past its end on this device offers only Forget; a live one offers Sign out too', async () => {
  const rows = savedServerRows([saved('a', 'http://127.0.0.1:32511', '2026-11-01T00:00:00Z'), saved('c', 'https://cottage.example', '2026-12-01T00:00:00Z')], id => names[id]!, () => 'accounts.servers.local', now);
  assert.deepEqual(rows.map(row => row.signedIn), [false, true]);
  const t = (key: string, values?: Record<string, string>) => values ? `${key}:${values.server}` : key;
  const {serverMenuItems, serverRowDetail} = await screenModule(new URL('../src-next/screens/accounts/PhoneServerChooser.tsx', import.meta.url), {
    react: {useState: () => [undefined, () => {}]}, 'react-native': {}, 'react-native-safe-area-context': {}, '../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../kit/controls': {}, '../../kit/Icon': {}, '../../kit/overlays': {}, '../../kit/shell/visual': {}, '../../kit/theme': {}, '../../kit/states': {}, '../../kit/artwork': {}, '../../kit/layout/TopBar': {},
  });
  const acted: string[] = [];
  const items = (row: any) => serverMenuItems(row, {signOut: (server: any) => acted.push('signOut:' + server.id), forget: (server: any) => acted.push('forget:' + server.id)});
  assert.deepEqual(items(rows[0]).map((item: any) => item.label), ['connection.identity.forget:Portico']);
  assert.deepEqual(items(rows[1]).map((item: any) => item.label), ['accounts.signOut.server:Cottage', 'connection.identity.forget:Cottage']);
  items(rows[0])[0].onSelect(); items(rows[1])[0].onSelect();
  assert.deepEqual(acted, ['forget:a', 'signOut:c']);
  assert.equal(serverRowDetail({...rows[1], sharesName: true}), 'cottage.example');
  assert.equal(serverRowDetail(rows[1]), 'accounts.servers.local');
});
