import test from 'node:test';
import assert from 'node:assert/strict';
import type {LocalSession} from '../../../packages/client-core/src';
import type {PreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {settingsStructure} from '../../../packages/client-core/src/presentation/settings-structure';
import {installationIdentity,setInstallationIdentity} from '../../../packages/client-core/src/installation';
import {nativeAccountPanelIds, nativeSettingsContext, nativeSettingsUnavailablePanelIds} from '../src-next/screens/settings-live/settings-context';

const preferences = {
  deviceClass: 'mobile',
  registry: {revision: 'r1', fields: [
    {key: 'playback.autoplayNext', type: 'boolean', default: true, scopes: ['profile-server'], group: 'playback', labelKey: 'settings.row.autoplayNext'},
    {key: 'region.locale', type: 'string', default: 'en-US', scopes: ['profile-server'], group: 'region', labelKey: 'settings.row.language'},
  ]},
  documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'r1',
} as unknown as PreferenceSnapshot;

const account = (role: 'owner' | 'admin' | 'member', hostedAccountId?: string): DirectSnapshot => Object.freeze({
  authority: 'local', serverId: 'server-1', canManage: true,
  capabilities: {manageAccount: true, managePeople: role !== 'member', manageServer: role !== 'member', manageSignIn: role === 'owner'},
  account: {id: 'account-1', username: 'justin', primaryProfileId: 'profile-1', role, revision: 1, disabled: false, allowedLibraries: [], ...(hostedAccountId ? {hostedAccountId} : {})},
  profiles: [{id: 'profile-1', name: 'Justin', primary: true, position: 0, art: 'blue' as const, revision: 1, pinRevision: 0, pinRequired: false}],
});

const source = (role: 'owner' | 'admin' | 'member', hostedAccountId?: string) => ({account: account(role, hostedAccountId), preferences: () => preferences, session: () => ({sessionFamilyId: 'family-1', accessToken: 'test-only', expiresAt: '2030-01-01T00:00:00Z', tokenGeneration: '1', authorizationHorizon: 'h', viewer: {serverId: 'server-1', accountId: 'account-1', profileId: 'profile-1', role, authority: 'local'}} as LocalSession)});

test('an owner on a local server is the owner, with the server-published preference keys', () => {
  const context = nativeSettingsContext(source('owner'), 'phone');
  assert.equal(context.owner, true);
  assert.equal(context.administrator, false);
  assert.equal(context.platform, 'phone');
  assert.deepEqual([...context.capabilities.preferenceKeys ?? []], ['playback.autoplayNext', 'region.locale']);
  assert.equal(context.capabilities.localAccount, true);
  assert.equal(context.capabilities.porticoMember, false);
  assert.equal(context.capabilities.manageSignIn, true);
  assert.equal(context.capabilities.manageDevices, true);
  assert.equal(context.capabilities.downloads, false);
});

test('a Portico Account member is not a local account and is never made owner', () => {
  const context = nativeSettingsContext(source('member', 'hosted-1'), 'phone');
  assert.equal(context.owner, false);
  assert.equal(context.administrator, false);
  assert.equal(context.capabilities.localAccount, false);
  assert.equal(context.capabilities.porticoMember, true);
  assert.equal(context.capabilities.hostedAccount, true);
  assert.equal(context.capabilities.accessTokens, false);
  assert.equal(context.capabilities.manageSignIn, true, 'Hosted credentials use the account-owned security flow in Settings');
  assert.equal(context.capabilities.manageDevices, true, 'a Hosted member manages its main-profile server devices here');
});

test('an administrator sees only the administrator tier, never the owner pages', () => {
  const context = nativeSettingsContext(source('admin'), 'tv');
  assert.equal(context.owner, false);
  assert.equal(context.administrator, true);
  assert.equal(context.platform, 'tv');
});

test('the structure omits every drawn panel this build cannot draw rather than leaving an empty section', () => {
  const original = installationIdentity();
  setInstallationIdentity(undefined);
  try {
    const context = nativeSettingsContext(source('owner'), 'phone');
    const structure = settingsStructure(context);
    const rows = structure.headings.flatMap(heading => heading.pages).flatMap(page => page.sections).flatMap(section => section.rows);
    const custom = rows.filter(row => row.kind === 'custom').map(row => row.kind === 'custom' ? row.id : '');
    for (const id of nativeSettingsUnavailablePanelIds) assert.equal(custom.includes(id), false, id);
    // Device name and downloads storage are drawn only from their real capability:
    // an installation name and the read-only downloads summary port. This context
    // has neither, so both rows stay out rather than opening an empty panel.
    const capabilityGated = new Set<string>(['deviceName', 'downloadsStorage']);
    for (const id of nativeAccountPanelIds.filter(id => !capabilityGated.has(id))) assert.equal(custom.includes(id), true, id);
    assert.equal(context.capabilities.deviceName, false);
    assert.equal(custom.includes('deviceName'), false, 'a missing installation name supplies no device-name row');
    assert.equal(context.capabilities.downloads, false);
    assert.equal(custom.includes('downloadsStorage'), false, 'without the downloads port the storage row is omitted');
    const withDownloads = settingsStructure(nativeSettingsContext(source('owner'), 'phone', true));
    const downloadsRows = withDownloads.headings.flatMap(heading => heading.pages).flatMap(page => page.sections).flatMap(section => section.rows);
    assert.ok(downloadsRows.some(row => row.kind === 'custom' && row.id === 'downloadsStorage'), 'the real downloads port admits its storage row');
    for (const section of structure.headings.flatMap(heading => heading.pages).flatMap(page => page.sections)) assert.ok(section.rows.length > 0, section.id);
    setInstallationIdentity({installationId: 'm0_005_settings_installation_0000000000', name: 'Named phone', platform: 'android', app: 'Portico', appVersion: 'test'});
    const namedContext = nativeSettingsContext(source('owner'), 'phone');
    const namedRows = settingsStructure(namedContext).headings.flatMap(heading => heading.pages).flatMap(page => page.sections).flatMap(section => section.rows);
    assert.equal(namedContext.capabilities.deviceName, true);
    assert.ok(namedRows.some(row => row.kind === 'custom' && row.id === 'deviceName'), 'the real installation-name capability admits its panel');
  } finally {setInstallationIdentity(original);}
});

test('the dashboard route stays the owner and administrator read-only page on television', () => {
  const member = settingsStructure(nativeSettingsContext(source('member'), 'tv'));
  assert.equal(member.headings.some(heading => heading.id === 'server'), false);
  const owner = settingsStructure(nativeSettingsContext(source('owner'), 'tv'));
  assert.deepEqual(owner.headings.find(heading => heading.id === 'server')?.pages.map(page => page.id), ['dashboard']);
});


test('main local member can manage password and devices without owner-only Server Sign-in powers', () => {
  const member = source('member');
  assert.equal(member.account.capabilities.manageSignIn, false);
  const context = nativeSettingsContext(member, 'phone');
  assert.equal(context.capabilities.manageSignIn, true);
  assert.equal(context.capabilities.manageDevices, true);
});

test('a missing session, secondary profile, or revoked account-management capability never exposes security panels', () => {
  const main = source('member'), session = main.session();
  const denied = [
    {...main, session: () => undefined},
    {...main, session: () => ({...session, viewer: {...session.viewer, profileId: 'secondary'}})},
    {...main, account: {...main.account, canManage: false, capabilities: {...main.account.capabilities, manageAccount: false}}},
    {...main, account: {...main.account, account: {...main.account.account, disabled: true}}},
  ];
  for (const value of denied) {
    const context = nativeSettingsContext(value, 'phone');
    assert.equal(context.capabilities.manageSignIn, false); assert.equal(context.capabilities.manageDevices, false);
    const rows = settingsStructure(context).headings.flatMap(heading => heading.pages).flatMap(page => page.sections).flatMap(section => section.rows);
    assert.equal(rows.some(row => row.kind === 'custom' && ['password', 'twoStep', 'devices'].includes(row.id)), false);
  }
});
