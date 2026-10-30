import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const {react} = hooks();
let television = true;
const {SettingsRow} = await screenModule(new URL('../src-next/kit/shell/SettingsControls.tsx', import.meta.url), {
  react, 'react-native': {StyleSheet: {absoluteFill: {}}, Text: 'Text', View: 'View'},
  '../../../../../packages/i18n/src/index': {defaultI18n: {t: (id: string) => id}},
  '../controls/Focusable': {Focusable: 'Focusable'}, '../controls/Select': {Select: 'Select'}, '../overlays/Menu': {Menu: 'Menu'},
  '../artwork/Artwork': {Artwork: 'Artwork'}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => ({isTV: television, colors: new Proxy({}, {get: () => '#000'}), type: {}, textScale: 1})},
  '../focus/registry': {focusTestID: (a: string, b: string) => `${a}.${b}`}, './HighlightedText': {HighlightedText: 'HighlightedText'},
  './visual': {alpha: () => '#000', shellType: () => ({})},
});

test('a TV value-only row is a readable focus stop whose OK does nothing', () => {
  const row = SettingsRow({label: 'App version', value: '1.0', testID: 'v'});
  assert.equal(row.type, 'Focusable');
  assert.equal(row.props.accessibilityRole, 'text');
  assert.equal(row.props.onPress, undefined);
});

test('a TV row with no value, action or control stays plain', () => {
  assert.equal(SettingsRow({label: 'Heading'}).type, 'View');
});

test('a slotted control keeps its own focus target on TV', () => {
  assert.equal(SettingsRow({label: 'Choice', value: 'x', control: 'control'}).type, 'View');
});

test('a TV switch row is one row-wide stop that toggles, with the switch drawn only', () => {
  let pressed = 0;
  const row = SettingsRow({label: 'Stereo', toggle: {checked: true, visual: 'switch'}, onPress: () => {pressed++;}});
  assert.equal(row.type, 'Focusable');
  assert.equal(row.props.accessibilityRole, 'switch');
  assert.deepEqual(row.props.accessibilityState, {checked: true});
  row.props.onPress();
  assert.equal(pressed, 1);
});

test('phone and tablet rows are unchanged: value-only rows are not stops', () => {
  television = false;
  try {assert.equal(SettingsRow({label: 'App version', value: '1.0'}).type, 'View');}
  finally {television = true;}
});
