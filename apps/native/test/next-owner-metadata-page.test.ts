import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {settingsSpec, settingsRowIndex} from '../../../packages/client-core/src/presentation/settings-spec.ts';

type Node = {type: any; props: Record<string, any>};
const nodes = (node: any): Node[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.control), ...nodes(node.props.footer)] : [];
const byId = (node: unknown, id: string) => nodes(node).find(value => value.props.testID === id);
const t = (id: string) => id;

async function fixture(legacy = false) {
  const outer = hooks(), inner = hooks(); let active = outer, setup: () => void = () => {};
  const react = {...outer.react, useState: (...args: unknown[]) => active.react.useState(...args), useRef: (...args: unknown[]) => active.react.useRef(...args),
    useEffect: (fn: () => void) => {setup = fn;}, useCallback: (fn: unknown) => fn,
    useSyncExternalStore: (_subscribe: unknown, get: () => unknown) => get()};
  const data = {revision: 7, localArtworkLimitBytes: 2 ** 30, localArtworkUsedBytes: 0, includeAdult: true, artworkMaxEdge: 1920, tmdbCredentialConfigured: true,
    tvdbCredentialConfigured: false, openSubtitlesKeyConfigured: false, openSubtitlesPasswordConfigured: true, openSubtitlesUsername: 'viewer', openSubtitles: {},
    ...(legacy ? {} : {onlineLookups: true, defaultLanguage: 'en', defaultRegion: 'US', artworkPreference: 'folders', artworkLanguage: 'same'})};
  const writes: unknown[] = [], calls: string[] = [], state = {data, busy: false, needsReload: false, refreshStarted: false};
  class Controller {
    getSnapshot = () => state; subscribe = () => () => {}; load = async () => {}; dispose() {}
    save = async (original: unknown, patch: unknown) => {assert.equal(original, data); writes.push(patch); return true;};
    refreshAll = async (original: unknown) => {assert.equal(original, data); calls.push('refreshAll'); state.refreshStarted = true; return true;};
    checkOpenSubtitles = async (original: unknown) => {assert.equal(original, data); calls.push('test');};
  }
  const scope = {api: {}, lifetime: {current: () => true}, authority: {profileId: 'owner'}};
  const words = {artworkLimitText: (value: number) => String(value / 2 ** 30), openSubtitlesSummary: () => ({text: 'Saved status'})};
  const model = await screenModule(new URL('../src-next/pages/shared/settings/useServerMetadata.ts', import.meta.url), {
    react, '../../../../../../packages/client-core/src/metadata-settings': words,
    '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec, settingsRowIndex},
    '../../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../../screens/settings-live/use-settings-request-scope': {useSettingsRequestScope: () => scope},
    '../../../screens/settings-live/server/metadata-controller': {NativeMetadataController: Controller},
    '../../../screens/settings-live/server/storage/forms': {},
  });
  const module = await screenModule(new URL('../src-next/pages/touch/settings/ServerMetadataPage.tsx', import.meta.url), {
    react, '../../../../../../packages/client-core/src/metadata-settings': words, '../../shared/settings/useServerMetadata': model,
    '../../../kit/controls': {Button: 'Button', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
    '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/overlays/surface': {OverlayFootRow: 'OverlayFootRow'},
    '../../../kit/frames': {SettingsPageFrame: 'SettingsPageFrame', PanelBlock: 'PanelBlock', SectionCard: 'SectionCard', StateRow: 'StateRow'},
    '../../../kit/states': {StateView: 'StateView'}, '../../../kit/settings/SettingsParts': {SettingsHelp: 'Text'},
    '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/settings/AdvancedSection': {NativeAdvancedSection: 'NativeAdvancedSection'},
  });
  const props = {ports: {context: {serverName: 'Server', account: {canManage: true, account: {role: 'owner', primaryProfileId: 'owner'}}}},
    runtime: {text: {t}, reportError() {}, error: () => ({title: 'Error'})}, onLibraries: () => calls.push('libraries')};
  outer.render(() => module.ServerMetadataPage(props)); setup();
  const page = outer.render(() => module.ServerMetadataPage(props)) as Node;
  return {writes, calls, render: () => {active = inner; return inner.render(() => {const content = page.props.children[1];return content.type(content.props);});}};
}

test('Metadata exposes only published settings and routes row edits through the mounted controller', async () => {
  const f = await fixture(), tree = f.render();
  for (const id of ['metadata.refreshAll', 'metadata.online', 'metadata.adult', 'metadata.language', 'metadata.region', 'metadata.artworkPrefer', 'metadata.artworkLanguage', 'metadata.artworkLimit', 'metadata.artworkSize']) assert.ok(byId(tree, id), id);
  for (const id of ['metadata.refresh', 'metadata.fallbackLanguage', 'metadata.animeTitles', 'metadata.castPhotos', 'metadata.acoustid']) assert.equal(byId(tree, id), undefined, id);
  // Values use the kit's inline row control, which leaves half the row for long labels.
  for (const id of ['metadata.language', 'metadata.region', 'metadata.artworkPrefer', 'metadata.artworkLanguage']) {
    const control = byId(tree, id)!;
    assert.equal(nodes(tree).find(value => value.props.control === control)?.type, 'SettingsRow');
    assert.equal(control.props.label, undefined);
    assert.ok(control.props.accessibilityLabel);
  }
  byId(tree, 'metadata.adult')!.props.onCheckedChange(false); byId(tree, 'metadata.language')!.props.onValueChange('fr');
  assert.deepEqual(f.writes, [{includeAdult: false}, {defaultLanguage: 'fr'}]);
  const libraryLink = nodes(tree).find(value => value.props.label === t(settingsRowIndex.get('metadata.perLibrary')!.row.label))!;
  libraryLink.props.onPress(); assert.deepEqual(f.calls, ['libraries']);
  // Recommendations (the title dataset switch) sits after Subtitle search, before Advanced.
  const children = nodes(tree).map(value => value.type?.name ?? value.type);
  assert.ok(children.includes('TitleDatasetGroup'));
  assert.ok(children.indexOf('TitleDatasetGroup') < children.indexOf('NativeAdvancedSection'));
  const legacy = await fixture(true), older = legacy.render();
  assert.equal(byId(older, 'metadata.online'), undefined); assert.equal(byId(older, 'metadata.language'), undefined); assert.ok(byId(older, 'metadata.adult'));
});

test('credential editing starts empty and sends only the explicitly edited credential', async () => {
  const f = await fixture(); let tree = f.render();
  const provider = nodes(tree).find(value => value.type === 'SettingsRow' && value.props.label === t(settingsRowIndex.get('metadata.tmdb')!.row.label))!;
  nodes(provider.props.control).find(value => value.type === 'Button')!.props.onPress(); tree = f.render();
  const field = byId(tree, 'metadata-credential-value')!; assert.equal(field.props.value, ''); assert.equal(field.props.secureTextEntry, true);
  field.props.onChangeText('synthetic-new-value'); tree = f.render();
  const sheet = nodes(tree).find(value => value.type === 'BottomSheet')!;
  nodes(sheet.props.footer).filter(value => value.type === 'Button')[1]!.props.onPress(); await Promise.resolve();
  assert.deepEqual(f.writes, [{tmdbReadToken: 'synthetic-new-value'}]);
  tree = f.render(); assert.equal(nodes(tree).find(value => value.type === 'BottomSheet')!.props.open, false);
  assert.equal(byId(tree, 'metadata-credential-value')!.props.value, '');
});


test('Refresh all confirms before dispatch and shows the spec result after acceptance', async () => {
  const f=await fixture();let tree=f.render();
  byId(tree,'metadata.refreshAll')!.props.onPress();tree=f.render();assert.deepEqual(f.calls,[]);
  const row=settingsRowIndex.get('metadata.refreshAll')!.row;
  const sheet=nodes(tree).find(value=>value.type==='BottomSheet'&&value.props.title===t(row.label))!;
  assert.equal(sheet.props.open,true);assert.ok(JSON.stringify(sheet).includes(row.control.confirm!));
  const confirm=byId(tree,'metadata.refreshAll.confirm')!;assert.ok(confirm);
  confirm.props.onPress();await Promise.resolve();await Promise.resolve();tree=f.render();
  assert.deepEqual(f.calls,['refreshAll']);
  assert.equal(nodes(tree).find(value=>value.type==='SettingsRow'&&value.props.label===t(row.label))!.props.description,'server.metadata.refreshAllRunning');
});
