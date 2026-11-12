import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, textOf} from './helpers/interaction-harness.mjs';

// H09: an owner who arrived through the normal Hosted return holds a server session, not a Hosted
// session on this origin. A Portico Account server's Sign-in section must render from what the server
// knows and open the Portico Account's own Security page, without any Hosted read on this origin.
async function fixture({signIn = 'portico', hostedAccountId = 'portico-account-1', canManage = true, origin = 'https://server.example:32500'} = {}) {
  const h = createRenderer(), hostedCalls = [], opened = [];
  const savedOpen = globalThis.open, savedLocation = globalThis.location;
  globalThis.open = (...args) => {opened.push(args);};
  globalThis.location = {origin, hostname: new URL(origin).hostname};
  const account = {id: 'account-1', username: 'owner-name', primaryProfileId: 'profile-1', role: 'owner', revision: 3, disabled: false, allowedLibraries: [], hostedAccountId};
  const document = {serverId: 'srv_1', canManage, capabilities: {manageAccount: true}, account};
  const profiles = {subscribe: () => () => {}, getSnapshot: () => ({document, inUseId: 'profile-1'})};
  const session = () => ({sessionFamilyId: 'family-1', viewer: {serverId: 'srv_1', accountId: 'account-1', profileId: 'profile-1', authority: 'local', role: 'owner'}});
  const label = (key, values) => values ? `${key}:${JSON.stringify(values)}` : key;
  const mod = await componentModule(new URL('../src/screens/settings/account-profile/ProfileSignIn.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n: {t: label}},
    '../../../services/session': {servicesAccount: () => {hostedCalls.push('servicesAccount'); throw new Error('Sign in with your Portico Account.');},
      currentAccountWork: () => {hostedCalls.push('currentAccountWork'); throw new Error('Sign in with your Portico Account.');}, accountIdentity: () => ({}), sameAccount: () => false},
    '../../../platform/account-origin': {browserAccountOrigin: () => 'https://web.getportico.tv'},
    '../../../platform/account-security-policy': {accountSecurityBrowserAvailable: () => true,
      accountSecurityUIURL: (origin, href) => href === `${origin}/account?tab=security`},
    '../../../kit/settings/SettingsGroups': {SettingsGroup: 'SettingsGroup'}, '../../../kit/shell/SettingsSurfaces': {SettingsRow: 'SettingsRow'},
    '../../../kit/controls': {Button: 'Button', Switch: 'Switch', TextField: 'TextField'},
  });
  const render = () => tree(h.render(() => mod.ProfileSignIn({profiles, api: {}, commandApi: {}, signIn, isCurrent: () => true, onEnded() {}, session})));
  let nodes; for (let i = 0; i < 4; i++) {nodes = render(); await Promise.resolve();}
  return {nodes, render, hostedCalls, opened, close: () => {h.dispose(); globalThis.open = savedOpen; globalThis.location = savedLocation;}};
}

test('a Portico Account server shows the server-known account and opens the Portico Account Security page, with no Hosted session here', async () => {
  const f = await fixture();
  try {
    const row = f.nodes.find(node => node.type === 'SettingsRow');
    assert.equal(row?.props.id, 'profile.porticoAccount');
    assert.match(textOf(row.props.help), /owner-name/);
    assert.ok(!f.nodes.some(node => node.type === 'SettingsRow' && ['profile.password', 'profile.twoStep', 'profile.email'].includes(node.props.id)), 'no row needs a Hosted session');
    const manage = f.nodes.find(node => node.type === 'Button');
    assert.equal(manage.props.disabled, false);
    manage.props.onClick();
    assert.deepEqual(f.opened, [['https://web.getportico.tv/account?tab=security', '_blank', 'noopener,noreferrer']]);
    assert.deepEqual(f.hostedCalls, [], 'no Hosted session or account work is touched on the server origin');
  } finally {f.close();}
});

test('an unlinked or unmanageable account shows no Portico Account section', async () => {
  for (const options of [{hostedAccountId: null}, {canManage: false}]) {
    const f = await fixture(options);
    try {
      assert.equal(f.nodes.filter(node => node.type === 'SettingsRow').length, 0);
      assert.deepEqual(f.hostedCalls, []);
    } finally {f.close();}
  }
});

// With the new Portico Services the in-app inline security (the first service's) is gone: a Portico
// Account is managed on its own Security page from every address, web.getportico.tv included.
test('at the Portico Account address too, the account is managed on its own Security page and no old account session is read', async () => {
  const f = await fixture({origin: 'https://web.getportico.tv'});
  try {
    assert.ok(f.nodes.some(node => node.type === 'SettingsRow' && node.props.id === 'profile.porticoAccount'), 'the Portico Account row');
    assert.deepEqual(f.hostedCalls, [], 'nothing reads the old account session');
  } finally {f.close();}
});
