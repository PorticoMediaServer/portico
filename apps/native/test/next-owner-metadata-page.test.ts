import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {settingsSpec, settingsRowIndex} from '../../../packages/client-core/src/presentation/settings-spec.ts';

type Node = {type: any; props: Record<string, any>};
const nodes = (node: any): Node[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.trailing), ...nodes(node.props.footer)] : [];
const byId = (node: unknown, id: string) => nodes(node).find(value => value.props.testID === id);
const t = (id: string) => id;

async function fixture(legacy = false) {
  const outer = hooks(), inner = hooks(); let active = outer, setup: () => void = () => {};
  const react = {...outer.react, useState: (...args: unknown[]) => active.react.useState(...args), useEffect: (fn: () => void) => {setup = fn;}, useCallback: (fn: unknown) => fn,
    useSyncExternalStore: (_subscribe: unknown, get: () => unknown) => get()};
  const data = {revision: 7, localArtworkLimitBytes: 2 ** 30, localArtworkUsedBytes: 0, includeAdult: true, artworkMaxEdge: 1920, tmdbCredentialConfigured: true,
    tvdbCredentialConfigured: false, openSubtitlesKeyConfigured: false, openSubtitlesPasswordConfigured: true, openSubtitlesUsername: 'viewer', openSubtitles: {},
    ...(legacy ? {} : {onlineLookups: true, defaultLanguage: 'en', defaultRegion: 'US', artworkPreference: 'folders', artworkLanguage: 'same'})};
  const writes: unknown[] = [], calls: string[] = [], state = {data, busy: false, needsReload: false};
  class Controller {
    getSnapshot = () => state; subscribe = () => () => {}; load = async () => {}; dispose() {}
    save = async (original: unknown, patch: unknown) => {assert.equal(original, data); writes.push(patch); return true;};
    checkOpenSubtitles = async (original: unknown) => {assert.equal(original, data); calls.push('test');};
  }
  const scope = {api: {}, lifetime: {current: () => true}, authority: {profileId: 'owner'}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/MetadataPage.tsx', import.meta.url), {
    react, 'react-native': {Linking: {openURL: async () => {}}, Text: 'Text', View: 'View', ScrollView: 'ScrollView'},
    '../../../../../../packages/client-core/src/metadata-settings': {artworkLimitText: (value: number) => String(value / 2 ** 30), openSubtitlesSummary: () => ({text: 'Saved status'})},
    '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec, settingsRowIndex}, '../../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/Select': {Select: 'Select'}, '../../../kit/controls/Switch': {Switch: 'Switch'},
    '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/overlays/BottomSheet': {BottomSheet: 'BottomSheet'},
    '../../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'}, '../../../kit/states': {StateView: 'StateView'}, './page-layout': {settingsPageInset: () => ({}), settingsPageHeading: () => ({})}, '../../../kit/shell/visual': {shellType: () => ({})},
    '../../../kit/theme': {useTheme: () => ({colors: {text: '#fff', textSecondary: '#aaa'}, type: {caption: {}}})},
    '../use-settings-request-scope': {useSettingsRequestScope: () => scope}, '../owner-libraries/controls': {LibraryGroup: 'LibraryGroup', LibraryRow: 'LibraryRow', SectionTitle: 'SectionTitle'},
    './AdvancedSection': {NativeAdvancedSection: 'NativeAdvancedSection'}, './metadata-controller': {NativeMetadataController: Controller},
  });
  const props = {ports: {context: {serverName: 'Server', account: {canManage: true, account: {role: 'owner', primaryProfileId: 'owner'}}}},
    runtime: {text: {t}, reportError() {}, error: () => ({title: 'Error'})}, onLibraries: () => calls.push('libraries')};
  outer.render(() => module.NativeServerMetadata(props)); setup();
  const page = outer.render(() => module.NativeServerMetadata(props)) as Node;
  return {writes, calls, render: () => {active = inner; return inner.render(() => page.type(page.props));}};
}

test('Metadata exposes only published settings and routes row edits through the mounted controller', async () => {
  const f = await fixture(), tree = f.render();
  for (const id of ['metadata.online', 'metadata.adult', 'metadata.language', 'metadata.region', 'metadata.artworkPrefer', 'metadata.artworkLanguage', 'metadata.artworkLimit', 'metadata.artworkSize']) assert.ok(byId(tree, id), id);
  for (const id of ['metadata.refresh', 'metadata.refreshAll', 'metadata.fallbackLanguage', 'metadata.animeTitles', 'metadata.castPhotos', 'metadata.acoustid']) assert.equal(byId(tree, id), undefined, id);
  byId(tree, 'metadata.adult')!.props.onCheckedChange(false); byId(tree, 'metadata.language')!.props.onValueChange('fr');
  assert.deepEqual(f.writes, [{includeAdult: false}, {defaultLanguage: 'fr'}]);
  const libraryLink = nodes(tree).find(value => value.props.label === t(settingsRowIndex.get('metadata.perLibrary')!.row.label))!;
  libraryLink.props.onPress(); assert.deepEqual(f.calls, ['libraries']);
  const legacy = await fixture(true), older = legacy.render();
  assert.equal(byId(older, 'metadata.online'), undefined); assert.equal(byId(older, 'metadata.language'), undefined); assert.ok(byId(older, 'metadata.adult'));
});

test('credential editing starts empty and sends only the explicitly edited credential', async () => {
  const f = await fixture(); let tree = f.render();
  const provider = nodes(tree).find(value => value.type === 'LibraryRow' && value.props.label === t(settingsRowIndex.get('metadata.tmdb')!.row.label))!;
  nodes(provider.props.trailing).find(value => value.type === 'Button')!.props.onPress(); tree = f.render();
  const field = byId(tree, 'metadata-credential-value')!; assert.equal(field.props.value, ''); assert.equal(field.props.secureTextEntry, true);
  field.props.onChangeText('synthetic-new-value'); tree = f.render();
  const sheet = nodes(tree).find(value => value.type === 'BottomSheet')!;
  nodes(sheet.props.footer).filter(value => value.type === 'Button')[1]!.props.onPress(); await Promise.resolve();
  assert.deepEqual(f.writes, [{tmdbReadToken: 'synthetic-new-value'}]);
  tree = f.render(); assert.equal(nodes(tree).find(value => value.type === 'BottomSheet')!.props.open, false);
  assert.equal(byId(tree, 'metadata-credential-value')!.props.value, '');
});
