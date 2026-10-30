import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {settingsRowIndex} from '../../../packages/client-core/src/presentation/settings-spec.ts';
import {settingNumber, settingsSummary} from '../src-next/screens/settings-live/server/settings-values.ts';

type Node = {type: any; props: Record<string, any>};
const nodes = (node: any): Node[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.control)] : [];
const words = (node: any): string => Array.isArray(node) ? node.map(words).join('') : node?.props ? words(node.props.children) : typeof node === 'string' ? node : '';
const button = (tree: unknown, label: string) => nodes(tree).find(value => value.type === 'Button' && words(value) === label)!;
const input = (tree: unknown) => nodes(tree).find(value => value.type === 'TextField')!;

async function fixture(error = false) {
  const h = hooks(), pending: (() => void)[] = [], effects = new Set<any>();
  const effect = (setup: () => void | (() => void), deps: readonly unknown[]) => {
    const slot = h.react.useRef(undefined); effects.add(slot);
    if (!slot.current || deps.some((value, index) => value !== slot.current.deps[index])) {
      const old = slot.current; slot.current = {deps, cleanup: undefined}; const owned = slot.current;
      pending.push(() => {old?.cleanup?.(); owned.cleanup = setup();});
    }
  };
  const react = {...h.react, useEffect: effect, useLayoutEffect: effect};
  const base = {apiRequestsPerMinute: 600,'integrations.compatibilityEnabled':false};
  let state: any = {draft: base, saved: base, dirty: false, saving: false, saveError: error ? new Error('save failed') : undefined};
  const compatibilityWrites:boolean[]=[];
  const writes: number[] = [], edits: number[] = []; let loads = 0, discards = 0;
  const form = {
    getSnapshot: () => state,
    set: (change: (draft: typeof base) => typeof base) => {const draft = change(state.draft); edits.push(draft.apiRequestsPerMinute); state = {...state, draft, dirty: draft.apiRequestsPerMinute !== base.apiRequestsPerMinute || draft['integrations.compatibilityEnabled']!==base['integrations.compatibilityEnabled']};},
    load: async () => {loads++;},
  };
  const group = {mount: 1, forms: {runtime: form}, set: {
    getSnapshot: () => state,
    save: async () => {writes.push(state.draft.apiRequestsPerMinute);compatibilityWrites.push(state.draft['integrations.compatibilityEnabled']); return true;},
    discard: () => {discards++; state = {...state, draft: base, dirty: false};},
  }};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/integrations/Advanced.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../../packages/client-core/src/presentation/settings-bindings': {
      settingsBinding: () => ({form: 'runtime', key: 'apiRequestsPerMinute'}),
      readSettingsBinding: (_binding: unknown, sources: any) => ({current: sources.forms.runtime.apiRequestsPerMinute, effective: sources.forms.runtime.apiRequestsPerMinute, default: 600}),
      settingsBindingPatch: (_binding: unknown, value: number) => ({form: 'runtime', values: {apiRequestsPerMinute: value}}),
    },
    '../../../../../../../packages/client-core/src/presentation/settings-spec': {settingsRowIndex},
    '../../../../../../../packages/i18n/src': {defaultI18n: {number: String}},
    '../../../../kit/controls': {Button: 'Button',Switch:'Switch', TextField: 'TextField'}, '../../../../kit/shell': {SettingsRow: 'SettingsRow'},
    '../../../../kit/theme': {useTheme: () => ({colors: {record: '#f00'}})}, '../../../../kit/states': {StateView: 'StateView'},
    '../AdvancedSection': {NativeAdvancedSection: 'NativeAdvancedSection'}, '../settings-values': {settingNumber, settingsSummary},
    '../storage/forms': {useStorageForms: () => group, useFormState: () => state, useStorageSet: () => state},
  });
  const runtime = {text: {t: (id: string) => id === 'next.settings.allDefaults' ? 'All at defaults' : id}, reportError() {}, error: (_error: unknown, retry: () => void) => ({primaryAction: {onPress: retry}})};
  const child = module.NativeIntegrationAdvanced({ports: {}, runtime}) as Node;
  const render = () => {
    let tree = h.render(() => child.type(child.props));
    for (let attempts = 0; pending.length; attempts++) {
      assert.ok(attempts < 5, 'effects settle'); for (const run of pending.splice(0)) run(); tree = h.render(() => child.type(child.props));
    }
    return tree;
  };
  return {render, writes, compatibilityWrites,edits, get loads() {return loads;}, get discards() {return discards;}, unmount: () => {for (const slot of effects) slot.current?.cleanup?.();}};
}

