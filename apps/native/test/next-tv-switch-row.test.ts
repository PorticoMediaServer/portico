import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Walk, 1 Nov: a focused TV switch alone stopped at the screen's bottom edge with its row cut
// off, and lost focus when disabled while saving. As in Settings on TV, the row is the stop.
test('a TV server switch row is one focusable row that OK flips, drawing an unfocusable switch', async () => {
  const react = await import('react');
  const {TVSwitchRow} = await screenModule(new URL('../src-next/screens/settings-live/server/TVSettingRows.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch'}, '../../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({})},
    '../../../kit/overlays': {Banner: 'Banner', Menu: 'Menu', SidePanel: 'SidePanel'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'},
    '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => ({})}, '../../search/TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'},
    './settings-values': {settingNumber: () => 0},
  });
  const changes: boolean[] = [];
  const row = TVSwitchRow({testID: 'metadata.titleDataset', label: 'Download title data', checked: true, onChange: (value: boolean) => changes.push(value)}) as {type: unknown; props: any};
  assert.equal(row.type, 'SettingsRow');
  assert.equal(row.props.testID, 'metadata.titleDataset');
  assert.equal(row.props.control, undefined, 'the switch is not the stop');
  assert.equal(row.props.toggle.checked, true);
  assert.equal(row.props.toggle.visual.props.focusable, false);
  row.props.onPress();
  assert.deepEqual(changes, [false]);
  const blocked = TVSwitchRow({testID: 'x', label: 'x', checked: false, disabled: true, onChange: () => changes.push(true)}) as {props: any};
  assert.equal(blocked.props.onPress, undefined);
  assert.equal(blocked.props.readable, true, 'a disabled row stays a readable stop');
});
