import test from 'node:test';
import assert from 'node:assert/strict';
import {surfacePaintFixture, screenModule, hooks} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {shellLayout} from '../src-next/shell/model.ts';
const {react, render} = hooks(); let isTV = false, width = 390, height = 844;
const {NativeVersionChooser} = await screenModule(new URL('../src-next/screens/watch/VersionChooser.tsx', import.meta.url), {
  '../../kit/overlays/SurfacePaintHost': surfacePaintFixture,
  react, 'react-native': {Modal: 'Modal', ScrollView: 'ScrollView', StyleSheet: {absoluteFill: {}}, Text: 'Text', View: 'View', useWindowDimensions: () => ({width, height})},
  'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
  '../../../../../packages/i18n/src': {defaultI18n}, '../../shell/model': {shellLayout}, '../../kit/controls': {Focusable: 'Focusable'},
  '../../kit/overlays/TVPanelMotion': {TVPanelSlide: 'TVPanelSlide', TVPanelScrim: 'TVPanelScrim', useTVPanelInteractive: () => true},
  '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/overlays/surface': {FocusBoundary: 'FocusBoundary', PanelBackground: 'PanelBackground'},
  '../../kit/overlays/OverlayHost': {useOverlayLifecycle() {}}, '../../kit/theme': {nativeType: (value: unknown) => value, useTheme: () => ({isTV, colors: {}, type: {caption: {}}})},
});
function descendants(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(descendants);
  if (!node || typeof node !== 'object' || !node.type) return [];
  const children = typeof node.props.children === 'function' ? node.props.children({focused: node.props.testID === 'watch-version-v2'}) : node.props.children;
  return [node, ...descendants(children), ...descendants(node.props.headerContent)];
}
const value = {capture: {entry: {id: 'movie', title: 'Stored title', displayTitle: 'Stored title: Special Edition'}}, busy: false, destination: {
  selectedVersionId: 'v2', rows: [{id: 'v1', heading: 'Director’s Cut', detail: '4K Dolby Vision, Dolby TrueHD with Atmos 7.1, 61.4 GB'},
    {id: 'v2', heading: '4K Dolby Vision', detail: 'Dolby TrueHD with Atmos 7.1, 58.2 GB'}, {id: 'v3', heading: '1080p', detail: 'Dolby Digital 5.1, 9.8 GB'}],
}};

test('actual phone chooser draws only two-line buttons, stored title and Done; a row selects immediately', () => {
  isTV = false; width = 390; height = 844; const choices: string[] = []; let closed = 0;
  const tree = descendants(render(() => NativeVersionChooser({value, choose: (id: string) => choices.push(id), close: () => {closed++;}}))).find(node => node.type === 'BottomSheet');
  assert.equal(tree.type, 'BottomSheet'); assert.equal(tree.props.title, 'Play a different version'); assert.equal(tree.props.showClose, false);
  const nodes = descendants(tree), rows = nodes.filter(node => /^watch-version-v/.test(node.props.testID ?? ''));
  assert.equal(rows.length, 3); assert.ok(rows.every(node => node.props.accessibilityRole === 'button' && node.props.accessibilityState === undefined));
  assert.ok(rows.every(node => node.props.style.minHeight === 64));
  rows[1].props.onPress(); assert.deepEqual(choices, ['v2']);
  nodes.find(node => node.props.testID === 'watch-versions-done').props.onPress(); assert.equal(closed, 1);
  assert.ok(nodes.some(node => node.type === 'Text' && node.props.children.includes('Stored title: Special Edition')));
  assert.deepEqual(rows.map(node => value.destination.rows[value.destination.rows.findIndex(row => `watch-version-${row.id}` === node.props.testID)]?.heading), ['Director’s Cut', '4K Dolby Vision', '1080p']);
  assert.equal(nodes.some(node => node.type === 'Icon' || node.props.accessibilityRole === 'radio'), false);
});

for (const scale of [1, .5, 2]) test(`actual TV chooser uses one760px panel/one canvas at scale ${scale}, with one focused row`, () => {
  isTV = true; width = 1920 * scale; height = 1080 * scale;
  const tree = render(() => NativeVersionChooser({value, choose() {}, close() {}})), nodes = descendants(tree);
  const canvas = nodes.find(node => node.props.testID === 'watch-versions-tv-canvas'); assert.deepEqual(canvas.props.style.transform, [{scale}]);
  const panel = nodes.find(node => node.props.testID === 'watch-versions-tv'); assert.equal(panel.props.style.width, 760);
  assert.deepEqual(nodes.filter(node => node.type === 'Stop').map(node => node.props.stopColor), ['#132836', '#0d1c27', '#0b1822']);
  const rows = nodes.filter(node => /^watch-version-v/.test(node.props.testID ?? ''));
  assert.equal(rows.filter(node => node.props.hasTVPreferredFocus).length, 1); assert.equal(rows.find(node => node.props.hasTVPreferredFocus).props.testID, 'watch-version-v2');
  assert.equal(nodes.some(node => node.props.testID === 'watch-versions-done'), false);
  assert.equal(nodes.some(node => node.type === 'Surface'), false);
});
