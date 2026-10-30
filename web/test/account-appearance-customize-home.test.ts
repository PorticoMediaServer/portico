import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {settingsSpec, settingsRowIndex} = await import('../../packages/client-core/src/presentation/settings-spec.ts');
const {PersonalSavedService} = await import('../../packages/client-core/src/personal-saved.ts');
const {hasWebPreferenceEffect} = await import('../src/screens/settings/effects.ts');
const {CustomizeHomeRow, customizeHomeBinding} = await import('../src/screens/settings/account-appearance/CustomizeHome.tsx');

const api = {request: async <T,>() => ({}) as T};
const home = {loadLayout: async () => ({view: 'default', rows: []}), saveLayout: async () => ({view: 'default', rows: []}), resetLayout: async () => ({view: 'default', rows: []})};
const options = {api, home: home as never, serverId: 'server', viewerId: '["local","account","profile"]'};

test('the canonical Appearance page keeps Customize Home as its own profile link', () => {
  const appearance = settingsSpec.find(section => section.scope === 'account' && section.id === 'appearance');
  assert.ok(appearance);
  const group = appearance.groups?.[0];
  const row = group?.rows.find(value => value.id === 'appearance.customizeHome');
  assert.ok(row);
  assert.equal(group?.title, undefined);
  assert.equal(row.scope, 'profile');
  assert.equal(row.control.type, 'link');
  assert.equal(row.control.label, 'next.settingsSpec.account.appearance.groups.0.rows.0.control.label');
  assert.equal(row.platforms, undefined);
  assert.equal(appearance.groups?.[1]?.rows.some(value => value.id === 'appearance.cardSize'), true);
});

test('every drawn Display row on web still has a real consumer', () => {
  const appearance = settingsSpec.find(section => section.scope === 'account' && section.id === 'appearance')!;
  const display = appearance.groups?.[1];
  const advanced = appearance.advanced?.groups?.flatMap(group => group.rows) ?? [];
  const rows = [...(display?.rows ?? []), ...advanced].filter(row => !row.platforms || row.platforms.includes('web'));
  assert.deepEqual(rows.map(row => row.id), ['appearance.cardSize', 'appearance.backdrops', 'appearance.motion', 'appearance.cardSizeExact', 'appearance.libraryView', 'appearance.episodesLayout']);
  for (const row of rows) assert.equal(hasWebPreferenceEffect(settingsRowIndex.get(row.id)!.row), true, row.id);
});

test('the drawn control is the link the frame names, and the dialog starts closed', () => {
  const markup = renderToStaticMarkup(createElement(CustomizeHomeRow, {...options, isCurrent: () => true}));
  assert.match(markup, /Customize Home…/);
  assert.doesNotMatch(markup, /role="dialog"/);
});

test('saved views are only created inside a live session, and the request id is real', async () => {
  const retired = customizeHomeBinding({...options, isCurrent: () => false});
  assert.throws(() => retired.createSavedViews(), /shell_authority_retired/);

  const live = customizeHomeBinding({...options, isCurrent: () => true});
  const service = live.createSavedViews();
  assert.ok(service instanceof PersonalSavedService);
  assert.deepEqual(service.scope, {serverId: 'server', viewerId: '["local","account","profile"]'});
  assert.match(await live.requestId(), /\S/);
});
