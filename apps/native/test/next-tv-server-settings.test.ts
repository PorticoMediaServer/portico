import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {settingNumber} from '../src-next/screens/settings-live/server/settings-values';
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.footer)] : [];

/** Two components render through one module, so each gets its own hook slots. */
async function entryRow() {
  const row = hooks(), sheet = hooks(), saved: unknown[] = [];
  let current = row;
  const react: any = new Proxy({}, {get: (_, key: string) => key === 'default' ? react : key === 'useId' ? () => 'id' : key === 'Fragment' ? 'Fragment' : (...args: unknown[]) => {const hook = (current.react as any)[key]; if (typeof hook !== 'function') throw new Error(`no hook ${key}`); return hook(...args);}});
  const host = {render: (fn: () => any) => {current = row; return row.render(fn);}, sheet: (fn: () => any) => {current = sheet; return sheet.render(fn);}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/TVSettingRows.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch'},
    '../../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({focus() {}})},
    '../../../kit/overlays': {Banner: 'Banner', Menu: 'Menu', SidePanel: 'SidePanel'},
    '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/shell/visual': {shellType: () => ({})},
    '../../../kit/theme': {useTheme: () => ({colors: {text: 'text', textSecondary: 'secondary'}})},
    '../../search/TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'}, './settings-values': {settingNumber},
  });
  return {host, module, saved};
}

test('a secret row shows only whether a value is kept, and its sheet starts empty and masks what is typed', async () => {
  const {host, module, saved} = await entryRow();
  const t = (id: string, values?: {count?: number}) => values?.count === undefined ? id : `${id}:${values.count}`;
  const render = () => host.render(() => module.TVEntryRow({testID: 'metadata.tmdb', label: 'TMDB', kind: 'secret', configured: true, t, onSave: (value: unknown) => saved.push(value)}));
  let tree = render();
  const row = nodes(tree).find(node => node.type === 'SettingsRow');
  assert.equal(row.props.value, 'next.tvServer.secretSet');
  row.props.onPress(); tree = render();
  const sheet = nodes(tree).find(node => typeof node.type === 'function');
  const panel = () => host.sheet(() => sheet.type(sheet.props));
  let drawn = panel();
  const keyboard = () => nodes(drawn).find(node => node.type === 'NativeTVKeyboard');
  assert.equal(keyboard().props.value, '');
  keyboard().props.onChangeText('s3cret'); drawn = panel();
  const text = nodes(drawn).filter(node => node.type === 'Text').flatMap(node => node.props.children).join('');
  assert.ok(text.includes('••••••'));
  assert.equal(text.includes('s3cret'), false);
  assert.equal(JSON.stringify(nodes(drawn).map(node => node.props.accessibilityLabel)).includes('s3cret'), false);
  const save = nodes(drawn).find(node => node.type === 'Button' && node.props.focusId === 'save');
  assert.deepEqual(save.props.children, ['next.tvServer.replace']);
  save.props.onPress();
  assert.deepEqual(saved, ['s3cret']);
});

test('a number sheet steps within the row’s range and refuses a value off its step', async () => {
  const {host, module, saved} = await entryRow();
  const control = {type: 'number', min: 5, max: 3600, step: 5} as const;
  const sheet = () => {
    const tree = host.render(() => module.TVEntryRow({testID: 'live.retry', label: 'Retry', kind: 'number', value: '60', control, t: (id: string) => id, onSave: (value: unknown) => saved.push(value)}));
    nodes(tree).find(node => node.type === 'SettingsRow').props.onPress();
    const element = nodes(host.render(() => module.TVEntryRow({testID: 'live.retry', label: 'Retry', kind: 'number', value: '60', control, t: (id: string) => id, onSave: (value: unknown) => saved.push(value)}))).find(node => typeof node.type === 'function');
    return () => host.sheet(() => element.type(element.props));
  };
  const panel = sheet();
  let drawn = panel();
  const button = (id: string) => nodes(drawn).find(node => node.type === 'Button' && node.props.focusId === id);
  button('increase').props.onPress(); drawn = panel();
  assert.equal(nodes(drawn).find(node => node.type === 'NativeTVKeyboard').props.value, '65');
  nodes(drawn).find(node => node.type === 'NativeTVKeyboard').props.onChangeText('62'); drawn = panel();
  assert.equal(button('save').props.disabled, true);
  assert.ok(nodes(drawn).some(node => node.type === 'Banner'));
  nodes(drawn).find(node => node.type === 'NativeTVKeyboard').props.onChangeText('120'); drawn = panel();
  button('save').props.onPress();
  assert.deepEqual(saved, [120]);
});
