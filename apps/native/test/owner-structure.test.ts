import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowId, settingsStructure, type SettingsPage, type SettingsStructure} from '../../../packages/client-core/src/presentation/index.ts';

// The native clients consume the shared structure, including its phone-only source editor.
const owner = {owner: true, capabilities: {accessTokens: false}};
const member = {owner: false, capabilities: {accessTokens: true}};
const administrator = {owner: false, administrator: true, capabilities: {accessTokens: true}};
const on = (context: typeof owner | typeof member | typeof administrator, television: boolean) => settingsStructure({...context, platform: television ? 'tv' : 'phone'});
const heading = (s: SettingsStructure, id: 'account' | 'server') => s.headings.find(h => h.id === id);
const page = (s: SettingsStructure, h: 'account' | 'server', id: string): SettingsPage => heading(s, h)!.pages.find(p => p.id === id)!;
const sections = (p: SettingsPage) => p.sections.map(s => s.id);
const panels = (p: SettingsPage) => p.sections.flatMap(s => s.rows.filter(r => r.kind === 'custom').map(settingsRowId));

test('an owner on a phone or tablet has Integrations and Metadata, where the web has them', () => {
  const phone = on(owner, false);
  const web = settingsStructure({...owner, platform: 'web'});
  const ids = heading(phone, 'server')!.pages.map(p => p.id);
  assert.deepEqual(ids.filter(id => id !== 'remote-sources'), heading(web, 'server')!.pages.map(p => p.id));
  assert.equal(ids[ids.indexOf('remote-sources') + 1], 'storage', 'the native source editor precedes Storage');
  assert.equal(ids[ids.indexOf('metadata') + 1], 'segments', 'intro/credits review follows Metadata');
  assert.equal(ids[ids.indexOf('libraries') + 1], 'metadata');
  assert.equal(ids[ids.indexOf('people') + 1], 'integrations');
});

test('Integrations matches the web: API keys and rate limit, webhooks and assistants without retired member-token controls', () => {
  const integrations = page(on(owner, false), 'server', 'integrations');
  assert.deepEqual(integrations.tabs?.map(t => t.id), ['keys', 'webhooks', 'assistants']);
  assert.deepEqual(panels(integrations), ['server.apiKeys', 'server.webhooks', 'server.assistants']);
  const settings = integrations.sections.flatMap(s => s.rows.filter(r => r.kind === 'server-setting').map(settingsRowId));
  assert.deepEqual(settings, ['runtime.apiRequestsPerMinute']);
  assert.deepEqual(integrations, page(settingsStructure({...owner, platform: 'web'}), 'server', 'integrations'));
});

test('the keys list is in one place: Integrations, not People', () => {
  const people = page(on(owner, false), 'server', 'people');
  assert.equal(panels(people).includes('server.apiKeys'), false);
  assert.deepEqual(people.tabs?.map(t => t.id), ['accounts', 'invitations', 'devices']);
});

test('Storage, Streaming, Troubleshooting and General carry their owner panels in the web’s order', () => {
  const phone = on(owner, false);
  const storage = sections(page(phone, 'server', 'storage'));
  assert.deepEqual(storage.slice(storage.indexOf('retention'), storage.indexOf('sources') + 1), ['retention', 'usage', 'places', 'downloadUsage', 'sources']);
  const trouble = sections(page(phone, 'server', 'troubleshooting'));
  assert.deepEqual(trouble.slice(trouble.indexOf('capabilities'), trouble.indexOf('export') + 1), ['capabilities', 'setAside', 'clientReports', 'skipped', 'export']);
  const general = sections(page(phone, 'server', 'general'));
  assert.deepEqual(general.slice(general.indexOf('updates'), general.indexOf('updates') + 2), ['updates', 'restart']);
  assert.deepEqual(panels(page(phone, 'server', 'metadata')), ['server.metadataSettings']);
  assert.equal(panels(page(phone, 'server', 'streaming')).at(-1), 'server.playbackProblems');
});

