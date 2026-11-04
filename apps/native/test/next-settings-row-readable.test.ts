import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const {react} = hooks();
let television = true;
// The fake React's useContext reads `value`: each targeting test sets it and puts it back.
const targeting: {value: any} = {value: {}};
const {SettingsRow} = await screenModule(new URL('../src-next/kit/shell/SettingsControls.tsx', import.meta.url), {
  react, 'react-native': {StyleSheet: {absoluteFill: {}}, Text: 'Text', View: 'View'},
  '../../../../../packages/i18n/src/index': {defaultI18n: {t: (id: string) => id}},
  '../controls/Focusable': {Focusable: 'Focusable'}, '../controls/Select': {Select: 'Select'}, '../overlays/Menu': {Menu: 'Menu'},
  '../artwork/Artwork': {Artwork: 'Artwork'}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => ({isTV: television, colors: new Proxy({}, {get: () => '#000'}), type: {}, textScale: 1})},
  '../focus/registry': {focusTestID: (a: string, b: string) => `${a}.${b}`}, './HighlightedText': {HighlightedText: 'HighlightedText'},
  './visual': {alpha: () => '#000', shellType: () => ({})},
  './SettingsRowTarget': {SettingsRowTargetContext: targeting, SettingsRowFlash: 'SettingsRowFlash'},
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

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children)] : [];
const targeted = <T,>(value: any, run: () => T): T => {targeting.value = value; try {return run();} finally {targeting.value = {};}};

test('tv-more 34: under Settings search, OK on a TV switch, choice or readable row opens its page and does not act on the row', () => {
  const opened: string[] = []; let toggled = 0;
  targeted({open: (id: string) => opened.push(id)}, () => {
    const toggle = SettingsRow({testID: 'subtitles', label: 'Subtitles', toggle: {checked: true, visual: 'switch'}, onPress: () => {toggled++;}});
    assert.equal(toggle.type, 'Focusable'); assert.equal(toggle.props.accessibilityRole, 'button'); assert.equal(toggle.props.accessibilityState, undefined);
    toggle.props.onPress();
    const readable = SettingsRow({testID: 'version', label: 'App version', value: '1.0'});
    assert.equal(readable.props.accessibilityRole, 'button'); readable.props.onPress();
    const choice = SettingsRow({testID: 'size', label: 'Size', value: 'Medium', onPress: () => {toggled++;}});
    choice.props.onPress();
    assert.ok(!nodes(readable).some(node => node.type === 'Icon'), 'a readable row draws no choice arrows for opening');
  });
  assert.deepEqual(opened, ['subtitles', 'version', 'size']); assert.equal(toggled, 0);
});

test('a slotted control and a row with no id keep their own press under Settings search', () => {
  targeted({open: () => assert.fail('opened')}, () => {
    assert.equal(SettingsRow({testID: 'c', label: 'Choice', value: 'x', control: 'control'}).type, 'View');
    let pressed = 0; SettingsRow({label: 'Plain', onPress: () => {pressed++;}}).props.onPress(); assert.equal(pressed, 1);
  });
});

test('tv-more 34: the row a match opened is drawn highlighted, and no other row is', () => {
  const bar = (row: any) => nodes(row).some(node => node.type === 'View' && node.props?.style?.width === 3);
  targeted({highlight: 'size'}, () => {
    assert.ok(bar(SettingsRow({testID: 'size', label: 'Size', value: 'Medium', onPress() {}})));
    assert.ok(!bar(SettingsRow({testID: 'background', label: 'Background', value: 'Translucent', onPress() {}})));
  });
});

test('settings.html: on phones and tablets the row a result opened carries the one-time flash for that opening', () => {
  television = false;
  try {
    const reveal = () => {};
    targeted({flash: {rowId: 'size', serial: 4, reveal}}, () => {
      const flash = nodes(SettingsRow({testID: 'size', label: 'Size', control: 'select'})).find(node => node.type === 'SettingsRowFlash');
      assert.equal(flash?.props.serial, 4); assert.equal(flash?.props.reveal, reveal);
      assert.ok(!nodes(SettingsRow({testID: 'other', label: 'Other', control: 'select'})).some(node => node.type === 'SettingsRowFlash'));
    });
  } finally {television = true;}
});
