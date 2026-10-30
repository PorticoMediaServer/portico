import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {settingsSpec} from '@core/presentation/settings-spec.ts';
import {RemoteFormSource} from '../src/screens/settings/server-remote/remote-form-source.ts';
import {transformTsx} from './transform-tsx.mjs';
const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module' as const, source: 'export default new Proxy({}, {get:(_,k)=>String(k)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module' as const, source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ServerRemotePage} = await import('../src/screens/settings/server-remote/ServerRemotePage.tsx');
const {SettingsRows} = await import('../src/kit/settings/SettingsRows.tsx');
test('the actual Remote page renders the five approved controls in spec order through its one form', async () => {
  const api = {request: async <T>() => ({authorityId: 'original', generation: '1', state: 'reachable',
    config: {revision: '1', enabled: true, mapping: true, pcp: true, natpmp: false, upnp: true, publicPort: 32500, gateway: '192.0.2.1', ipv6Open: false},
    topology: {lan: [], public: [], bind: '0.0.0.0'}, candidates: [], mappings: []}) as T};
  const source = new RemoteFormSource(api, () => true, true);
  try {
    await source.load();
    const section = settingsSpec.find(section => section.scope === 'server' && section.id === 'remote')!;
    const render = () => renderToStaticMarkup(createElement(ServerRemotePage, {api, isCurrent: () => true, source, groups: null,
      advanced: createElement(SettingsRows, {holder: {advanced: section.advanced}, environment: {platform: 'web', os: 'browser', role: 'owner', signIn: 'local'},
        state: row => source.rowState(row), change: (row, value) => source.change(row.id, value), reset: row => source.reset(row.id),
        renderSpecial: () => null, advancedOpen: true, onAdvancedChange() {}})}));
    const html = render();
    const ids = ['remote.mapping', 'remote.gateway', 'remote.mappingProtocols', 'remote.publicPort', 'remote.ipv6'];
    for (const id of ids) assert.ok(html.includes(`id="${id}"`), id);
    assert.deepEqual(ids.map(id => html.indexOf(`id="${id}"`)), ids.map(id => html.indexOf(`id="${id}"`)).sort((a, b) => a - b));
    assert.equal((html.match(/role="switch"/g) ?? []).length, 1, 'only approved IPv6 switch; no retired remote master toggle');
    assert.ok(html.includes('192.0.2.1'));assert.ok(html.includes('32500'));
    source.change('remote.publicPort', 40000);const edited = render();
    assert.ok(!edited.includes('>Discard<'));assert.ok(!edited.includes('>Save<'));
    assert.ok(edited.includes('Changed'));assert.ok(edited.includes('Reset'));
  } finally {source.dispose();}
});
test.after(() => loader.deregister());
