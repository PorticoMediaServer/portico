import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {routes} from '../../packages/contracts/src/routes.ts';
import {serverTierMayCall, serverTierMayCallAccess} from '../../packages/client-core/src/server-admin/route-access.ts';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {troubleshootingParts, troubleshootingRoutes, troubleshootingRowShown} = await import('../src/screens/settings/server/troubleshooting/parts.ts');
const {createTroubleshootingSurfaces} = await import('../src/screens/settings/server/troubleshooting/index.tsx');
const {MessagesPanel} = await import('../src/screens/settings/server/troubleshooting/Messages.tsx');
const {SettingsMessagesSource} = await import('../src/screens/settings/server/troubleshooting/messages-source.ts');

const settings = {revision: 1, logLevel: 'info', effectiveLevel: 'info', retention: [], levels: ['error', 'warn', 'info', 'debug'], categories: ['server'], logDetailMinutes: 30,
  debugWindowUntil: new Date(Date.now() + 600_000).toISOString()};
const page = {items: [], limit: 200, nextCursor: '', level: 'info', effectiveLevel: 'info'};
const transport = {request: async <T>(path: string) => (path.includes('/settings') ? settings : page) as T};

test('a tier may call a route as routes.ts publishes it: the log reads are administrators\', the debug window and log settings changes the owner\'s', () => {
  for (const id of ['get_admin_logs', 'get_admin_logs_events', 'get_admin_logs_settings'] as const) {
    assert.equal(serverTierMayCall(id, 'admin'), true, id);
    assert.equal(serverTierMayCall(id, 'owner'), true, id);
  }
  for (const id of ['post_admin_logs_debug_window', 'patch_admin_logs_settings', 'get_client_logs', 'get_capability_report', 'list_playback_failure_memory'] as const) {
    assert.equal(serverTierMayCall(id, 'admin'), false, id);
    assert.equal(serverTierMayCall(id, 'owner'), true, id);
  }
  // A device route that asks for administration publishes no administrator tier, so it stays the owner's.
  assert.equal(serverTierMayCall('get_admin_feedback_reports', 'admin'), false);
});

test('an administrator may call only the access kinds named for it; an unknown or future kind is not allowed', () => {
  assert.equal(serverTierMayCallAccess({access: 'admin_scope'}, 'admin'), true);
  for (const access of ['public', 'device', 'viewer_item_restrictions']) {
    assert.equal(serverTierMayCallAccess({access}, 'admin'), true, access);
    assert.equal(serverTierMayCallAccess({access, requires: 'admin'}, 'admin'), false, `${access} asking for administration`);
  }
  for (const access of ['owner', 'admin_owner', 'future_kind', '']) assert.equal(serverTierMayCallAccess({access}, 'admin'), false, access);
  assert.equal(serverTierMayCallAccess({access: 'future_kind'}, 'owner'), true, 'the owner may call any route');
  // Every access kind main publishes is one the check names, so none reaches it by falling through.
  const named = new Set(['admin_scope', 'owner', 'public', 'device', 'viewer_item_restrictions']);
  for (const route of routes) assert.ok(named.has(route.access), `${route.id}: ${route.access}`);
});

test('every route a Troubleshooting part names is a published route', () => {
  const published = new Set(routes.map(route => route.id as string));
  for (const [part, ids] of Object.entries(troubleshootingRoutes)) for (const id of ids) assert.ok(published.has(id), `${part}: ${id}`);
});

test('an administrator gets Messages and stuck files (spec: "Administrators see Messages and stuck files"); the owner gets every part', () => {
  assert.deepEqual([...troubleshootingParts('admin')].sort(), ['messages', 'stuckFiles']);
  // Stuck files light up from routes.ts alone: its one route publishes admin_scope.
  assert.deepEqual(troubleshootingRoutes.stuckFiles, ['get_catalogue_failures']);
  assert.equal(serverTierMayCall('get_catalogue_failures', 'admin'), true);
  assert.ok(!troubleshootingRoutes.reports.includes('get_catalogue_failures' as never), 'stuck files are not gated by the owner\'s app logs');
  assert.equal(troubleshootingRowShown('troubleshooting.reports', 'admin'), true, 'the reports row opens stuck files for an administrator');
  assert.deepEqual([...troubleshootingParts('owner')].sort(), Object.keys(troubleshootingRoutes).sort());
  assert.equal(troubleshootingRowShown('troubleshooting.messages', 'admin'), true);
  for (const row of ['troubleshooting.detailWindow', 'troubleshooting.logLevel', 'troubleshooting.keepLogs', 'troubleshooting.feedback', 'troubleshooting.support', 'troubleshooting.exportWindow', 'troubleshooting.playbackProblems', 'troubleshooting.someNewRow'])
    assert.equal(troubleshootingRowShown(row, 'admin'), false, row);
  for (const row of ['troubleshooting.detailWindow', 'troubleshooting.logLevel', 'troubleshooting.someNewRow', 'storage.backups']) assert.equal(troubleshootingRowShown(row, 'owner'), true, row);
  assert.equal(troubleshootingRowShown('storage.backups', 'admin'), true, 'a row outside Troubleshooting is not this page\'s to decide');
});

