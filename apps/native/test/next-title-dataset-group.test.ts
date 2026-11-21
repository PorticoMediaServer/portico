import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {settingsRowIndex} from '../../../packages/client-core/src/presentation/settings-spec.ts';
import {createStorageForms} from '../src-next/screens/settings-live/server/storage/forms.ts';

// Settings › Metadata › Recommendations on touch.
type Node = {type: any; props: Record<string, any>};
const nodes = (node: any): Node[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.control)] : [];
const byId = (node: unknown, id: string) => nodes(node).find(value => value.props.testID === id);
const t = (id: string) => id;

async function fixture(on: boolean | undefined, unavailable = false) {
  const outer = hooks(), inner = hooks(); let active = outer;
  const react = {...outer.react, useState: (...args: unknown[]) => active.react.useState(...args)};
  let draft: Record<string, unknown> | undefined = unavailable ? undefined : on === undefined ? {} : {download: on};
  let reads = 0;
  const saves: Record<string, unknown>[] = [];
  const titleData = {getSnapshot: () => ({saving: false}), set: (change: (value: Record<string, unknown>) => Record<string, unknown>) => {draft = change(draft ?? {});}, load: async () => {reads++; draft = {download: false};}};
  const group = {mount: 1, forms: {titleData}, set: {save: async () => {if (draft) saves.push(draft);}}};
  const forms = {useStorageForms: (_ports: unknown, ids: readonly string[]) => {assert.deepEqual(ids, ['titleData']);return group;},
    useFormState: () => ({draft, loading: false, unavailable: !draft}), useStorageSet: () => ({saving: false})};
  const props = {ports: {}, runtime: {text: {t}, reportError() {}}};
  const model = await screenModule(new URL('../src-next/pages/shared/settings/useServerMetadata.ts', import.meta.url), {
    react, '../../../../../../packages/client-core/src/metadata-settings': {},
    '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec: [], settingsRowIndex},
    '../../../../../../packages/i18n/src': {}, '../../../screens/settings-live/server/metadata-controller': {},
    '../../../screens/settings-live/server/storage/forms': forms, '../../../screens/settings-live/use-settings-request-scope': {},
  });
  const page = await screenModule(new URL('../src-next/pages/touch/settings/ServerMetadataPage.tsx', import.meta.url), {
    react, '../../../../../../packages/client-core/src/metadata-settings': {}, '../../shared/settings/useServerMetadata': model,
    '../../../kit/controls': {Switch: 'Switch'}, '../../../kit/frames': {SectionCard: 'SectionCard', StateRow: 'StateRow'},
    '../../../kit/overlays': {}, '../../../kit/overlays/surface': {}, '../../../kit/settings/AdvancedSection': {},
    '../../../kit/states': {}, '../../../kit/shell': {SettingsRow: 'SettingsRow'},
  });
  const element: Node = outer.render(() => page.TitleDatasetGroup(props));
  return {saves, reads: () => reads, render: () => {active = inner; return inner.render(() => element.type(element.props));}};
}

test('the native title-data form reads the rebuilt document', async () => {
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: '1', viewerFence: 'fence'};
  const calls: unknown[] = []; let unavailable = false;
  const context = {current: () => true, authority: () => authority, subscribeAuthority: () => () => {},
    account: {serverId: 'server', account: {id: 'owner', role: 'owner', disabled: false, primaryProfileId: 'main'}, capabilities: {manageServer: true}},
    api: {request: async (path: string, method: string) => {
      calls.push([method, path]);
      if (unavailable) throw {status: 404};
      return {name: 'title_data', etag: '"4"', values: {download: false}};
    }},
  };
  const group = createStorageForms(context as never, ['titleData'], error => {throw error;});
  try {
    await group.forms.titleData.load();
    assert.deepEqual(group.forms.titleData.getSnapshot().draft, {download: false});
    assert.deepEqual(calls, [['GET', '/v1/admin/settings/title_data']]);
    unavailable = true;
    await group.forms.titleData.load();
    const missing = group.forms.titleData.getSnapshot();
    assert.equal(missing.unavailable, true);
    assert.equal(missing.loading, false);
  } finally {group.dispose();}
});

test('the switch shows the owner setting, on when never saved, and saves a change at once', async () => {
  const row = settingsRowIndex.get('metadata.titleDataset')!.row;
  for (const [stored, shown] of [[undefined, true], [true, true], [false, false]] as const) {
    const f = await fixture(stored), tree = f.render();
    const control = byId(tree, 'metadata.titleDataset')!;
    assert.equal(control.type, 'Switch');
    assert.equal(control.props.checked, shown, `stored ${stored}`);
    assert.ok(nodes(tree).some(value => value.type === 'SectionCard' && !!value.props.label), 'the Recommendations heading');
    assert.ok(nodes(tree).some(value => value.type === 'SettingsRow' && value.props.label === row.label && value.props.description === row.help));
  }
  const f = await fixture(true);
  byId(f.render(), 'metadata.titleDataset')!.props.onCheckedChange(false);
  await Promise.resolve();
  assert.equal(f.saves.length, 1);
  assert.deepEqual(f.saves[0], {download: false});
});

test('a 404 shows the unavailable row and Retry without exposing the setting', async () => {
  const f = await fixture(undefined, true), tree = f.render();
  const unavailable = nodes(tree).find(value => value.type === 'StateRow');
  assert.equal(byId(tree, 'metadata.titleDataset'), undefined);
  assert.equal(unavailable?.props.kind, 'error');
  assert.equal(unavailable?.props.action.label, 'action.tryAgain');
  unavailable?.props.action.onPress();
  assert.equal(f.reads(), 1);
  assert.equal(byId(f.render(), 'metadata.titleDataset')?.props.checked, false);
  assert.deepEqual(f.saves, []);
});
