import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as choices from '../../../packages/client-core/src/remote-source-choice.ts';
import * as presentation from '../src-next/screens/settings/remote-sources/reading-presentation.ts';

type Node = {type: unknown; props: Record<string, any>};
const elements = (node: any): Node[] => Array.isArray(node) ? node.flatMap(elements) : node?.props ? [node, ...elements(node.props.children), ...elements(node.props.footer), ...elements(node.props.trailing)] : [];
const ofType = (node: unknown, type: string) => elements(node).filter(element => element.type === type);
const words = (node: any): string => Array.isArray(node) ? node.map(words).join(' ') : node?.props ? words(node.props.children) : typeof node === 'string' ? node : '';
const theme = {colors: {ink: '#000', raised: '#111', text: '#fff', textSecondary: '#aaa', accent: '#4466cc', accentStrong: '#4466cc', lineSoft: '#333', lineStrong: '#777'}, type: {body: {}, bodyStrong: {}, caption: {}}};
const t = (id: string) => id;
const presets = {
  list: {scanDepth: 'file_list_only', wholeFileReads: false, connections: 2, requestsPerSecond: 2, bytesPerSecond: 0},
  once: {scanDepth: 'basic', wholeFileReads: false, connections: 4, requestsPerSecond: 4, bytesPerSecond: 0},
  everything: {scanDepth: 'complete', wholeFileReads: false, connections: 8, requestsPerSecond: 10, bytesPerSecond: 0},
  ask: {scanDepth: 'basic', wholeFileReads: false, connections: 2, requestsPerSecond: 2, bytesPerSecond: 0},
};
const sourceNamespace = {sourceReadPreset: (preset: keyof typeof presets) => presets[preset], sourceReadPresetName: (values: unknown) => Object.entries(presets).find(([, value]) => JSON.stringify(value) === JSON.stringify(values))?.[0]};
const corePath = '../../../../../../packages/client-core/src/remote-sources';
const rowPath = '../../settings-live/owner-libraries/controls';

test('reading renders four real choice handlers and opens a validated number sheet for a changed limit', async () => {
  const h = hooks(), calls: unknown[] = [];
  const module = await screenModule(new URL('../src-next/screens/settings/remote-sources/SourceReadingControls.tsx', import.meta.url), {
    react: h.react, 'react-native-svg': {default: 'Svg', Path: 'Path'}, 'react-native': {Text: 'Text', View: 'View'}, [corePath]: sourceNamespace, [corePath.replace('remote-sources', 'remote-source-choice')]: choices, '../../../kit/Icon': {Icon: 'Icon'}, '../../../../../../packages/i18n/src': {defaultI18n: {t, bytes: (value: number) => String(value)}},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/Focusable': {Focusable: 'Focusable'},
    '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/overlays/BottomSheet': {BottomSheet: 'BottomSheet'},
    '../../../kit/shell/visual': {alpha: (value: string) => value}, '../../../kit/theme': {useTheme: () => theme},
    [rowPath]: {LibraryGroup: 'LibraryGroup', LibraryRow: 'LibraryRow', SectionTitle: 'SectionTitle'}, './reading-presentation': presentation,
  });
  const definitions = Object.entries(presets).map(([name, reading]) => ({name, definitionRevision: 1, reading, advanced: {scanIntervalSeconds: 21600, cacheBytes: 8 * 1024 ** 3}}));
  const profile = {values: presets.once, configurationGeneration: '1', choiceDefinitions: definitions, choiceApplicableFields: ['scanIntervalSeconds', 'scanDepth', 'cacheBytes']};
  const props = {profile, comparison: {name: 'once', applicableFields: ['connections', 'scanDepth', 'cacheBytes'], changedFields: ['connections']}, values: {...presets.once, connections: 5}, baseline: presets.once, disabled: false, t,
    onSelect: (value: unknown) => calls.push(['preset', value]), onLimit: (...value: unknown[]) => calls.push(['limit', ...value]), onReset: () => calls.push(['reset'])};
  const render = () => h.render(() => module.SourceReadingControls(props));
  let tree = render();
  const cards = ofType(tree, 'Focusable'); assert.equal(cards.length, 4); assert.ok(cards.every(value => value.props.accessibilityRole === 'radio'));
  cards[2]!.props.onPress(); assert.deepEqual(calls.pop(), ['preset', definitions[2]]);
  const limits = ofType(tree, 'LibraryRow'); assert.equal(limits.length, 3); assert.match(limits[0]!.props.help, /Changed/);
  limits[2]!.props.onPress(); tree = render();
  assert.equal(ofType(tree, 'BottomSheet')[0]!.props.open, true);
  ofType(tree, 'TextField')[0]!.props.onChangeText('1001'); tree = render();
  let sheet = ofType(tree, 'BottomSheet')[0]!; assert.equal(ofType(sheet.props.footer, 'Button')[0]!.props.disabled, true);
  ofType(tree, 'TextField')[0]!.props.onChangeText('25'); tree = render(); sheet = ofType(tree, 'BottomSheet')[0]!;
  ofType(sheet.props.footer, 'Button')[0]!.props.onPress(); assert.deepEqual(calls.pop(), ['limit', 'bytesPerSecond', 25_000_000]);
  tree = render(); assert.equal(ofType(tree, 'BottomSheet')[0]!.props.open, false);
  props.profile = {...profile, choiceDefinitions: []}; tree = render(); assert.equal(ofType(tree, 'Focusable').length, 0, 'withheld definitions have no named cards');
  const section = ofType(tree, 'SectionTitle')[0]!; ofType(section.props.action, 'Button')[0]!.props.onPress(); assert.deepEqual(calls.pop(), ['reset']);
});