const surfaces = (tier: 'owner' | 'admin') => createTroubleshootingSurfaces({tier, transport: transport as never, serverId: 'server', viewerId: 'viewer', downloadBase: 'https://server.invalid',
  isCurrent: () => true, subscribe: () => () => {}, reportError: () => {}});

test('an administrator\'s landing offers Messages and Stuck files, with no detail window control and no export', () => {
  const admin = surfaces('admin');
  const html = renderToStaticMarkup(createElement('div', null, admin.render(createElement('p', null, 'LOG SETTINGS'))));
  assert.match(html, /Messages/);
  assert.match(html, /<b>Stuck files<\/b><p>Files Portico couldn’t prepare\.<\/p>/);
  assert.match(html, /Open stuck files/);
  for (const absent of [/Record more detail/, /Problems people reported/, /App logs and stuck files/, /What this server can do/, /LOG SETTINGS/]) assert.doesNotMatch(html, absent);
  admin.dispose();
  const owner = surfaces('owner');
  const full = renderToStaticMarkup(createElement('div', null, owner.render(createElement('p', null, 'LOG SETTINGS'))));
  for (const present of [/Record more detail/, /Messages/, /Problems people reported/, /App logs and stuck files/, /What this server can do/, /LOG SETTINGS/]) assert.match(full, present);
  assert.doesNotMatch(full, /Open stuck files/);
  owner.dispose();
});

test('the Messages viewer shows an administrator whether a detail window is open, with no button to change it', async () => {
  const source = new SettingsMessagesSource(transport, undefined, () => true);
  await source.load();
  const admin = renderToStaticMarkup(createElement(MessagesPanel, {source, canDetail: false, reportError: () => {}}));
  const owner = renderToStaticMarkup(createElement(MessagesPanel, {source, reportError: () => {}}));
  assert.match(admin, /until/i);
  assert.equal((owner.match(/<button/g) ?? []).length - (admin.match(/<button/g) ?? []).length, 1);
  source.dispose();
});

test('Settings builds the page for owners and administrators, and passes log settings and Playback problems only with their parts', () => {
  const route = readFileSync(new URL('../src/screens/settings/Settings.route.tsx', import.meta.url), 'utf8');
  assert.match(route, /route\.section === 'troubleshooting' && \(role === 'owner' \|\| role === 'admin'\)[^\n]*createTroubleshootingSurfaces\(\{tier: role,/);
  assert.match(route, /const playbackProblems = troubleshooting\?\.parts\.has\('problems'\) \?/);
  assert.match(route, /troubleshooting\?\.render\(troubleshooting\.parts\.has\('logSettings'\) && <MessageOptions>/);
  const controller = readFileSync(new URL('../src/screens/settings/controller.ts', import.meta.url), 'utf8');
  assert.match(controller, /if \(section\.id === 'troubleshooting'\) return this\.options\.capabilities\.manageServer && this\.options\.role !== 'member' && troubleshootingParts\(this\.options\.role\)\.size > 0;/);
  const bindings = readFileSync(new URL('../src/screens/settings/bindings.ts', import.meta.url), 'utf8');
  assert.match(bindings, /section\.id === 'troubleshooting' && troubleshootingParts\('admin'\)\.size > 0 \? 'administrators' : 'owner'/);
});

test('an administrator\'s Reports source reads only the stuck files; the owner\'s reads app logs, records and stuck files', async () => {
  const {SettingsReportsSource} = await import('../src/screens/settings/server/troubleshooting/reports-source.ts');
  const read = async (sections?: {reports: boolean; stuckFiles: boolean}) => {
    const paths: string[] = [];
    const api = {request: async <T>(path: string) => {paths.push(path.split('?')[0]!);
      return (path.includes('catalogue/failures') ? {scope: {serverId: 'server', viewerFence: 'f'}, data: {failures: [], more: false}} : {items: [], nextCursor: ''}) as T;}};
    const source = new SettingsReportsSource(api as never, 'server', 'viewer', () => true, () => {}, sections);
    await source.load();source.dispose();
    return paths;
  };
  assert.deepEqual(await read({reports: false, stuckFiles: true}), ['/v1/admin/catalogue/failures']);
  const owner = await read();
  assert.ok(owner.includes('/v1/admin/catalogue/failures') && owner.includes('/v1/admin/diagnostics/client-logs'), owner.join(' '));
});

test('a stuck-files-only Reports viewer draws stuck files alone, and says when none are stuck', async () => {
  const {ReportsPanel} = await import('../src/screens/settings/server/troubleshooting/Reports.tsx');
  const snapshot = {phase: 'ready', reports: [], setAside: {failures: [], more: false}};
  const source = {getSnapshot: () => snapshot, load: async () => {}, checkSetAside: async () => {}} as never;
  const html = renderToStaticMarkup(createElement(ReportsPanel, {source, subscribe: () => () => {}, isCurrent: () => true, downloadBase: '', reportError: () => {}, appLogs: false}));
  assert.match(html, /No files are stuck\./);
  assert.doesNotMatch(html, /Client reports/);
});
