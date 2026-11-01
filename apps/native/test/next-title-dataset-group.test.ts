import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {settingsRowIndex} from '../../../packages/client-core/src/presentation/settings-spec.ts';

// Settings › Metadata › Recommendations on phone and TV (B2 R6).
type Node = {type: any; props: Record<string, any>};
const nodes = (node: any): Node[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.trailing)] : [];
const byId = (node: unknown, id: string) => nodes(node).find(value => value.props.testID === id);
const t = (id: string) => id;

async function fixture(on: boolean | undefined, tv = false, saving = false) {
  const outer = hooks(), inner = hooks(); let active = outer;
  const react = {...outer.react, useState: (...args: unknown[]) => active.react.useState(...args)};
  let draft: Record<string, unknown> = {name: 'Server', ...(on === undefined ? {} : {'recommendations.titleDataset': on})};
  const saves: Record<string, unknown>[] = [];
  const runtimeForm = {getSnapshot: () => ({saving: false}), set: (change: (value: Record<string, unknown>) => Record<string, unknown>) => {draft = change(draft);}};
  const group = {mount: 1, forms: {runtime: runtimeForm}, set: {save: async () => {saves.push(draft);}}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/TitleDatasetGroup.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsRowIndex},
    '../../../kit/controls/Switch': {Switch: 'Switch'}, '../../../kit/shell/visual': {shellType: () => ({})},
    '../../../kit/theme': {useTheme: () => ({colors: {textTertiary: '#999', raised: '#222'}})},
    '../owner-libraries/controls': {LibraryGroup: 'LibraryGroup', LibraryRow: 'LibraryRow', SectionTitle: 'SectionTitle'},
    './storage/forms': {useStorageForms: () => group, useFormState: () => ({draft}), useStorageSet: () => ({saving})},
    './TVSettingRows': {TVSwitchRow: 'TVSwitchRow'},
  });
  const props = {ports: {}, runtime: {text: {t}, reportError() {}}, tv};
  const element = outer.render(() => module.NativeTitleDatasetGroup(props)) as Node;
  return {saves, render: () => {active = inner; return inner.render(() => element.type(element.props));}};
}

test('the switch shows the owner setting, on when never saved, and saves a change at once', async () => {
  const row = settingsRowIndex.get('metadata.titleDataset')!.row;
  for (const [stored, shown] of [[undefined, true], [true, true], [false, false]] as const) {
    const f = await fixture(stored), tree = f.render();
    const control = byId(tree, 'metadata.titleDataset')!;
    assert.equal(control.type, 'Switch');
    assert.equal(control.props.checked, shown, `stored ${stored}`);
    assert.ok(nodes(tree).some(value => value.type === 'SectionTitle'), 'the Recommendations heading');
    assert.ok(nodes(tree).some(value => value.type === 'LibraryRow' && value.props.label === row.label && value.props.help === row.help));
  }
  const f = await fixture(true);
  byId(f.render(), 'metadata.titleDataset')!.props.onCheckedChange(false);
  await Promise.resolve();
  assert.equal(f.saves.length, 1);
  assert.equal(f.saves[0]!['recommendations.titleDataset'], false);
  assert.equal(f.saves[0]!.name, 'Server', 'the rest of the document is kept');
});

test('on TV the switch is the panel\'s focusable switch row', async () => {
  const f = await fixture(true, true), tree = f.render();
  const control = byId(tree, 'metadata.titleDataset')!;
  assert.equal(control.type, 'TVSwitchRow');
  assert.equal(control.props.checked, true);
  control.props.onChange(false);
  await Promise.resolve();
  assert.equal(f.saves[0]!['recommendations.titleDataset'], false);
});

test('on TV a save in progress keeps the switch focusable and ignores presses', async () => {
  // A disabled Android TV row drops focus to the next control (walk, 1 Nov).
  const f = await fixture(true, true, true), control = byId(f.render(), 'metadata.titleDataset')!;
  assert.ok(!control.props.disabled, 'never disabled');
  control.props.onChange(false);
  await Promise.resolve();
  assert.equal(f.saves.length, 0);
});