async function connectionFixture(testConnection: (input: unknown) => Promise<unknown>) {
  const h = hooks();
  const react = {...h.react, createContext: (value: unknown) => ({value})};
  const module = await screenModule(new URL('../src-next/screens/settings/remote-sources/RemoteSourceForms.tsx', import.meta.url), {'../../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true}, '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, 
    react, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView'}, [corePath]: sourceNamespace, [corePath.replace('remote-sources', 'remote-source-choice')]: choices, '../../../kit/Icon': {Icon: 'Icon'}, '../../../../../../packages/i18n/src': {defaultI18n: {t, bytes: (value: number) => String(value)}},
    '../../../kit/controls/Button': {Button: 'Button'},
    '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/controls/Switch': {Switch: 'Switch'}, '../../../kit/theme': {useTheme: () => theme},
    './SourceAnalysisConsent': {SourceAnalysisConsent: 'SourceAnalysisConsent'}, './SourceReadingControls': {SourceReadingControls: 'SourceReadingControls'},
    './ConnectionTestCard': {ConnectionTestCard: 'ConnectionTestCard'}, './reading-presentation': presentation,
    [rowPath]: {LibraryGroup: 'LibraryGroup', LibraryRow: 'LibraryRow'},
  });
  const controller = {testConnection, getSnapshot: () => ({sources: {ambiguous: false, operationId: null, receipt: null, rclone: null}})};
  const render = () => h.render(() => module.ConnectionEditor({controller, t, onCancel() {}, onSaved() {}, isCurrent: () => true}));
  let tree = render();
  const fields = ofType(tree, 'TextField'); fields[0]!.props.onChangeText('Archive'); fields[1]!.props.onChangeText('https://storage.example');
  tree = render();
  return {render, tree};
}

test('a successful limited-range test folds the connection into its returned origin and can be edited again', async () => {
  let complete: (value: unknown) => void = () => {};
  const f = await connectionFixture(() => new Promise(resolve => {complete = resolve;}));
  ofType(f.tree, 'Button').find(button => words(button) === 'next.remoteSources.test')!.props.onPress();
  let tree = f.render(); assert.equal(ofType(tree, 'ConnectionTestCard')[0]!.props.testing, true);
  complete({origin: 'https://storage.example', rangeSupport: 'unsupported'}); await Promise.resolve(); await Promise.resolve();
  tree = f.render(); assert.equal(ofType(tree, 'TextField').length, 0);
  assert.equal(ofType(tree, 'ConnectionTestCard')[0]!.props.result.rangeSupport, 'unsupported');
  assert.equal(typeof ofType(tree, 'ConnectionTestCard')[0]!.props.onTestAgain, 'function');
  assert.equal(ofType(tree, 'LibraryRow')[0]!.props.help, 'https://storage.example');
  ofType(tree, 'Button').find(button => words(button) === t('web.selection.edit'))!.props.onPress(); tree = f.render();
  assert.equal(ofType(tree, 'TextField').length, 4); assert.equal(ofType(tree, 'ConnectionTestCard')[0]!.props.result, undefined);
});

test('the normalized credential refusal marks the credential field and offers a fresh test', async () => {
  const f = await connectionFixture(async () => {throw {code: 'remote_credentials_required'};});
  ofType(f.tree, 'Button').find(button => words(button) === 'next.remoteSources.test')!.props.onPress();
  await Promise.resolve(); await Promise.resolve();
  const tree = f.render(), password = ofType(tree, 'TextField').find(field => field.props.secureTextEntry)!;
  assert.match(password.props.error, /Check the user name and password/);
  const card = ofType(tree, 'ConnectionTestCard')[0]!; assert.equal(card.props.error, password.props.error); assert.equal(typeof card.props.onTestAgain, 'function');
  assert.equal(ofType(tree, 'ConnectionTestCard')[0]!.props.result, undefined);
});