test('pausing background work and the guide limits were already a phone’s, and stay', () => {
  const phone = on(owner, false);
  assert.ok(panels(page(phone, 'server', 'schedule')).includes('server.backgroundPause'));
  const live = page(phone, 'server', 'live').sections.flatMap(s => s.rows.map(settingsRowId));
  for (const row of ['liveDefaults.guideDays', 'liveDefaults.importLimits.channels', 'liveDefaults.importLimits.programmes', 'liveDefaults.importLimits.bytes']) assert.ok(live.includes(row), row);
});

test('a member has no Server heading, and their own access tokens under Profile, before Sign out', () => {
  const phone = on(member, false);
  assert.equal(heading(phone, 'server'), undefined);
  const profile = sections(page(phone, 'account', 'profile'));
  assert.equal(profile[profile.indexOf('tokens') + 1], 'session');
  assert.deepEqual(page(phone, 'account', 'profile').sections.find(s => s.id === 'tokens')!.rows.map(settingsRowId), ['accessTokens']);
});

test('the owner makes keys, not personal tokens: no tokens row for them', () => {
  assert.equal(sections(page(on(owner, false), 'account', 'profile')).includes('tokens'), false);
});

test('an administrator has the Server sections the server lets them use, on a phone and (Watching now) on television', () => {
  const phone = on(administrator, false);
  assert.deepEqual(heading(phone, 'server')!.pages.map(p => p.id), ['dashboard', 'people', 'troubleshooting']);
  assert.deepEqual(panels(page(phone, 'server', 'dashboard')), ['server.nowPlaying']);
  assert.deepEqual(panels(page(phone, 'server', 'people')), ['server.accounts', 'server.invitations', 'server.devices']);
  assert.deepEqual(panels(page(phone, 'server', 'troubleshooting')), ['server.logs', 'server.setAsideTitles']);
  const tv = on(administrator, true);
  assert.deepEqual(heading(tv, 'server')!.pages.map(p => p.id), ['dashboard']);
  assert.deepEqual(panels(page(tv, 'server', 'dashboard')), ['server.nowPlaying']);
});

test('television gets none of it: the Dashboard alone for an owner, nothing for a member', () => {
  const tv = on(owner, true);
  assert.deepEqual(heading(tv, 'server')!.pages.map(p => p.id), ['dashboard']);
  assert.equal(sections(page(tv, 'account', 'profile')).includes('tokens'), false);
  const memberTV = on(member, true);
  assert.equal(heading(memberTV, 'server'), undefined);
  assert.equal(sections(page(memberTV, 'account', 'profile')).includes('tokens'), false);
});

test('Card size, Show backdrops and the caption style are a phone’s and a television’s rows too', () => {
  for (const television of [false, true]) {
    const s = settingsStructure({owner: false, platform: television ? 'tv' : 'phone', capabilities: {preferenceKeys: new Set(['appearance.cardSizePercent', 'appearance.showBackdrops', 'playback.subtitleSize', 'playback.subtitleBackground', 'notifications.badges'])}});
    const appearance = page(s, 'account', 'appearance').sections.flatMap(x => x.rows.map(settingsRowId));
    assert.deepEqual(appearance.slice(0, 3), ['customizeHome', 'cardSize', 'appearance.showBackdrops']);
    const captions = page(s, 'account', 'languages').sections.find(x => x.id === 'appearance')!;
    assert.deepEqual(captions.rows.map(settingsRowId), ['playback.subtitleSize', 'playback.subtitleBackground']);
  }
  // A server that does not publish the card size has no row for it.
  const bare = settingsStructure({owner: false, platform: 'phone', capabilities: {preferenceKeys: new Set(['appearance.showBackdrops'])}});
  assert.equal(page(bare, 'account', 'appearance').sections.flatMap(x => x.rows.map(settingsRowId)).includes('cardSize'), false);
});
