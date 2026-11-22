import test from 'node:test';
import assert from 'node:assert/strict';

async function hooks() {
  const {hooks, screenModule} = await import('./helpers/screen-harness.mjs'), host = hooks();
  const module = await screenModule(new URL('../src-next/pages/shared/live/useLiveSourceSheets.ts', import.meta.url), {
    react: host.react, '../../../../../../packages/i18n/src': {defaultI18n: {has: () => true}},
    '../../../../../../packages/client-core/src/server-admin/live-words': {importSkipped: () => undefined},
    '../../../screens/settings-live/live-owner-controller': {newRemoteDraft: () => ({kind: 'm3u', name: '', locator: '', guideUrl: '', username: '', password: '', viewerAccess: 'server-members', refreshMs: 86400000, confirmedLanRoots: []})},
  });
  return {host, module};
}
const settle = () => new Promise(resolve => setTimeout(resolve, 0));
const t = (key: string, values?: Record<string, unknown>) => values ? `${key}:${JSON.stringify(values)}` : key;

test('Add source checks first, asks once about a home-network address, then saves the checked preview', async () => {
  const {host, module} = await hooks(), checked: unknown[] = [], saved: string[] = [];
  const controller: any = {lifetime: {authority: {profileId: 'p'}},
    previewRemote: async (draft: any) => {checked.push(draft.confirmedLanRoots); if (!draft.confirmedLanRoots.length) throw {confirmRoots: ['192.168.1.0/24']}; return {previewId: 'pv', preview: {channels: 3, programmes: 9, names: ['A', 'B'], diagnostics: []}};},
    saveRemote: async (id: string) => {saved.push(id); return true;}};
  let done = 0;
  const render = () => host.render(() => module.useAddSource({controller, runtime: {text: {t}}, open: true, onSaved: () => {done++;}}));
  let view: any = render();
  assert.equal(view.ready, false); assert.equal(view.step, 'check');
  view.update({name: 'Home', locator: 'http://192.168.1.5/list.m3u'}); view = render(); assert.equal(view.ready, true);
  view.check(); await settle(); view = render();
  assert.equal(view.step, 'confirm'); assert.deepEqual(view.confirm, ['192.168.1.0/24']);
  view.confirmLan(); await settle(); view = render();
  assert.equal(view.step, 'save'); assert.deepEqual(checked, [[], ['192.168.1.0/24']]);
  assert.deepEqual(view.found.facts, ['server.live.channelCount:{"count":3}', 'server.live.programCount:{"count":9}']);
  view.save(); await settle();
  assert.deepEqual(saved, ['pv']); assert.equal(done, 1);
});

test('the channel map sends only touched channels and says how many landed when a save stops part-way', async () => {
  const {host, module} = await hooks(), sent: unknown[] = [];
  const items = [{channelId: 'a', name: 'One', number: '1', sourceNumber: '1', group: '', guideChannelId: '', hidden: false, overridden: false}, {channelId: 'b', name: 'Two', number: '2', sourceNumber: '2', group: '', guideChannelId: '', hidden: false, overridden: false}];
  const controller: any = {channelMap: async () => ({sourceId: 's', items}), saveChannelMap: async (_id: string, edits: unknown[]) => {sent.push(edits); throw {saved: 1, total: 2};}};
  const source: any = {id: 's', name: 'Synthetic'};
  const render = () => host.render(() => module.useChannelMap({controller, source, open: true}));
  // The harness runs no effects; the first read is the same load Try again runs.
  render().retry(); await settle(); let view: any = render();
  assert.equal(view.rows.length, 2); assert.equal(view.changed, 0);
  view.rows[1].edit({hidden: true}); view = render(); assert.equal(view.changed, 1);
  view.save(); await settle(); await settle(); view = render();
  assert.deepEqual(sent, [[{channelId: 'b', override: {number: '', guideChannelId: '', hidden: true}}]]);
  assert.deepEqual(view.partial, {saved: 1, total: 2});
});
