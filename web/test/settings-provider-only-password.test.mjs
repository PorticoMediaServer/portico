import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

// Justin, 1 Nov (DECISIONS.md): a Portico Account that signs in only with Google or Apple never sees
// "Set a password". Its Password row says how it signs in, with no button; a quiet More sign-in
// options link reveals Add a password (today's set-password flow). A password account is unchanged.
const google = {id: 'link-google', provider: 'google', revision: '1'}, apple = {id: 'link-apple', provider: 'apple', revision: '1'};
const security = values => ({contactAddress: 'owner@example.test', privateEmail: false, hasPassword: false, mfaEnabled: false, recoveryCodesRemaining: 0, providers: [google], ...values});

async function settings(state) {
  const h = createRenderer(), savedLocation = globalThis.location;
  globalThis.location = {origin: 'https://web.getportico.tv', hostname: 'web.getportico.tv'};
  const account = {id: 'account-1', username: 'owner-name', primaryProfileId: 'profile-1', role: 'owner', revision: 3, disabled: false, allowedLibraries: [], hostedAccountId: 'portico-account-1'};
  const document = {serverId: 'srv_1', canManage: true, capabilities: {manageAccount: true}, account};
  const profiles = {subscribe: () => () => {}, getSnapshot: () => ({document, inUseId: 'profile-1'})};
  const session = () => ({sessionFamilyId: 'family-1', viewer: {serverId: 'srv_1', accountId: 'account-1', profileId: 'profile-1', authority: 'local', role: 'owner'}});
  const label = (key, values) => values ? `${key}:${JSON.stringify(values)}` : key;
  const hosted = {service: {getSnapshot: () => ({session: {account: {id: 'portico-account-1'}, familyId: 'hosted-family', accessToken: 'memory-only'}})}, client: {state: async () => state, options: async () => ({credentialProviderReauthentication: 'account-settings-v1', providers: [{id: 'google', enabled: true}, {id: 'apple', enabled: true}]})}};
  const mod = await componentModule(new URL('../src/screens/settings/account-profile/ProfileSignIn.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n: {t: label}},
    '../../../services/session': {servicesAccount: () => hosted, currentAccountWork: async (_hosted, work) => work({account: {id: 'portico-account-1'}, familyId: 'hosted-family', accessToken: 'memory-only'}),
      accountIdentity: value => ({accountId: value.account.id, familyId: value.familyId}), sameAccount: () => true},
    '../../../platform/account-security-context': {useOriginalAccountSecurity: () => undefined},
    '../../../platform/account-origin': {browserAccountOrigin: () => 'https://web.getportico.tv'},
    '../../../platform/account-security-policy': {accountSecurityBrowserAvailable: () => true, accountSecurityUIURL: () => false},
    '../../../kit/settings/SettingsGroups': {SettingsGroup: 'SettingsGroup'}, '../../../kit/shell/SettingsSurfaces': {SettingsRow: 'SettingsRow'},
    '../../../kit/controls': {Button: 'Button', Switch: 'Switch', TextField: 'TextField'},
    '../../../kit/accounts/ServicesWorkspace': {ServicesAuthenticator: 'ServicesAuthenticator', ServicesQuietLink: 'ServicesQuietLink', ServicesRecoveryCodes: 'ServicesRecoveryCodes'},
    './HostedSecurityAction': {HostedSecurityAction: 'HostedSecurityAction'},
  });
  const render = () => tree(h.render(() => mod.ProfileSignIn({profiles, api: {}, commandApi: {}, signIn: 'portico', isCurrent: () => true, onEnded() {}, session})));
  let nodes; for (let i = 0; i < 40; i++) {nodes = render(); await new Promise(resolve => setTimeout(resolve, 0));}
  nodes = render();
  const row = id => nodes.find(node => node.type === 'SettingsRow' && node.props.id === id);
  const actionIn = node => node && tree(node).find(child => child.type === 'HostedSecurityAction');
  return {row: id => nodes.find(node => node.type === 'SettingsRow' && node.props.id === id), actionIn, nodes: () => nodes, render: () => {nodes = render(); return nodes;}, close: () => {h.dispose(); globalThis.location = savedLocation;}};
}

test('a Google-only account has no Password row, never Set a password, and a quiet link under the group', async () => {
  const f = await settings(security());
  try {
    assert.equal(f.row('profile.password'), undefined, 'no Password row until it is asked for');
    assert.equal(JSON.stringify(f.nodes().map(node => node.props?.label)).includes('account.setPassword'), false);
    assert.ok(f.row('profile.twoStep'), 'two-step stays: Hosted asks for it after a provider sign-in too');
    const link = f.nodes().find(node => node.type === 'ServicesQuietLink');
    assert.deepEqual([link.props.children].flat(), ['web.hostedSecurity.moreSignInOptions']);
    const group = f.nodes().find(node => node.type === 'SettingsGroup');
    assert.equal(tree(group).includes(link), false, 'the link sits under the group, not in it');
  } finally {f.close();}
});

test('More sign-in options shows the Password row: Not set, Add a password', async () => {
  const f = await settings(security());
  try {
    f.nodes().find(node => node.type === 'ServicesQuietLink').props.onClick();
    for (let i = 0; i < 4; i++) {f.render(); await new Promise(resolve => setTimeout(resolve, 0));} f.render();
    const row = f.row('profile.password');
    assert.ok(row); assert.equal(row.props.label, 'next.settingsSpec.account.profile.groups.5.rows.1.label');
    assert.equal(row.props.help, 'web.hostedSecurity.notSet');
    assert.equal(f.actionIn(row).props.label, 'web.hostedSecurity.addPassword');
    assert.equal(f.nodes().some(node => node.type === 'ServicesQuietLink'), false, 'the link goes away');
  } finally {f.close();}
});

test('an account with a password is unchanged: Change password, no link', async () => {
  const f = await settings(security({hasPassword: true}));
  try {
    const row = f.row('profile.password');
    assert.ok(row); assert.equal(f.actionIn(row).props.label, 'next.settingsSpec.account.profile.groups.5.rows.1.control.label');
    assert.equal(row.props.help, undefined);
    assert.equal(f.nodes().some(node => node.type === 'ServicesQuietLink'), false);
  } finally {f.close();}
});