test('Integration Advanced enforces the canonical ten-request step before editing or saving', async () => {
  const f = await fixture(); let tree = f.render(); assert.equal(input(tree).props.value, '600');
  input(tree).props.onChangeText('61'); tree = f.render(); assert.deepEqual(f.edits, []); assert.equal(input(tree).props.error, 'error.invalidInput');
  input(tree).props.onChangeText('620'); tree = f.render(); assert.deepEqual(f.edits, [620]);
  assert.equal(button(tree, 'action.save').props.disabled, false); button(tree, 'action.save').props.onPress(); assert.deepEqual(f.writes, [620]);
  f.unmount();
});

test('retained Save, Discard and error Retry cannot act after newer invalid text', async () => {
  const f = await fixture(true); let tree = f.render();
  input(tree).props.onChangeText('620'); tree = f.render();
  const save = button(tree, 'action.save').props.onPress, discard = button(tree, 'action.discard').props.onPress;
  const retry = nodes(tree).find(value => value.type === 'StateView')!.props.primaryAction.onPress;
  input(tree).props.onChangeText('621'); save(); discard(); retry();
  assert.deepEqual(f.writes, []); assert.equal(f.discards, 0);
  tree = f.render(); assert.equal(input(tree).props.value, '621'); assert.equal(button(tree, 'action.save').props.disabled, true);
  button(tree, 'action.discard').props.onPress(); tree = f.render(); assert.equal(input(tree).props.value, '600'); assert.equal(f.discards, 1);
  f.unmount();
});

test('a retired Advanced opening cannot edit, save, discard or retry its old form', async () => {
  const f = await fixture(true); let tree = f.render(); input(tree).props.onChangeText('620'); tree = f.render();
  const field = input(tree), save = button(tree, 'action.save'), discard = button(tree, 'action.discard');
  const retry = nodes(tree).find(value => value.type === 'StateView')!.props.primaryAction.onPress;
  f.unmount(); field.props.onChangeText('630'); save.props.onPress(); discard.props.onPress(); retry();
  assert.deepEqual(f.edits, [620]); assert.deepEqual(f.writes, []); assert.equal(f.discards, 0); assert.equal(f.loads, 0);
});

test('Integration Advanced shares the canonical default and changed summary', async () => {
  const f = await fixture();
  assert.equal(nodes(f.render()).find(node=>node.type==='NativeAdvancedSection')!.props.summary, 'All at defaults');
  input(f.render()).props.onChangeText('620');
  assert.equal(nodes(f.render()).find(node=>node.type==='NativeAdvancedSection')!.props.summary, 'next.settingsSpec.server.integrations.advanced.groups.1.rows.0.label: 620');
  f.unmount();
});


test('integration compatibility uses the captured native settings form and retired callbacks cannot change it',async()=>{
 const f=await fixture();let tree=f.render();const control=nodes(tree).find(node=>node.type==='Switch')!;
 assert.equal(control.props.checked,false);control.props.onCheckedChange(true);tree=f.render();
 assert.equal(nodes(tree).find(node=>node.type==='Switch')!.props.checked,true);
 button(tree,'action.save').props.onPress();assert.deepEqual(f.compatibilityWrites,[true]);
 f.unmount();control.props.onCheckedChange(false);assert.deepEqual(f.edits,[600]);
});
