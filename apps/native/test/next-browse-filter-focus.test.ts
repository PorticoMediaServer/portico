// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import * as design from '../../../packages/design/src/index.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {createTheme, fontFamilies} from '../src-next/kit/theme.ts';
import * as controlModel from '../src-next/kit/controls/control-model.ts';
import {useControlValue} from '../src-next/kit/controls/useControlValue.ts';
import * as presentation from '../src-next/kit/browse/presentation.ts';
import * as registryModule from '../src-next/kit/focus/registry.ts';
import {useHostRef} from '../src-next/kit/focus/useHostRef.ts';
import {fabricHost} from './helpers/fabric-host.mjs';
import {liveOverlayPaint, screenModule} from './helpers/screen-harness.mjs';

type Node = {type: string; props: Record<string, any>; children: (Node | string | number)[]};
const text = (node: Node | string | number): string => typeof node === 'object' ? node.children.map(text).join('') : String(node);

// Actual FilterBuilder, controls, FocusRegion, useFocusTarget and registry run.
// Only native hosts/animation/blur/theme are replaced; no D-pad/layout claim.
async function fixture(isTV: boolean) {
  const theme = createTheme({isTV, width: isTV ? 1920 : 390});
  const react = {...React, default: React}, native = {View: 'View', Text: 'Text', ScrollView: 'ScrollView', Pressable: React.forwardRef(({children, ...props}: any, ref: any) => React.createElement('Pressable', {...props, ref}, typeof children === 'function' ? children({pressed: false, hovered: false}) : children)), TextInput: 'TextInput', TVFocusGuideView: 'TVFocusGuideView',
    ActivityIndicator: 'ActivityIndicator', Platform: {OS: 'android', isTV}, findNodeHandle: (host: any) => host?.tag ?? null,
    StyleSheet: {create: (value: any) => value, flatten: (value: any): any => Array.isArray(value) ? Object.assign({}, ...value.map(native.StyleSheet.flatten)) : value ?? {}, absoluteFill: {position: 'absolute', inset: 0}}};
  const themeModule = {useTheme: () => theme, fontFamilies};
  const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react, 'react-native': native, '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': themeModule, './registry': registryModule, './useHostRef': {useHostRef},
  });
  const focusModule = {...registryModule, ...focus};
  const reanimated = {default: {createAnimatedComponent: (value: any) => value, View: 'AnimatedView'},
    useSharedValue: (value: any) => React.useRef({value}).current, useAnimatedStyle: (run: any) => run(), withTiming: (value: any) => value};
  const {Focusable} = await screenModule(new URL('../src-next/kit/controls/Focusable.tsx', import.meta.url), {
    '../overlays/OverlayPaintContext': liveOverlayPaint,
    react, 'react-native': native, 'react-native-reanimated': reanimated, '@portico/design': design, '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': themeModule,
    '../useMotion': {useMotion: () => ({transforms: false, timing: () => ({duration: 0})})}, './control-model': controlModel, './useFocusedMenu': {useFocusedMenu() {}}, '../focus': focusModule,
  });
  const shared = {'../overlays/OverlayPaintContext': liveOverlayPaint, react, 'react-native': native, '@portico/design': design, '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': themeModule, '../focus': focusModule,
    '../Icon': {Icon: 'Icon'}, './Focusable': {Focusable}, './control-model': controlModel, './useControlValue': {useControlValue}};
  const {Chip} = await screenModule(new URL('../src-next/kit/controls/Chip.tsx', import.meta.url), shared);
  const {Segmented} = await screenModule(new URL('../src-next/kit/controls/Tabs.tsx', import.meta.url), shared);
  const {TextField} = await screenModule(new URL('../src-next/kit/controls/TextField.tsx', import.meta.url), {...shared, '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({})}});
  const {Button} = await screenModule(new URL('../src-next/kit/controls/Button.tsx', import.meta.url), {...shared, '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({})}});
  const {FilterBuilder} = await screenModule(new URL('../src-next/kit/browse/Filters.tsx', import.meta.url), {
    '@react-native-masked-view/masked-view': {default: 'MaskedView'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    react, 'react-native': native, '../../../../../packages/i18n/src': {defaultI18n}, '../controls': {Button, Chip, Segmented, TextField, Focusable},
    '../Icon': {Icon: 'Icon'}, '../overlays': {Popover: 'Popover'}, '../focus': focusModule, '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': themeModule, './presentation': presentation,
  });
  return {FilterBuilder, ...focusModule};
}

async function mountTree(root: React.ReactNode) {
  // React owns the complete provider/component tree and native ref attachment.
  // Only the commands on the native public-instance boundary are controlled.
  const host = await fabricHost({publicInstance: (tag: number) => ({tag, requestTVFocus() {}, focus() {}, setDestinations() {}})});
  try {await host.render(root);} catch (error) {try {await host.unmount();} catch {} throw error;}
  const native: Node[] = [];
  const collect = (node: any): Node | string => {
    assert.equal(typeof node.type, 'string', 'the test must resolve every actual component');
    if (node.type === 'RCTRawText') return node.props.text;
    const record: Node = {type: node.type, props: node.props, children: node.children.map(collect)};
    native.push(record);
    return record;
  };
  host.root.forEach(collect);
  return {nodes: native, controls: native.filter(node => node.type === 'Pressable' || node.type === 'TextInput'), dispose: () => host.unmount()};
}

function props(prefix?: string, reverse = false) {
  const calls: any[] = [];
  const values = [{value: 'same/value', label: 'Shared value', count: 4}, {value: 'another.value', label: 'Another value', count: 2}];
  const fields: Record<string, any> = {
    genre: {kind: 'values', included: [], excluded: [], mode: 'any', allowAll: true, showMatchMode: true, search: '', showAllLabel: 'All genres', values: reverse ? [...values].reverse() : values},
    tag: {kind: 'values', included: [], excluded: [], mode: 'any', allowAll: false, values: [values[0]]},
    year: {kind: 'range', value: null, presets: [{value: '1980', label: '1980s'}, {value: '1990', label: '1990s'}]},
    rating: {kind: 'choice', value: null, options: [{value: 'same/value', label: 'Same choice'}, {value: 'disabled', label: 'Unavailable choice', disabled: true}]},
    favorite: {kind: 'toggle', value: null}, query: {kind: 'text', value: ''},
    failed: {kind: 'values', included: [], excluded: [], mode: 'any', allowAll: false, status: 'error', values: []},
  };
  const ids = Object.keys(fields);
  return {calls, value: {focusPrefix: prefix, labels: {field: (field: any) => field.id},
    model: {groups: [{id: 'title', fields: (reverse ? ids.reverse() : ids).map(id => ({id}))}]}, fields, openFields: ids,
    more: {title: {fields: ['tag'], summary: 'Tag', open: true, onOpenChange: (value: boolean) => calls.push(['more', value])}},
    onFieldOpenChange: (...args: any[]) => calls.push(['open', ...args]), onFieldChange: (...args: any[]) => calls.push(['change', ...args]),
    onSearchChange: (...args: any[]) => calls.push(['search', ...args]), onShowAll: (id: string) => calls.push(['all', id]), onRetry: (id: string) => calls.push(['retry', id])}};
}

for (const isTV of [true, false]) test(`${isTV ? 'TV' : 'phone'} real FilterBuilder controls register distinct semantic IDs under a real FocusRegion`, async () => {
  const mod = await fixture(isTV), registry = registryModule.createFocusRegistry(), prefix = 'actual-authority/library-id/filters';
  const data = props(prefix), tree = await mountTree(React.createElement(mod.FocusProvider, {registry}, React.createElement(mod.FocusRegion, {id: prefix}, React.createElement(mod.FilterBuilder, data.value))));
  let ids: string[];
  try {
    assert.ok(tree.controls.length > 16, 'all field control kinds and nested rare fields must mount');
    ids = tree.controls.map(node => node.props.testID);
    assert.ok(ids.every(id => typeof id === 'string' && id.length > 0)); assert.equal(new Set(ids).size, ids.length);
    assert.equal(registry.order(prefix).length, tree.controls.filter(node => !node.props.disabled).length, 'every enabled actual native host registers exactly once');
    for (const id of registry.order(prefix)) assert.ok(registry.child({regionId: prefix, childId: id}));
    const facet = tree.controls.find(node => node.props.accessibilityRole === 'checkbox' && text(node).startsWith('Shared value'))!;
    facet.props.onPress({defaultPrevented: false});
    assert.deepEqual(data.calls.at(-1), ['change', 'genre', {kind: 'values', mode: 'any', included: ['same/value'], excluded: []}]);
    const yes = tree.controls.find(node => node.props.accessibilityRole === 'radio' && text(node) === 'Yes')!;
    yes.props.onPress({defaultPrevented: false}); assert.deepEqual(data.calls.at(-1), ['change', 'favorite', {kind: 'toggle', value: true}]);
    tree.controls.find(node => text(node) === 'Try again')!.props.onPress({defaultPrevented: false}); assert.deepEqual(data.calls.at(-1), ['retry', 'failed']);
    tree.controls.find(node => text(node) === 'All genres')!.props.onPress({defaultPrevented: false}); assert.deepEqual(data.calls.at(-1), ['all', 'genre']);
    const input = tree.controls.find(node => node.type === 'TextInput' && node.props.accessibilityLabel === 'query')!;
    input.props.onChangeText('real field value'); assert.deepEqual(data.calls.at(-1), ['change', 'query', {kind: 'text', value: 'real field value'}]);
  } finally {await tree.dispose();}
  assert.deepEqual(registry.order(prefix), [], 'actual host/region cleanup removes registrations');
  const reordered = await mountTree(React.createElement(mod.FocusProvider, {registry}, React.createElement(mod.FocusRegion, {id: prefix}, React.createElement(mod.FilterBuilder, props(prefix, true).value))));
  try {assert.deepEqual(reordered.controls.map(node => node.props.testID).sort(), ids!.sort(), 'field/facet order changes cannot change the semantic IDs');}
  finally {await reordered.dispose();}
});

test('omitting the optional prefix preserves the existing unfocused control geometry and callbacks', async () => {
  const mod = await fixture(false), unscoped = props(), scoped = props('actual-library/filters');
  const before = await mountTree(React.createElement(mod.FilterBuilder, unscoped.value)), after = await mountTree(React.createElement(mod.FilterBuilder, scoped.value));
  const project = (node: Node) => ({type: node.type, text: text(node), disabled: node.props.disabled, role: node.props.accessibilityRole, state: node.props.accessibilityState,
    style: typeof node.props.style === 'function' ? node.props.style({pressed: false, hovered: false}) : node.props.style});
  try {
    assert.ok(before.controls.every(node => node.props.testID === undefined));
    assert.deepEqual(after.controls.map(project), before.controls.map(project), 'the additive API changes identity only');
    before.controls.find(node => node.props.accessibilityRole === 'checkbox' && text(node).startsWith('Shared value'))!.props.onPress({defaultPrevented: false});
    assert.deepEqual(unscoped.calls.at(-1), ['change', 'genre', {kind: 'values', mode: 'any', included: ['same/value'], excluded: []}]);
  } finally {await before.dispose(); await after.dispose();}
});

test('retained selected chips keep their native focus hosts while loading and after refresh errors', async () => {
  const mod=await fixture(true),registry=registryModule.createFocusRegistry(),prefix='retained/library/filters',data=props(prefix);
  const host=await fabricHost({publicInstance:(tag:number)=>({tag,requestTVFocus(){},focus(){},setDestinations(){}})});
  const nodes=():Node[]=>{const result:Node[]=[];const collect=(node:any):Node|string=>{if(node.type==='RCTRawText')return node.props.text;const value:Node={type:node.type,props:node.props,children:node.children.map(collect)};result.push(value);return value;};host.root.forEach(collect);return result;};
  const render=async(status?:'loading'|'error')=>host.render(React.createElement(mod.FocusProvider,{registry},React.createElement(mod.FocusRegion,{id:prefix},React.createElement(mod.FilterBuilder,{...data.value,fields:{...data.value.fields,genre:{...data.value.fields.genre,included:['same/value'],status,statusLabel:status==='loading'?'Loading values':status==='error'?'Refresh failed':undefined}}}))));
  try{
    await render();
    const original=nodes().find(node=>node.type==='Pressable'&&node.props.accessibilityRole==='checkbox'&&text(node).startsWith('Shared value'))!;
    const childId=registry.order(prefix).find(id=>{const [owner,...identity]=JSON.parse(id);return registryModule.focusTestID(owner,JSON.stringify(identity))===original.props.testID;})!;
    assert.ok(childId);const address={regionId:prefix,childId},originalHost=registry.child(address);
    assert.ok(originalHost,'selected chip has no registered native focus host');
    for(const status of ['loading','error'] as const){
      await render(status);
      const tree=nodes(),chip=tree.find(node=>node.props.testID===original.props.testID)!;
      assert.ok(chip,'refresh removed a retained chip');assert.equal(chip.props.accessibilityState.checked,true);
      assert.equal(registry.child(address),originalHost,'refresh remounted the native focus host');
      assert.ok(tree.some(node=>text(node).includes(status==='loading'?'Loading values':'Refresh failed')));
      if(status==='error')assert.ok(tree.some(node=>text(node)==='Try again'));
    }
    nodes().find(node=>node.props.testID===original.props.testID)!.props.onPress({defaultPrevented:false});
    assert.deepEqual(data.calls.at(-1),['change','genre',{kind:'values',mode:'any',included:[],excluded:[]}]);
  }finally{await host.unmount();}
});

test('H10: on large screens an open field opens in a popover anchored to its tile, and the grid keeps its two columns', async () => {
  const mod = await fixture(false), data = props();
  const tree = await mountTree(React.createElement(mod.FilterBuilder, {...data.value, platform: 'desktop', openFields: ['genre']}));
  try {
    const popovers = tree.nodes.filter(node => node.type === 'Popover');
    const open = popovers.filter(node => node.props.open);
    assert.equal(open.length, 1, 'exactly the open field has an open popover');
    assert.equal(open[0].props.title, 'genre');
    assert.ok(text(open[0]).includes('Shared value'), 'the field options live in the popover');
    const tiles = tree.nodes.filter(node => node.type === 'View' && node.props.style?.width === '48.8%' && node.children.some(child => typeof child === 'object' && child.type === 'Popover'));
    assert.ok(tiles.length >= 6, 'every desktop field, open or not, keeps its half-width tile in the grid');
    assert.ok(!tree.nodes.some(node => node.type === 'View' && node.props.style?.width === '100%' && text(node).startsWith('genre')), 'no field expands inline to full width');
  } finally {await tree.dispose();}
});
