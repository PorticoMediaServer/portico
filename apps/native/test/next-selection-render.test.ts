// @fabric-react
import * as cardModel from '../src-next/kit/cards/model.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {registerHooks} from 'node:module';
import React, {type ReactElement, type ReactNode} from 'react';
import {transformSync} from 'rolldown/utils';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {createTheme, fontFamilies, nativeType} from '../src-next/kit/theme.ts';
import type {BulkEditorProps, DeleteMediaDialogProps, SelectionBarProps} from '../src-next/kit/selection/types.ts';

// Exercise the actual family, controls and overlay composition. Only native
// hosts, animation/gesture drivers and the theme context are replaced. This
// element/hook harness cannot establish native layout or physical D-pad focus.
const keyboardListeners = new Map<string, (event?: any) => void>();
Object.assign(globalThis, {__porticoSelectionKeyboard: keyboardListeners, __porticoKeyboardControllerListeners: keyboardListeners});
const state = {viewport: {width: 1920, height: 1080, scale: 1, fontScale: 1}, theme: createTheme({isTV: true, width: 1920}), fontFamilies, nativeType, menuKeyEnabled: false};
const stateKey = '__porticoSelectionRenderTest';
Object.assign(globalThis, {[stateKey]: state});
const moduleURL = (source: string) => `data:text/javascript,${encodeURIComponent(source)}`;
const nativeStub = new URL('./react-native-stub.mjs', import.meta.url).href;
const native = moduleURL(`export * from ${JSON.stringify(nativeStub)};
  export const Platform={OS:'android',isTV:true};
  export const View='View',Text='Text',Pressable='Pressable',Modal='Modal',ScrollView='ScrollView',FlatList='FlatList',TVFocusGuideView='TVFocusGuideView',ActivityIndicator='ActivityIndicator';
  export const Image='Image', ImageBackground='ImageBackground', PixelRatio={get:()=>1,getFontScale:()=>1,roundToNearestPixel:n=>n};
  export const Animated={View:'NativeAnimatedView',createAnimatedComponent:c=>c,Value:class{constructor(value){this.value=value}}};
  export const Easing={bezier:()=>v=>v,linear:v=>v,out:v=>v,in:v=>v,inOut:v=>v};
  export const TextInput=Object.assign(()=>null,{State:{currentlyFocusedInput:()=>globalThis.__porticoSelectionFocusedInput}});
  export const LayoutAnimation={configureNext(){},Types:{keyboard:"keyboard"}};
  export const Keyboard={addListener:(name,callback)=>{const m=globalThis.__porticoSelectionKeyboard;m.set(name,callback);return {remove(){m.delete(name);}};}};
  export const BackHandler={addEventListener:()=>({remove(){}})};
  export const TVEventControl={enableTVMenuKey(){globalThis.__porticoSelectionRenderTest.menuKeyEnabled=true;},disableTVMenuKey(){globalThis.__porticoSelectionRenderTest.menuKeyEnabled=false;}};
  export const useWindowDimensions=()=>globalThis.__porticoSelectionRenderTest.viewport;
  const flatten=s=>Array.isArray(s)?Object.assign({},...s.map(flatten)):s??{};
  export const StyleSheet={create:s=>s,flatten,absoluteFill:{position:'absolute',inset:0}};`);
const theme = moduleURL(`const s=globalThis.${stateKey}; export const activeTextScale=()=>s.theme.textScale; export const useTheme=()=>s.theme; export const fontFamilies=s.fontFamilies; export const nativeType=s.nativeType;`);
const reanimated = moduleURL(`import {useRef} from ${JSON.stringify(import.meta.resolve('react'))};
  export const useSharedValue=value=>useRef({value}).current,useAnimatedStyle=fn=>fn(),cancelAnimation=()=>{},withTiming=value=>value,withSpring=value=>value,withDelay=(_,value)=>value,runOnJS=fn=>fn,runOnUI=fn=>fn,useAnimatedRef=()=>useRef(null),useAnimatedScrollHandler=()=>{},useAnimatedReaction=()=>{},scrollTo=()=>{};
  export const Easing={bezier:()=>v=>v,linear:v=>v},ReduceMotion={Never:'never'};
  export default {View:'AnimatedView',ScrollView:'AnimatedScrollView',createAnimatedComponent:c=>c};`);
const gesture = moduleURL(`const pan=()=>new Proxy({}, {get:()=>()=>pan()}); export const Gesture={Pan:pan}; export const GestureDetector='GestureDetector';`);
const svg = moduleURL(`export default 'Svg'; export const Defs='Defs',LinearGradient='LinearGradient',RadialGradient='RadialGradient',Rect='Rect',Stop='Stop',Path='Path',Circle='Circle',Ellipse='Ellipse',Line='Line',Polyline='Polyline',Polygon='Polygon',G='G',ClipPath='ClipPath',Mask='Mask',Use='Use',Text='SvgText';`);
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === 'react-native') return {url: native, shortCircuit: true};
    if (specifier === 'react-native-reanimated') return {url: reanimated, shortCircuit: true};
    if (specifier === 'react-native-gesture-handler') return {url: gesture, shortCircuit: true};
    if (specifier === '@react-native-masked-view/masked-view') return {url: moduleURL(`export default 'MaskedView';`), shortCircuit: true};
    if (specifier === 'react-native-svg') return {url: svg, shortCircuit: true};
    if (specifier === '@react-native-community/blur') return {url: moduleURL(`export const BlurView='BlurView';`), shortCircuit: true};
    if (specifier === 'react-native-safe-area-context') return {url: moduleURL(`export const useSafeAreaInsets=()=>({top:0,bottom:0,left:0,right:0});`), shortCircuit: true};
    if (specifier.startsWith('.') && context.parentURL?.startsWith('file:') && /\/kit\/theme(?:\.ts)?$/.test(new URL(specifier, context.parentURL).pathname)) return {url: theme, shortCircuit: true};
    try {return next(specifier, context);} catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ERR_MODULE_NOT_FOUND' || !specifier.startsWith('.')) throw error;
      for (const suffix of ['.ts', '.tsx', '/index.ts', '/index.tsx']) {
        try {return next(specifier + suffix, context);} catch (retry) {if ((retry as NodeJS.ErrnoException).code !== 'ERR_MODULE_NOT_FOUND') throw retry;}
      }
      throw error;
    }
  },
  load(url, context, next) {
    if (!url.endsWith('.tsx')) return next(url, context);
    const filename = fileURLToPath(url);
    const result = transformSync(filename, readFileSync(filename, 'utf8'), {lang: 'tsx', jsx: {runtime: 'automatic'}, target: 'es2022'});
    if (result.errors.length) throw new AggregateError(result.errors, `Cannot transform ${filename}`);
    return {format: 'module', source: result.code, shortCircuit: true};
  },
});
const NativeInput = (await import('react-native')).TextInput;
const nativePlatform = (await import('react-native')).Platform as {OS: 'android' | 'ios'};
const {selectionControlID: controlID} = await import('../src-next/kit/selection/focus-ids.ts');
const {PlaylistPicker} = await import('../src-next/kit/selection/Picker.tsx');
const {BulkEditor} = await import('../src-next/kit/selection/BulkEditor.tsx');
const {DeleteMediaDialog} = await import('../src-next/kit/selection/DeleteMediaDialog.tsx');
const {MixedEditExplainer} = await import('../src-next/kit/selection/MixedEditExplainer.tsx');
const {SelectionBar, SelectionPhoneActionBar} = await import('../src-next/kit/selection/SelectionBar.tsx');
const {Icon} = await import('../src-next/kit/Icon.tsx');
const {SelectionActionRow} = await import('../src-next/kit/selection/SelectionActionRow.tsx');
const {StateLayout} = await import('../src-next/kit/states/StateLayout.tsx');
const {StateView} = await import('../src-next/kit/states/StateView.tsx');
const {SelectionSurface, SelectionText} = await import('../src-next/kit/selection/surface.tsx');
const {BottomSheet} = await import('../src-next/kit/overlays/BottomSheet.tsx');
const {TVDesignCanvas} = await import('../src-next/kit/overlays/TVDesignCanvas.tsx');
const {Dialog, FocusBoundary, SurfaceHeader, SidePanel} = await import('../src-next/kit/overlays/surface.tsx');
const {OverlaysProvider, OverlayPortal} = await import('../src-next/kit/overlays/OverlayHost.tsx');
const {Menu, MenuRow} = await import('../src-next/kit/overlays/Menu.tsx');
const {Button} = await import('../src-next/kit/controls/Button.tsx');
const {Focusable} = await import('../src-next/kit/controls/Focusable.tsx');
const {SelectionCircle} = await import('../src-next/kit/selection/SelectionCircle.tsx');
const {TextField} = await import('../src-next/kit/controls/TextField.tsx');
const {Select} = await import('../src-next/kit/controls/Select.tsx');
const {Switch} = await import('../src-next/kit/controls/Switch.tsx');

type Element = ReactElement<Record<string, any>>;
function elements(node: ReactNode): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!React.isValidElement<Record<string, any>>(node)) return [];
  return [node, ...elements(node.props.children)];
}
const ofType = (node: ReactNode, type: unknown) => elements(node).filter(element => element.type === type);
const text = (node: ReactNode): string => Array.isArray(node) ? node.map(text).join(' ') : React.isValidElement<Record<string, any>>(node) ? text(node.props.children) : typeof node === 'string' || typeof node === 'number' ? String(node) : '';
async function mount(component: any, props: any) {
  let tree: ReactNode;
  const host = createFabricHookHost(() => {tree = typeof component === 'function' ? component(props) : component.render(props, props.ref ?? null);});
  await host.mount();
  return {get tree() {return tree;}, async update(next: any) {props = next; await host.rerender();}, host};
}
function bulk(overrides: Partial<BulkEditorProps> = {}): BulkEditorProps {
  return {open: true, onOpenChange() {}, title: 'Edit actual movies', count: 4, saveLabel: 'Save actual movies',
    schema: [{field: 'year', label: 'Year', group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: true}, {field: 'tags', label: 'Tags', group: 'general', type: 'list', maxLength: 128, max: 64, bulk: true}],
    singleItemFields: [{field: 'title', label: 'Title', group: 'general', type: 'text', bulk: false}],
    values: {year: {state: 'mixed', distinctCount: 4, truncated: true, values: [{value: '1995', count: 1}], entries: []}, tags: {state: 'mixed', distinctCount: 2, truncated: false, values: [], entries: [{value: 'Existing', count: 3}]}},
    changes: {}, onChangesChange() {}, lockEdited: true, onLockEditedChange() {}, onSave() {}, onRetry() {}, resultEntries: [{id: 'a', title: 'Actual saved title'}, {id: 'b', title: 'Actual failed title'}], ownerHeader: null, fieldLabel: field => field.label, summary: [], ...overrides};
}

test('TV BulkEditor binds real schema fields to native controls without writing untouched Mixed or locks', async () => {
  const calls: unknown[] = [];
  const props = bulk({onChangesChange: changes => calls.push(changes), onLockEditedChange: value => calls.push(value), onSave: () => calls.push('save')});
  const editor = await mount(BulkEditor, props);
  try {
    const surface = editor.tree as Element;
    assert.equal(surface.type, SelectionSurface);
    assert.equal(surface.props.returnFocusRef, props.returnFocusRef);
    assert.equal(ofType(surface.props.footer, Button).at(-1)!.props.disabled, true);
    const scalar = elements(surface).find(element => element.props.field?.field === 'year')!;
    const input = await mount(scalar.type, scalar.props);
    try {
      const field = ofType(input.tree, TextField)[0]!;
      assert.equal(field.props.value, ''); assert.equal(field.props.keyboardType, 'numbers-and-punctuation');
      assert.equal(field.props.fieldStyle.height, 64); assert.equal(field.props.fieldStyle.borderRadius, 14);
      assert.match(text(input.tree), /Mixed/); assert.match(text(input.tree), /4 values/);
      assert.deepEqual(calls, []);
      const focus = {focus() {}}; field.props.ref(focus);
      assert.equal(surface.props.initialFocusRef.current, focus, 'initial focus targets the actual first TextField');
      const control = await mount(TextField, field.props);
      try {
        const nativeInput = elements(control.tree).find(element => element.type === NativeInput)!;
        assert.ok(nativeInput); assert.equal(nativeInput.props.keyboardType, 'numbers-and-punctuation');
        nativeInput.props.ref(focus);
        assert.equal(surface.props.initialFocusRef.current, focus, 'registry binding forwards the actual TextInput host');
        nativeInput.props.onChangeText('2020');
        assert.deepEqual(calls, [{year: {value: '2020'}}]);
        nativeInput.props.ref(null);
        assert.equal(surface.props.initialFocusRef.current, null, 'unmount clears the forwarded native host');
      } finally {await control.host.unmount();}
      assert.deepEqual(calls, [{year: {value: '2020'}}]);
    } finally {await input.host.unmount();}
    ofType(surface, Switch)[0]!.props.onCheckedChange(false);
    assert.deepEqual(calls.at(-1), false);
    const changed = {...props, changes: {year: {value: '2020'}}};
    await editor.update(changed);
    const save = ofType((editor.tree as Element).props.footer, Button).at(-1)!;
    assert.equal(save.props.disabled, false); save.props.onPress(); assert.equal(calls.at(-1), 'save');
  } finally {await editor.host.unmount();}
});

test('TV list controls use incremental callbacks and an actual keyboard TextField', async () => {
  const calls: unknown[] = [];
  const editor = await mount(BulkEditor, bulk({onChangesChange: changes => calls.push(changes)}));
  const listNode = elements(editor.tree).find(element => element.props.field?.field === 'tags')!;
  const list = await mount(listNode.type, listNode.props);
  try {
    const chip = ofType(list.tree, Focusable)[0]!;
    assert.equal(chip.props.focusKind, 'chip'); assert.equal(chip.props.style.height, 56);
    chip.props.onPress(); assert.deepEqual(calls, [{tags: {add: [], remove: ['Existing']}}]);
    ofType(list.tree, TextField)[0]!.props.onChangeText('New value'); await list.host.flush();
    ofType(list.tree, TextField)[0]!.props.onSubmitEditing();
    assert.deepEqual(calls.at(-1), {tags: {add: ['New value'], remove: []}});
  } finally {await list.host.unmount(); await editor.host.unmount();}
});

test('TV choice fields stay on the shared Select and no-fields remains its own truthful explanation', async () => {
  const props = bulk({fieldOptions: {year: [{value: '2020', label: '2020'}]}});
  const editor = await mount(BulkEditor, props);
  try {
    const scalar = elements(editor.tree).find(element => element.props.field?.field === 'year')!;
    const choice = await mount(scalar.type, scalar.props);
    try {
      assert.equal((choice.tree as Element).type, Select); assert.equal((choice.tree as Element).props.description, '4 values');
      const select = await mount(Select, (choice.tree as Element).props);
      try {
        const implementation = select.tree as Element;
        const control = await mount(implementation.type, implementation.props);
        try {
          const trigger = ofType(control.tree, Focusable)[0]!;
          assert.equal(trigger.props.focusRadius, 14); trigger.props.onPress({}); await control.host.flush();
          const picker = ofType(control.tree, Dialog)[0]!;
          assert.equal(picker.props.open, true); assert.equal(picker.props.placement, 'side'); assert.equal(picker.props.width, 640);
        } finally {await control.host.unmount();}
      } finally {await select.host.unmount();}
    } finally {await choice.host.unmount();}
    await editor.update({...props, schema: []});
    assert.match(text(editor.tree), /These fields are edited one title at a time/);
    assert.doesNotMatch(text(editor.tree), /Edit works on one type/);
    assert.equal(ofType((editor.tree as Element).props.footer, Button).at(-1)!.props.disabled, true);
  } finally {await editor.host.unmount();}
});

test('TV partial receipts retain actual rows and retry only actual failures, including row retry and focus', async () => {
  const calls: unknown[] = [];
  const returnFocusRef = {current: {requestTVFocus() {calls.push('return');}}};
  const props = bulk({returnFocusRef, onRetry: ids => calls.push(ids)});
  const editor = await mount(BulkEditor, props);
  try {
    const result = {updated: 1, failed: 1, results: [{kind: 'item', id: 'a', ok: true}, {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict'}]};
    await editor.update({...props, result});
    const surface = editor.tree as Element;
    assert.equal(surface.props.returnFocusRef, returnFocusRef);
    assert.match(text(surface), /Actual saved title/); assert.match(text(surface), /Actual failed title/);
    assert.match(text(surface), /Someone else changed this title while you were editing/);
    ofType(surface, Button).find(button => text(button.props.children) === 'Try again')!.props.onPress();
    const footerRetry = ofType(surface.props.footer, Button).at(-1)!;
    footerRetry.props.onPress(); assert.deepEqual(calls, [['b'], ['b']]);
    assert.equal(surface.props.initialFocusRef, footerRetry.props.ref);
    footerRetry.props.ref.current = {requestTVFocus() {calls.push(['retry-focus']);}};
    await editor.update({...props, result, pending: true}); await editor.update({...props, result, pending: false});
    await editor.update({...props, result: {...result, updated: 0, failed: 2, results: result.results.map(row => ({...row, ok: false}))}});
    assert.deepEqual(calls.at(-1), ['retry-focus']);
  } finally {await editor.host.unmount();}
});

test('the genuine TV surface retains the existing side-sheet recipe, initial/return refs and modal focus boundary', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const focused: string[] = [];
  const ref = {current: {requestTVFocus() {focused.push('initial');}}};
  const restore = {current: {requestTVFocus() {focused.push('return');}}};
  const surface = await mount(SelectionSurface, {open: true, onOpenChange() {}, title: 'Actual editor', children: null, fullHeight: true, initialFocusRef: ref, returnFocusRef: restore});
  try {
    const sheet = surface.tree as Element;
    assert.equal(sheet.type, BottomSheet); assert.equal(sheet.props.width, 680);
    assert.equal(sheet.props.initialFocusRef, ref); assert.equal(sheet.props.returnFocusRef, restore);
    const body = await mount(BottomSheet, sheet.props);
    try {
      const boundary = ofType(body.tree, FocusBoundary)[0]!;
      assert.equal(boundary.props.modal, true);
      const panel = elements(body.tree).find(node => Array.isArray(node.props.style) && node.props.style.some((value: any) => value?.paddingRight === 96))!;
      assert.ok(panel); assert.equal(panel.props.style[1].paddingLeft, 56);
      t.mock.timers.tick(320); assert.deepEqual(focused, ['initial']);
    } finally {await body.host.unmount();}
    assert.deepEqual(focused, ['initial', 'return']);
    const copy = await mount(SelectionText, {children: 'Actual message'});
    try {assert.equal((copy.tree as Element).props.style[0].fontSize, 24);} finally {await copy.host.unmount();}
  } finally {await surface.host.unmount();}
});

test('compact selection headers opt into phone frame spacing while preserving explicit and nonphone headers', async () => {
  const previous = state.theme;
  const action = React.createElement('Button', {testID: 'real-header-action'});
  try {
    for (const options of [{isTV: false, width: 390}, {isTV: false, width: 1024}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const props = {open: true, onOpenChange() {}, title: 'Actual title', description: 'Actual description', children: null, compactHeader: true, headerAction: action};
      const surface = await mount(SelectionSurface, props);
      try {
        const sheet = surface.tree as Element, header = sheet.props.headerContent;
        if (state.theme.platform === 'phone') {
          assert.equal(header.type, SurfaceHeader); assert.equal(header.props.action, action);
          const rendered = await mount(SurfaceHeader, header.props);
          try {
            assert.equal((rendered.tree as Element).props.style.paddingTop, 4);
            assert.equal((rendered.tree as Element).props.style.paddingBottom, 10);
            const texts = ofType(rendered.tree, 'Text');
            assert.equal(text(texts[0]), props.title); assert.equal(text(texts[1]), props.description);
            const description = flattenStyle(texts[1].props.style);
            assert.equal(description.color, state.theme.colors.textTertiary); assert.equal(description.marginTop, 4);
            assert.equal(description.fontSize, 13.5 * state.theme.textScale); assert.equal(description.lineHeight, 19.575 * state.theme.textScale);
          } finally {await rendered.host.unmount();}
        } else assert.equal(header, undefined);
        await surface.update({...props, fullHeight: true}); assert.equal((surface.tree as Element).props.headerContent, undefined);
        await surface.update({...props, headerContent: null}); assert.equal((surface.tree as Element).props.headerContent, null);
        await surface.update({...props, compactHeader: false}); assert.equal((surface.tree as Element).props.headerContent, undefined);
      } finally {await surface.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('TV More keeps owner commands out of a television selection while non-owner commands keep their callbacks', async () => {
  const calls: unknown[] = [];
  const props: SelectionBarProps = {count: 4, selectAllLabel: 'Select all', onSelectAll() {}, onCancel() {}, actions: [{id: 'more'}, {id: 'collection'}, {id: 'edit'}, {id: 'refresh'}, {id: 'delete', disabled: true, reason: 'One library required'}, {id: 'download'}], onAction: action => calls.push(action), menu: 'more', onMenuChange() {}};
  const bar = await mount(SelectionBar, props);
  try {
    const menus = elements(bar.tree).find(element => element.props.menu === 'more' && element.type !== SelectionBar)!;
    const menu = await mount(menus.type, menus.props);
    try {
      const element = menu.tree as Element; assert.equal(element.type, Menu); assert.equal(element.props.presentation, 'sideSheet');
      assert.equal(element.props.returnFocusRef, ofType(bar.tree, Button).find(button => button.props.accessibilityLabel === 'More')!.props.ref);
      const items = element.props.items.filter((entry: any) => entry.onSelect);
      // select 24 draws no owner area, so Edit, Refresh and Delete never reach a TV menu even
      // when the catalogue offers them; download joins them in the TV-only admission.
      assert.deepEqual(items.map((entry: any) => entry.id), ['collection']);
      items[0].onSelect(); assert.deepEqual(calls, ['collection']);
      await menu.update({...menus.props, actions: []}); assert.deepEqual((menu.tree as Element).props.items, []);
    } finally {await menu.host.unmount();}
  } finally {await bar.host.unmount();}
});

test('TV mixed Edit offers genuine Keep-only callbacks and an actual initial focus row', async () => {
  const calls: unknown[] = [];
  const props = {open: true, onOpenChange: (...args: unknown[]) => calls.push(args), total: 2, description: 'Actual mixed-type reason', groups: [{kind: 'movie', label: 'Movies', count: 1, icon: 'i-film'}, {kind: 'episode', label: 'Episodes', count: 1, icon: 'i-tv'}], onKeepOnly: (kind: string) => calls.push(kind)};
  const mixed = await mount(MixedEditExplainer, props);
  try {
    const surface = mixed.tree as Element; assert.equal(surface.type, SelectionSurface); assert.equal(surface.props.description, 'Actual mixed-type reason');
    const rows = ofType(surface, Focusable); assert.equal(rows.length, 2); assert.equal(rows[0]!.props.ref, surface.props.initialFocusRef);
    rows[1]!.props.onPress(); assert.deepEqual(calls, ['episode', [false, 'action']]);
    await mixed.update({...props, groups: [{kind: 'season', label: 'Seasons', count: 2, icon: 'i-tv'}], description: 'Actual no-fields reason'});
    const single = mixed.tree as Element;
    assert.equal(single.props.title, 'These fields are edited one title at a time.');
    assert.equal(single.props.description, 'Actual no-fields reason'); assert.equal(ofType(single, Focusable).length, 0);
    assert.equal(single.props.initialFocusRef, single.props.footer.props.ref);
  } finally {await mixed.host.unmount();}
});

test('phone mixed Edit uses full-width drawn rows and a continuous footer without changing Keep-only or Close', async () => {
  const previous = state.theme, calls: unknown[] = [];
  const props = {open: true, onOpenChange: (...args: unknown[]) => calls.push(args), total: 4, description: 'Actual mixed reason', groups: [{kind: 'movie', label: 'Movie', count: 1, icon: 'i-film'}, {kind: 'episode', label: 'Episode', count: 1, icon: 'i-tv'}], onKeepOnly: (kind: string) => calls.push(kind)};
  try {
    for (const options of [{isTV: false, width: 390}, {isTV: false, width: 1024}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const mixed = await mount(MixedEditExplainer, props);
      try {
        const surface = mixed.tree as Element, phone = state.theme.platform === 'phone';
        assert.deepEqual(surface.props.footerPadding, phone ? {top: 12, bottom: 34, keyboardBottom: 12} : undefined);
        assert.equal(surface.props.contentFooterGap, phone ? 0 : undefined);
        assert.equal(surface.props.showFooterSeparator, phone ? false : undefined);
        assert.equal(surface.props.contentStyle.paddingHorizontal, phone || options.isTV ? 0 : 20);
        const rows = ofType(surface, Focusable);
        assert.equal(rows.length, 2); assert.equal(rows[0].props.ref, surface.props.initialFocusRef);
        if (phone) {
          assert.equal(surface.props.headerContent.props.description, props.description);
          assert.equal(surface.props.headerContent.props.descriptionTextStyle.color, state.theme.colors.textTertiary);
          const labelRow = elements(surface.props.children).find(node => node.props.style?.minHeight === 48)!;
          assert.ok(labelRow); assert.equal(labelRow.props.style.paddingHorizontal, 20);
          for (const row of rows) {
            const style = flattenStyle(row.props.style);
            assert.equal(style.minHeight, 56); assert.equal(style.paddingVertical, 8); assert.equal(style.paddingHorizontal, 20);
          }
          rows[1].props.onPress(); surface.props.footer.props.onPress();
          assert.deepEqual(calls, ['episode', [false, 'action'], [false, 'action']]);
        } else assert.equal(surface.props.headerContent, undefined);
        await mixed.update({...props, groups: [props.groups[0]]});
        const single = mixed.tree as Element;
        assert.equal(single.props.headerContent, undefined); assert.equal(single.props.footerPadding, undefined);
        assert.equal(single.props.initialFocusRef, single.props.footer.props.ref);
      } finally {await mixed.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('TV Delete keeps the owner, preview, exact confirmation and restriction gates on the real dialog', async () => {
  const calls: unknown[] = [];
  const preview = {revision: 7, confirmation: '1', confirmationKind: 'count' as const, totalBytes: 0, totalFiles: 1, totalBytesText: '0 B', blocked: [], targets: [{itemId: 'a', title: 'Actual title', kind: 'movie', libraryId: 'one', libraryName: 'Actual library', allowMediaDeletion: true, titles: 1, bytes: 0, files: [], dependents: []}]};
  const props: DeleteMediaDialogProps = {testID: 'tv.authority.delete', open: true, onOpenChange() {}, ownerHeader: null, owner: true, selectionCount: 1, preview, confirmation: '', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete: (...args) => calls.push(args)};
  const dialog = await mount(DeleteMediaDialog, props);
  try {
    let surface = dialog.tree as Element; assert.equal(surface.type, SelectionSurface);
    assert.equal(surface.props.initialFocusRef, ofType(surface.props.footer, Button)[0]!.props.ref);
    assert.equal(await nativeControlID(ofType(surface.props.footer, Button)[0]!), controlID(props.testID, 'dismiss'));
    assert.equal(ofType(surface, TextField)[0]!.props.testID, controlID(props.testID, 'confirmation'));
    assert.equal(ofType(surface, Switch)[0]!.props.testID, controlID(props.testID, 'remove-files'));
    let remove = ofType(surface.props.footer, Button)[1]!; assert.equal(remove.props.disabled, true); remove.props.onPress(); assert.deepEqual(calls, []);
    await dialog.update({...props, confirmation: '1'}); surface = dialog.tree as Element;
    remove = ofType(surface.props.footer, Button)[1]!; assert.equal(remove.props.disabled, false); remove.props.onPress(); assert.deepEqual(calls, [[preview, '1', true]]);
    await dialog.update({...props, selectionCount: 201}); assert.match(text(dialog.tree), /Delete at most 200/);
    await dialog.update({...props, preview: {...preview, blocked: ['Actual library']}}); assert.match(text(dialog.tree), /Deleting is turned off/);
    await dialog.update({...props, owner: false}); assert.equal(dialog.tree, null);
  } finally {await dialog.host.unmount();}
});

test('a decorative circle gets its full native style state from Pressable without becoming a focus target', async () => {
  const nativeState = {pressed: false, focused: false};
  let received: unknown;
  let presses = 0;
  const circle = await mount(SelectionCircle, {selected: true, interactive: false, accessibilityLabel: 'Selected title',
    onPress: () => {presses++;}, style: (value: typeof nativeState) => {received = value; return {opacity: .5};}});
  try {
    const host = circle.tree as Element;
    assert.equal(host.type, 'Pressable');
    assert.equal(host.props.disabled, true); assert.equal(host.props.focusable, false);
    assert.equal(host.props.pointerEvents, 'none'); assert.equal(host.props.accessible, false);
    assert.equal(host.props.importantForAccessibility, 'no-hide-descendants');
    assert.equal(host.props.onPress, undefined); assert.equal(host.props.accessibilityLabel, undefined);
    host.props.style(nativeState);
    assert.equal(received, nativeState); assert.equal(presses, 0);
  } finally {await circle.host.unmount();}
});

// Follow each receiving seam through the real controls to the native host.
async function nativeControlID(element: Element): Promise<string | undefined> {
  const mounted: Awaited<ReturnType<typeof mount>>[] = [];
  try {
    let current = element;
    while (typeof current.type !== 'string') {
      const instance = await mount(current.type, current.props); mounted.push(instance);
      current = instance.tree as Element;
    }
    assert.equal(current.type, 'Pressable');
    return current.props.testID;
  } finally {for (const instance of mounted.reverse()) await instance.host.unmount();}
}

test('selection actions and their internal menu carry the authority fence to actual Pressables', async () => {
  const prefix = 'tv.[server,account,profile,epoch,viewer].selection-bar';
  const props: SelectionBarProps = {testID: prefix, count: 4, selectAllLabel: 'All', onSelectAll() {}, onCancel() {}, onAction() {}, menu: 'queue', onMenuChange() {}, actions: [{id: 'play'}, {id: 'queue'}, {id: 'watched'}, {id: 'more'}, {id: 'playNext'}, {id: 'playLater'}]};
  const bar = await mount(SelectionBar, props);
  try {
    const buttons = ofType(bar.tree, Button);
    assert.deepEqual(await Promise.all(buttons.map(nativeControlID)), ['play', 'queue', 'watched', 'more'].map(id => controlID(prefix, 'action', id)).concat([controlID(prefix, 'select-all'), controlID(prefix, 'dismiss')]));
    const menuNode = elements(bar.tree).find(element => element.props.menu === 'queue' && element.type !== SelectionBar)!;
    const menu = await mount(menuNode.type, menuNode.props);
    try {
      assert.equal((menu.tree as Element).props.testID, controlID(prefix, 'menu', 'queue'));
      const shared = await mount((menu.tree as Element).type, (menu.tree as Element).props);
      try {assert.deepEqual(await Promise.all(ofType((menu.tree as Element).props.children, MenuRow).map(nativeControlID)), ['playNext', 'playLater'].map(action => `${controlID(prefix, 'menu', 'queue')}-item-${action}`));} finally {await shared.host.unmount();}
      assert.equal((menu.tree as Element).props.returnFocusRef, buttons[1]!.props.ref);
      await menu.update({...menuNode.props, menu: null, returnFocusRef: undefined});
      assert.equal((menu.tree as Element).props.testID, controlID(prefix, 'menu', 'queue'), 'closing animation retains its semantic surface identity');
      assert.equal((menu.tree as Element).props.returnFocusRef, buttons[1]!.props.ref, 'closing animation retains the real opener ref');
    } finally {await menu.host.unmount();}
    await bar.update({...props, count: 99, selectAllLabel: 'Translated copy'});
    assert.deepEqual(await Promise.all(ofType(bar.tree, Button).map(nativeControlID)), await Promise.all(buttons.map(nativeControlID)));
  } finally {await bar.host.unmount();}
});

test('picker destination identity survives translation/reorder and find/name IDs reach TextInputs', async () => {
  const calls: unknown[] = [], prefix = 'tv.authority.picker.playlist';
  const props = {testID: prefix, open: true, onOpenChange() {}, count: 2, selectionLabel: 'Selected', entries: [{id: 'one/two', title: 'Same', size: 1, canAdd: true}, {id: 'one.two', title: 'Same', size: 2, canAdd: false}], creating: false, onCreatingChange() {}, name: '', onNameChange() {}, onCreateAndAdd() {}, onAdd: (id: string) => calls.push(id), query: '', onQueryChange() {}, showFind: true, onRetry() {}, onViewDestination() {}, onResultDismiss() {}};
  const wrapper = await mount(PlaylistPicker, props), picker = await mount((wrapper.tree as Element).type, (wrapper.tree as Element).props);
  const rowIDs = async () => {
    const list = elements(picker.tree).find(element => element.type === 'FlatList')!;
    return Promise.all(list.props.data.map(async (item: unknown) => {const row = list.props.renderItem({item}); return {id: await nativeControlID(row), row};}));
  };
  try {
    const rows = await rowIDs();
    assert.notEqual(rows[0].id, rows[1].id);
    assert.equal(rows[0].id, controlID(prefix, 'destination', 'playlist', 'one/two'));
    rows[0].row.props.onPress(); rows[1].row.props.onPress(); assert.deepEqual(calls, ['one/two']);
    const find = ofType(picker.tree, TextField)[0]!;
    const input = await mount(TextField, find.props);
    try {assert.equal(elements(input.tree).find(element => element.type === NativeInput)!.props.testID, controlID(prefix, 'find'));} finally {await input.host.unmount();}
    await picker.update({...props, kind: 'playlist', entries: [...props.entries].reverse().map(entry => ({...entry, title: 'Translated'}))});
    assert.deepEqual((await rowIDs()).map((entry: {id: string}) => entry.id), rows.map((entry: {id: string}) => entry.id).reverse());
    await picker.update({...props, kind: 'playlist', creating: true});
    const name = await mount(TextField, ofType(picker.tree, TextField)[0]!.props);
    try {assert.equal(elements(name.tree).find(element => element.type === NativeInput)!.props.testID, controlID(prefix, 'name'));} finally {await name.host.unmount();}
    await picker.update({...props, kind: 'playlist', entries: []});
    const surface = ofType(picker.tree, SelectionSurface)[0]!;
    const create = ofType(surface, Focusable).find(element => element.props.testID === controlID(prefix, 'new'))!;
    assert.equal(surface.props.initialFocusRef, create.props.ref, 'empty picker enters the actual New control');
  } finally {await picker.host.unmount(); await wrapper.host.unmount();}
});

test('bulk field/value/result IDs use schema and entity keys while native lock/save controls stay fenced', async () => {
  const prefix = 'tv.authority.editor';
  const editor = await mount(BulkEditor, bulk({testID: prefix, changes: {year: {value: '2020'}}}));
  try {
    const scalarNode = elements(editor.tree).find(element => element.props.field?.field === 'year')!;
    const scalar = await mount(scalarNode.type, scalarNode.props);
    try {
      const field = ofType(scalar.tree, TextField)[0]!, input = await mount(TextField, field.props);
      try {assert.equal(elements(input.tree).find(element => element.type === NativeInput)!.props.testID, controlID(prefix, 'field', 'year', 'input'));} finally {await input.host.unmount();}
    } finally {await scalar.host.unmount();}
    const listNode = elements(editor.tree).find(element => element.props.field?.field === 'tags')!;
    const list = await mount(listNode.type, listNode.props);
    try {assert.equal(await nativeControlID(ofType(list.tree, Focusable)[0]!), controlID(prefix, 'field', 'tags', 'value-toggle', 'Existing'));} finally {await list.host.unmount();}
    const lock = await mount(Switch, ofType(editor.tree, Switch)[0]!.props);
    try {assert.equal(await nativeControlID(lock.tree as Element), controlID(prefix, 'lock-edited'));} finally {await lock.host.unmount();}
    const surface = editor.tree as Element;
    assert.deepEqual(await Promise.all(ofType(surface.props.footer, Button).map(nativeControlID)), [controlID(prefix, 'dismiss'), controlID(prefix, 'save')]);
    await editor.update(bulk({testID: prefix, result: {updated: 0, failed: 2, results: [{kind: 'item', id: 'same', ok: false}, {kind: 'person', id: 'same', ok: false}]}}));
    const retries = ofType(editor.tree, Button).filter(button => button.props.testID?.includes('result'));
    assert.deepEqual(await Promise.all(retries.map(nativeControlID)), [controlID(prefix, 'result', 'item', 'same', 'retry'), controlID(prefix, 'result', 'person', 'same', 'retry')]);
  } finally {await editor.host.unmount();}
});


test('phone text-entry sheet moves body and footer together for keyboard events and restores refs', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const previousTheme = state.theme, previousViewport = state.viewport;
  state.theme = createTheme({isTV: false, width: 390});
  state.viewport = {width: 390, height: 844, scale: 1, fontScale: 1};
  const calls: string[] = [];
  const initial = {current: {focus() {calls.push('input');}}};
  const returning = {current: {focus() {calls.push('opener');}}};
  const props = {open: true, onOpenChange() {}, title: 'Actual entry', children: React.createElement('TextInput', {testID: 'actual-input'}), footer: React.createElement('Button', {testID: 'actual-footer'}), initialFocusRef: initial, returnFocusRef: returning, testID: 'phone.entry', detents: [800], keyboardAvoiding: true};
  const sheet = await mount(BottomSheet, props);
  try {
    const boundary = () => elements(sheet.tree).find(element => element.props.testID === 'phone.entry-keyboard-boundary')!;
    const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
    const getSheet = () => elements(sheet.tree).find(element => element.props.testID === 'phone.entry')!;
    t.mock.timers.tick(500);
    assert.deepEqual(calls, ['input']);
    assert.equal(flatten(boundary().props.style).marginBottom, 0);
    keyboardListeners.get('keyboardWillShow')!({height: 300, duration: 0}); await sheet.host.rerender();
    assert.equal(flatten(boundary().props.style).marginBottom, 300);
    assert.ok(elements(boundary()).some(element => element.props.testID === 'actual-footer'));
    assert.ok(elements(boundary()).some(element => element.props.testID === 'actual-input'));
    assert.ok(flatten(getSheet().props.style).height <= 544);
    assert.deepEqual(calls, ['input'], 'keyboard movement must not steal focus');
    const outer = sheet.tree as Element;
    const measured = elements(outer).find(element => typeof element.props.onLayout === 'function' && element.type === 'View')!;
    measured.props.onLayout({nativeEvent: {layout: {height: 544}}}); await sheet.host.rerender();
    assert.equal(flatten(boundary().props.style).marginBottom, 0, 'Android resize must not subtract the keyboard twice');
    assert.ok(flatten(getSheet().props.style).height <= 544);
    measured.props.onLayout({nativeEvent: {layout: {height: 844}}}); await sheet.host.rerender();
    keyboardListeners.get('keyboardWillHide')!({height: 0, duration: 0}); await sheet.host.rerender();
    assert.equal(flatten(boundary().props.style).marginBottom, 0);
    assert.equal(flatten(getSheet().props.style).height, 800);
    await sheet.host.unmount();
    assert.deepEqual(calls, ['input', 'opener']);
    assert.equal(keyboardListeners.size, 0);
    const regular = await mount(BottomSheet, {...props, keyboardAvoiding: false});
    try {assert.equal(keyboardListeners.size, 0, 'default sheets do not opt into keyboard handling');} finally {await regular.host.unmount();}
  } finally {state.theme = previousTheme; state.viewport = previousViewport; await sheet.host.unmount();}
});


test('phone queue uses the supplied kind, unboxed rows, denial copy and original authority IDs', async () => {
  const previousTheme = state.theme;
  state.theme = createTheme({isTV: false, width: 390});
  const calls: unknown[] = [];
  const props: SelectionBarProps = {testID: 'phone.authority.queue', count: 3, selectionLabel: '3 episodes', selectAllLabel: 'All', onSelectAll() {}, onCancel() {}, onAction: action => calls.push(action), menu: 'queue', onMenuChange: menu => calls.push(menu), actions: [{id: 'playNext'}, {id: 'playLater', disabled: true, reason: 'Actual unavailable reason'}]};
  const bar = await mount(SelectionBar, props);
  try {
    const node = elements(bar.tree).find(element => element.props.menu === 'queue' && element.type !== SelectionBar)!;
    const menu = await mount(node.type, node.props);
    try {
      const surface = menu.tree as Element;
      assert.equal(surface.type, SelectionSurface); assert.equal(surface.props.title, 'Add 3 episodes to the queue');
      assert.equal(surface.props.compactHeader, true);
      assert.equal(surface.props.description, 'In the order you picked them');
      const rows = ofType(surface, SelectionActionRow);
      assert.equal(rows[0].props.description, 'After what’s playing now');
      assert.equal(rows[1].props.description, 'Actual unavailable reason'); assert.equal(rows[1].props.disabled, true);
      assert.equal(await nativeControlID(rows[0]), `${controlID('phone.authority.queue', 'menu', 'queue')}-item-playNext`);
      rows[0].props.onPress(); assert.deepEqual(calls, ['playNext', null]);
    } finally {await menu.host.unmount();}
  } finally {await bar.host.unmount(); state.theme = previousTheme;}
});


test('phone Selection action line boxes retain their type and exclude additional native font padding', async () => {
  const previous = state.theme;
  try {
    for (const width of [390, 820]) {
      state.theme = createTheme({isTV: false, width, textScale: 1.2});
      const calls: string[] = [], ref = {current: null};
      const row = await mount(SelectionActionRow, {label: 'Actual queue action', description: 'Actual queue order', icon: 'i-queue', ref, onPress: () => calls.push('press')});
      try {
        const [label, description] = ofType(row.tree, SelectionText), phone = width === 390;
        assert.equal(label.props.weight, 600); assert.equal(description.props.weight, 500);
        assert.equal(label.props.style.includeFontPadding, phone ? false : undefined);
        assert.equal(description.props.style.includeFontPadding, phone ? false : undefined);
        assert.equal(label.props.style.lineHeight, phone ? undefined : 24, 'the phone label inherits the actual font normal line metrics as drawn');
        assert.equal(description.props.style.lineHeight, phone ? 16.875 * state.theme.textScale : 16.875);
        assert.equal(description.props.style.marginTop, 2);
        const native = row.tree as Element;
        assert.equal(native.props.ref, ref); assert.equal(native.props.focusKind, 'row');
        assert.equal(flattenStyle(native.props.style({focused: false, hovered: false, pressed: false})).minHeight, 56);
        native.props.onPress(); assert.deepEqual(calls, ['press']);
      } finally {await row.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('phone More paints its owner border inside the group without adding a layout inset or changing actions', async () => {
  const previous = state.theme;
  try {
    for (const width of [390, 820]) {
      state.theme = createTheme({isTV: false, width});
      const calls: unknown[] = [], ownerTag = React.createElement('View', {testID: 'actual-policy-badge'});
      const props: SelectionBarProps = {testID: 'actual.owner.more', count: 1, selectionLabel: '1 movie', selectAllLabel: 'All', menu: 'more', ownerTag,
        onSelectAll() {}, onCancel() {}, onAction: action => calls.push(action), onMenuChange: menu => calls.push(menu), actions: [{id: 'edit'}, {id: 'delete'}]};
      const bar = await mount(SelectionBar, props), element = elements(bar.tree).find(node => node.props.menu === 'more' && node.type !== SelectionBar)!;
      const menu = await mount(element.type, element.props);
      try {
        const group = elements(menu.tree).find(node => node.props.style?.backgroundColor === '#0b1822')!;
        assert.ok(group); assert.equal(group.props.style.borderWidth, width === 390 ? 0 : 1);
        const border = elements(group.props.children).find(node => node.props.style?.borderWidth === 1);
        assert.equal(!!border, width === 390);
        if (border) {assert.equal(border.props.pointerEvents, 'none'); assert.equal(border.props.style.position, 'absolute'); assert.equal(border.props.style.borderColor, 'rgba(92,179,226,.18)');}
        assert.ok(elements(group).includes(ownerTag));
        const rows = ofType(group, SelectionActionRow); assert.equal(rows.length, 2); assert.ok(rows[0].props.ref);
        assert.deepEqual(calls, []); rows[0].props.onPress(); assert.deepEqual(calls, ['edit', null]);
      } finally {await menu.host.unmount(); await bar.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('content-fit Selection surfaces do not reserve an empty footer or default Done control', async () => {
  const surface = await mount(SelectionSurface, {open: true, onOpenChange() {}, title: 'Actual queue', children: null});
  try {const sheet = surface.tree as Element; assert.equal(sheet.props.footer, undefined); assert.equal(sheet.props.showClose, false); assert.equal(sheet.props.fitContent, true);} finally {await surface.host.unmount();}
});


test('a real deletion preview error stays unavailable and cannot invoke the destructive callback', async () => {
  const calls: unknown[] = [];
  const props: DeleteMediaDialogProps = {open: true, onOpenChange() {}, ownerHeader: null, owner: true, selectionCount: 4, preview: null, error: 'Actual preview unavailable', confirmation: '4', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete: (...args) => calls.push(args)};
  const dialog = await mount(DeleteMediaDialog, props);
  try {
    assert.match(text(dialog.tree), /Actual preview unavailable/);
    assert.doesNotMatch(text(dialog.tree), /Checking what would be removed/);
    const button = ofType((dialog.tree as Element).props.footer, Button).at(-1)!;
    assert.equal(button.props.disabled, true); button.props.onPress(); assert.deepEqual(calls, []);
  } finally {await dialog.host.unmount();}
});

test('Selection input uses real native focus/blur/ref and keeps default and TV fields unchanged', async () => {
  const previous = state.theme; state.theme = createTheme({isTV: false, width: 390});
  const forwarded = {current: null as any}; const calls: string[] = [];
  const field = await mount(TextField, {appearance: 'selection', label: 'Confirmation', value: '', fieldStyle: {height: 46}, ref: forwarded, onFocus: () => calls.push('focus'), onBlur: () => calls.push('blur')});
  const flatten = (s: any): any => Array.isArray(s) ? Object.assign({}, ...s.map(flatten)) : s ?? {};
  try {
    let input = elements(field.tree).find(n => n.type === NativeInput)!;
    const host = {focus() {calls.push('native-focus');}}; input.props.ref(host); assert.equal(forwarded.current, host); forwarded.current.focus();
    input.props.onFocus({}); await field.host.rerender();
    input = elements(field.tree).find(n => n.type === NativeInput)!;
    const face = elements(field.tree).find(n => flatten(n.props.style).borderWidth === 2)!;
    assert.equal(flatten(face.props.style).borderColor, state.theme.colors.focus); assert.equal(flatten(face.props.style).backgroundColor, state.theme.colors.ink);
    assert.equal(flatten(input.props.style).minHeight, 42); assert.equal(elements(field.tree).filter(n => flatten(n.props.style).inset === -4).length, 0);
    input.props.onBlur({}); await field.host.rerender(); assert.deepEqual(calls, ['native-focus', 'focus', 'blur']);
    await field.update({appearance: 'selection', value: '', error: 'Actual error', editable: false, multiline: true, ref: forwarded});
    input = elements(field.tree).find(n => n.type === NativeInput)!; assert.equal(input.props.editable, false); assert.equal(input.props.multiline, true); assert.equal(input.props.accessibilityState.disabled, true); assert.match(text(field.tree), /Actual error/);
    await field.update({value: ''}); input = elements(field.tree).find(n => n.type === NativeInput)!; input.props.onFocus({}); await field.host.rerender(); assert.ok(elements(field.tree).some(n => flatten(n.props.style).inset === -4), 'default retains existing ring');
    state.theme = createTheme({isTV: true, width: 1920}); await field.update({appearance: 'selection', value: ''}); assert.ok(elements(field.tree).some(n => flatten(n.props.style).inset === -4), 'TV retains existing ring');
  } finally {await field.host.unmount(); state.theme = previous;}
  assert.equal(forwarded.current, null);
});

test('drawn Selection fields use native accent carets without changing selection or caller colours', async () => {
  const previousTheme = state.theme, previousOS = nativePlatform.OS;
  const selection = {start: 2, end: 4}, onSelectionChange = () => {};
  try {
    for (const os of ['android', 'ios'] as const) {
      nativePlatform.OS = os;
      for (const isTV of [false, true]) {
        state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
        const field = await mount(TextField, {appearance: 'selection', value: 'Actual input', selection, onSelectionChange, autoCorrect: false});
        try {
          const input = () => elements(field.tree).find(node => node.type === NativeInput)!;
          assert.equal(input().props.cursorColor, !isTV && os === 'android' ? state.theme.colors.accentStrong : undefined);
          assert.equal(input().props.selectionColor, !isTV && os === 'ios' ? state.theme.colors.accentStrong : undefined);
          assert.equal(input().props.selection, selection);
          assert.equal(input().props.onSelectionChange, onSelectionChange);
          assert.equal(input().props.autoCorrect, false);
          await field.update({appearance: 'selection', value: '', cursorColor: '#123456', selectionColor: '#654321'});
          assert.equal(input().props.cursorColor, '#123456'); assert.equal(input().props.selectionColor, '#654321');
          await field.update({value: ''});
          assert.equal(input().props.cursorColor, undefined); assert.equal(input().props.selectionColor, undefined);
        } finally {await field.host.unmount();}
      }
    }
  } finally {state.theme = previousTheme; nativePlatform.OS = previousOS;}
});

test('phone sheet name and confirmation fields use the drawn 46pt height without changing editor or TV fields', async () => {
  const previous = state.theme;
  try {
    for (const options of [{isTV: false, width: 390}, {isTV: false, width: 820}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const phone = state.theme.platform === 'phone';
      const wrapper = await mount(PlaylistPicker, {open: true, onOpenChange() {}, count: 1, entries: [], creating: true, onCreatingChange() {}, name: '', onNameChange() {}, onCreateAndAdd() {}, onAdd() {}, query: '', onQueryChange() {}, onViewDestination() {}, onResultDismiss() {}});
      const picker = await mount((wrapper.tree as Element).type, (wrapper.tree as Element).props);
      const deletion = await mount(DeleteMediaDialog, {open: true, onOpenChange() {}, owner: true, selectionCount: 1, preview: {revision: 1, confirmationKind: 'title', confirmation: 'Actual title', totalFiles: 1, totalBytesText: '1 KB', totalBytes: 1024, blocked: [], targets: [{itemId: 'actual', title: 'Actual title', kind: 'movie', libraryId: 'library', libraryName: 'Actual library', allowMediaDeletion: true, titles: 1, bytes: 1024, files: [], dependents: []}]}, confirmation: '', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete() {}});
      const editor = await mount(BulkEditor, bulk());
      try {
        for (const owner of [picker, deletion]) {
          const field = ofType(owner.tree, TextField)[0]!;
          assert.equal(field.props.fieldStyle?.height, phone ? 46 : undefined);
          assert.equal(field.props.style?.fontSize, phone ? 16 * state.theme.textScale : undefined);
        }
        if (phone) {
          const scalarNode = elements(editor.tree).find(node => node.props.field?.field === 'year')!;
          const scalar = await mount(scalarNode.type, scalarNode.props);
          try {assert.equal(ofType(scalar.tree, TextField)[0]!.props.fieldStyle.height, 44);} finally {await scalar.host.unmount();}
        }
      } finally {await editor.host.unmount(); await deletion.host.unmount(); await picker.host.unmount(); await wrapper.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('phone More uses real selected-kind/offer descriptions and cannot invent download facts or owner commands', async () => {
  const previous = state.theme; state.theme = createTheme({isTV: false, width: 390}); const calls: unknown[] = [];
  const props: SelectionBarProps = {testID: 'actual.more', count: 2, selectionLabel: '2 tracks', selectAllLabel: 'All', onSelectAll() {}, onCancel() {}, onAction: id => calls.push(id), menu: 'more', onMenuChange: id => calls.push(id), actions: [{id: 'download'}, {id: 'edit', disabled: true, reason: 'Actual denial', description: 'Actual shared field information'}]};
  const bar = await mount(SelectionBar, props);
  try {
    const node = elements(bar.tree).find(n => n.props.menu === 'more' && n.type !== SelectionBar)!; const menu = await mount(node.type, node.props);
    try {
      assert.equal((menu.tree as Element).props.title, '2 tracks selected'); assert.ok(elements(menu.tree).filter(n => n.type === 'Stop').every(n => typeof n.props.offset === 'number' || /^\d+(?:\.\d+)?%?$/.test(n.props.offset)), 'native SVG stops must use supported numeric or percentage values'); let rows = ofType(menu.tree, SelectionActionRow); assert.equal(rows[0].props.description, undefined); assert.equal(rows[1].props.description, 'Actual denial');
      rows[1].props.onPress(); assert.deepEqual(calls, []); rows[0].props.onPress(); assert.deepEqual(calls, ['download', null]);
      await menu.update({...node.props, actions: [{id: 'download', description: 'Actual 42 MB estimate at actual quality'}]}); rows = ofType(menu.tree, SelectionActionRow); assert.equal(rows.length, 1); assert.equal(rows[0].props.description, 'Actual 42 MB estimate at actual quality'); assert.doesNotMatch(text(menu.tree), /Owner/);
    } finally {await menu.host.unmount();}
  } finally {await bar.host.unmount(); state.theme = previous;}
});

test('phone destinations highlight only real focus state or supplied operation receipt', async () => {
  const previous = state.theme; state.theme = createTheme({isTV: false, width: 390}); const calls: string[] = [];
  const props = {open: true, onOpenChange() {}, count: 1, selectionLabel: '1 movie', entries: [{id: 'actual', title: 'Actual destination', size: 2, canAdd: true}], query: '', onQueryChange() {}, creating: false, onCreatingChange() {}, name: '', onNameChange() {}, onCreateAndAdd() {}, onAdd: (id: string) => calls.push(id), onRetry() {}, onViewDestination() {}, onResultDismiss() {}};
  const wrapper = PlaylistPicker(props); const picker = await mount(wrapper.type, wrapper.props);
  try {
    const list = elements(picker.tree).find(n => n.type === 'FlatList')!; const row = list.props.renderItem({item: props.entries[0]}) as Element;
    assert.equal(row.props.children({focused: false, hovered: false, pressed: false}).props.style[1].backgroundColor, 'transparent');
    assert.equal(row.props.children({focused: true, hovered: false, pressed: false}).props.style[1].backgroundColor, 'rgba(237,245,250,.06)'); assert.deepEqual(calls, []);
    row.props.onPress(); assert.deepEqual(calls, ['actual']);
    await picker.update({...wrapper.props, result: {destination: props.entries[0], inserted: 0, failures: [{id:'failed', title:'Actual failed title', reason:'Actual reason'}], complete:true}});
    assert.match(text(picker.tree), /0 added/); assert.match(text(picker.tree), /Actual reason/);
  } finally {await picker.host.unmount(); state.theme = previous;}
});

test('phone partial edit keeps only failed footer retry without narrowing reason into a duplicate control', async () => {
  const previous = state.theme; state.theme = createTheme({isTV: false, width: 390}); const calls: unknown[] = [];
  const editor = await mount(BulkEditor, bulk({result: {updated:1,failed:1,results:[{kind:'item',id:'a',ok:true},{kind:'item',id:'b',ok:false,code:'metadata_conflict'}]}, onRetry: ids => calls.push(ids)}));
  try {
    assert.equal(ofType(editor.tree, Button).filter(n => n.props.testID?.includes('result')).length, 0);
    const footer = (editor.tree as Element).props.footer; ofType(footer, Button).at(-1)!.props.onPress(); assert.deepEqual(calls, [['b']]); assert.match(text(editor.tree), /Someone else changed this title/);
  } finally {await editor.host.unmount(); state.theme = previous;}
});

test('States link and inline recipes retain real actions and avoid inventing surrounding pages', async () => {
  const previous = state.theme; state.theme = createTheme({isTV:false,width:390}); const calls: string[]=[];
  const layout=await mount(StateLayout,{title:'Actual member state',detailsPresentation:'link',detailsAction:{id:'switch',label:'Switch server',icon:'i-server',onPress:()=>calls.push('switch')}});
  const inline=await mount(StateView,{variant:'search',title:'Actual no matches',body:'Actual query explanation',icon:'i-clock'});
  try {
    const action=elements(layout.tree).find(n=>n.props.action?.id==='switch')!; assert.equal(action.props.action.trailingIcon,undefined); assert.equal(action.props.style.alignSelf,'center'); action.props.action.onPress(); assert.deepEqual(calls,['switch']);
    assert.equal(action.props.iconColor, state.theme.colors.textSecondary);
    const link = await mount(action.type, action.props);
    try {assert.equal(ofType(link.tree, Button)[0]!.props.iconColor, state.theme.colors.textSecondary);} finally {await link.host.unmount();}
    assert.equal(ofType(inline.tree,Icon).length,0); assert.match(text(inline.tree),/Actual query explanation/); assert.doesNotMatch(text(inline.tree),/Recent searches/);
    state.theme = createTheme({isTV: true, width: 1920});
    await layout.update({title: 'Actual TV state', detailsAction: action.props.action});
    assert.equal(elements(layout.tree).find(node => node.props.action?.id === 'switch')!.props.iconColor, undefined, 'TV retains its existing button glyph colour');
  } finally {await layout.host.unmount();await inline.host.unmount();state.theme=previous;}
});

test('Selection keyboard layout reveals the actual native input above the measured footer and preserves default inset recipe', async () => {
  const previous=state.theme, viewport=state.viewport; state.theme=createTheme({isTV:false,width:390}); state.viewport={width:390,height:844,scale:1,fontScale:1}; const calls:unknown[]=[];
  let inputTop=700;
  const input={measureInWindow(callback:any){callback(20,inputTop,350,42);}}; Object.assign(globalThis,{__porticoSelectionFocusedInput:input});
  const sheet=await mount(BottomSheet,{open:true,onOpenChange(){},title:'Actual confirmation',children:null,footer:React.createElement('Button'),keyboardAvoiding:true,footerPadding:{top:10,bottom:30,keyboardBottom:12}});
  const flatten=(s:any):any=>Array.isArray(s)?Object.assign({},...s.map(flatten)):s??{};
  try {
    let scroll=elements(sheet.tree).find(n=>n.type==='ScrollView')!; scroll.props.ref.current={getNativeScrollRef:()=>({measureInWindow(callback:any){callback(0,140,390,404);}}),scrollTo:(value:unknown)=>calls.push(value)};
    scroll.props.onLayout({}); assert.deepEqual(calls,[]);
    const footer=elements(sheet.tree).find(n=>flatten(n.props.style).paddingTop===10 && n.props.onLayout)!; footer.props.onLayout({nativeEvent:{layout:{height:100}}});
    keyboardListeners.get('keyboardWillShow')!({height: 300, duration: 0}); await sheet.host.rerender();
    scroll=elements(sheet.tree).find(n=>n.type==='ScrollView')!; scroll.props.onLayout({}); assert.deepEqual(calls,[{y:314,animated:true}], 'native input bottom742 must clear measured footer at444 plus16 breathing room');
    scroll.props.onScroll({nativeEvent:{contentOffset:{y:314}}}); inputTop=386; scroll.props.onLayout({}); assert.equal(calls.length,1,'already visible focus must not jump or lose its scroll position');
    inputTop=460; scroll.props.onLayout({}); assert.deepEqual(calls.at(-1),{y:388,animated:true},'a later resize adds only the newly obscured distance to actual native offset');
    assert.ok(elements(sheet.tree).some(n=>flatten(n.props.style).paddingBottom===12));
    keyboardListeners.get('keyboardWillHide')!({height: 0, duration: 0}); await sheet.host.rerender(); assert.ok(elements(sheet.tree).some(n=>flatten(n.props.style).paddingBottom===30));
  } finally {await sheet.host.unmount(); state.theme=previous;state.viewport=viewport;Object.assign(globalThis,{__porticoSelectionFocusedInput:undefined});}
});


test('phone deletion receipt fitted height keeps the measured notice above its fixed footer', async () => {
  const previousTheme = state.theme, previousViewport = state.viewport;
  state.theme = createTheme({isTV: false, width: 390, reduceMotion: true});
  state.viewport = {width: 390, height: 844, scale: 1, fontScale: 1};
  const calls: string[] = [];
  const props: DeleteMediaDialogProps = {testID: 'phone.receipt.geometry', open: true, onOpenChange() {calls.push('close');}, ownerHeader: null, owner: true,
    selectionCount: 4, preview: null, confirmation: '', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete() {calls.push('delete');},
    result: {revision: 2, removed: 3, failed: 1, bytesDeleted: 0, filesRetained: true, receipts: [{itemId: 'kept', title: 'Actual kept title', removed: false, filesDeleted: 0, filesKept: 1, bytes: 0, code: 'file_not_removed'}]}};
  const dialog = await mount(DeleteMediaDialog, props);
  const surface = await mount(SelectionSurface, (dialog.tree as Element).props);
  const sheet = await mount(BottomSheet, (surface.tree as Element).props);
  const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
  const panel = () => elements(sheet.tree).find(n => n.props.testID === props.testID)!;
  const scroll = () => ofType(sheet.tree, 'ScrollView')[0]!;
  const header = () => ofType(sheet.tree, 'GestureDetector')[0]!.props.children;
  const footer = () => elements(sheet.tree).find(n => n.props.onLayout && flatten(n.props.style).position === 'absolute' && flatten(n.props.style).borderTopColor)!;
  // Retained Android native measurements: header86, receipt125, footer87.
  // Exercise real measurement callbacks and evaluate rendered viewport geometry;
  // do not calculate the component's expected natural-height formula.
  const measure = async (footerHeight: number) => {
    header().props.onLayout({nativeEvent: {layout: {height: 86}}});
    footer().props.onLayout({nativeEvent: {layout: {height: footerHeight}}}); await sheet.host.rerender();
    scroll().props.onContentSizeChange(350, 125 + flatten(scroll().props.contentContainerStyle).paddingBottom); await sheet.host.rerender();
  };
  const clearance = (viewportBottom: number, footerHeight: number) => {
    const style = flatten(panel().props.style);
    const receiptBottom = viewportBottom - style.height + style.borderTopWidth + style.paddingTop + 86 + 125;
    return viewportBottom - footerHeight - receiptBottom;
  };
  try {
    const actualSurface = dialog.tree as Element, close = ofType(actualSurface.props.footer, Button)[0]!;
    assert.deepEqual(actualSurface.props.footerPadding, {top: 0, bottom: 34, keyboardBottom: 12});
    assert.equal(actualSurface.props.showFooterSeparator, false);
    assert.equal(flatten(footer().props.style).borderTopWidth, 0, 'the phone deletion frame has no divider above its actions');
    assert.equal(actualSurface.props.initialFocusRef, close.props.ref);
    assert.equal(await nativeControlID(close), controlID(props.testID, 'dismiss'));
    assert.match(text(dialog.tree), /Actual kept title/); assert.match(text(dialog.tree), /Kept, because/);
    assert.equal(scroll().props.focusable, undefined, 'native scrolling/focus must not be disabled to hide backing');
    await measure(87);
    assert.equal(clearance(844, 87), 16, 'complete notice must clear the measured fixed footer by16px');
    keyboardListeners.get('keyboardWillShow')!({height: 255, duration: 0}); await sheet.host.rerender(); await measure(58);
    assert.equal(clearance(589, 58), 16, 'fitted content and footer retain the same clearance above actual IME coordinates');
    assert.equal(elements(sheet.tree).find(n => n.props.testID === `${props.testID}-keyboard-boundary`)!.props.accessibilityViewIsModal, true);
    assert.deepEqual(calls, [], 'geometry measurement must not invoke any dialog operation');
  } finally {await sheet.host.unmount(); await surface.host.unmount(); await dialog.host.unmount(); state.theme = previousTheme; state.viewport = previousViewport;}
});

test('phone picker fits its continuous body to the authored action inset above the actual keyboard', async () => {
  const previousTheme = state.theme, previousViewport = state.viewport;
  state.theme = createTheme({isTV: false, width: 390, reduceMotion: true});
  state.viewport = {width: 390, height: 844, scale: 1, fontScale: 1};
  const wrapper = await mount(PlaylistPicker, {testID: 'phone.picker.flow', open: true, onOpenChange() {}, count: 4, entries: [], creating: true, onCreatingChange() {}, name: 'Actual name', onNameChange() {}, onCreateAndAdd() {}, onAdd() {}, query: '', onQueryChange() {}, onViewDestination() {}, onResultDismiss() {}});
  const picker = await mount((wrapper.tree as Element).type, (wrapper.tree as Element).props);
  const surface = await mount(SelectionSurface, ofType(picker.tree, SelectionSurface)[0]!.props);
  const sheet = await mount(BottomSheet, (surface.tree as Element).props);
  const panel = () => elements(sheet.tree).find(node => node.props.testID === 'phone.picker.flow')!;
  const header = () => ofType(sheet.tree, 'GestureDetector')[0]!.props.children;
  const body = () => elements(panel()).find(node => node.type === 'View' && node.props.onLayout)!;
  const footer = () => elements(sheet.tree).find(node => node.props.onLayout && flattenStyle(node.props.style).position === 'absolute' && flattenStyle(node.props.style).borderTopColor)!;
  const measure = async (footerHeight: number, bottom: number) => {
    header().props.onLayout({nativeEvent: {layout: {height: 53}}});
    body().props.onLayout({nativeEvent: {layout: {height: 75}}});
    footer().props.onLayout({nativeEvent: {layout: {height: footerHeight}}}); await sheet.host.rerender();
    const frame = flattenStyle(panel().props.style);
    const bodyBottom = bottom - frame.height + frame.borderTopWidth + frame.paddingTop + 53 + 75;
    assert.equal(bodyBottom, bottom - footerHeight, 'content ends at the footer; its twelve-point action inset supplies the drawn gap');
    assert.equal(flattenStyle(footer().props.style).paddingTop, 12);
    assert.equal(flattenStyle(footer().props.style).borderTopWidth, 0);
  };
  try {
    const input = ofType(picker.tree, TextField)[0]!;
    assert.equal(flattenStyle(elements(picker.tree).find(node => node.props.children === input)!.props.style).paddingBottom, 0);
    assert.equal(flattenStyle(elements(picker.tree).find(node => node.props.children === input)!.props.style).paddingTop, 4);
    assert.equal((surface.tree as Element).props.contentFooterGap, 0);
    await measure(94, 844);
    keyboardListeners.get('keyboardWillShow')!({height: 300, duration: 0}); await sheet.host.rerender();
    await measure(72, 544);
    assert.equal(flattenStyle(footer().props.style).paddingBottom, 12);
  } finally {await sheet.host.unmount(); await surface.host.unmount(); await picker.host.unmount(); await wrapper.host.unmount(); state.theme = previousTheme; state.viewport = previousViewport;}
});

test('phone Delete applies its authored context gap and text flow without changing other Selection footer recipes', async () => {
  const previous = state.theme;
  const ownerHeader = React.createElement('View', {testID: 'actual.owner.context'});
  const props: DeleteMediaDialogProps = {open: true, onOpenChange() {}, ownerHeader, owner: true, selectionCount: 1,
    preview: {revision: 7, confirmation: 'Actual title', confirmationKind: 'title', totalBytes: 0, totalFiles: 1, totalBytesText: '0 B', blocked: [], targets: [{itemId: 'a', title: 'Actual title', kind: 'movie', libraryId: 'one', libraryName: 'Actual library', allowMediaDeletion: true, titles: 1, bytes: 0, files: [], dependents: []}]},
    confirmation: '', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete() {}};
  try {
    for (const [isTV, width] of [[false, 390], [false, 820], [true, 1920]] as const) {
      state.theme = createTheme({isTV, width, textScale: 1.2});
      const phone = width === 390;
      const dialog = await mount(DeleteMediaDialog, props);
      const ordinary = await mount(SelectionSurface, {open: true, onOpenChange() {}, title: 'Actual selection surface', children: null});
      const surface = await mount(SelectionSurface, (dialog.tree as Element).props);
      try {
        const deletion = dialog.tree as Element, sheet = surface.tree as Element;
        assert.deepEqual((ordinary.tree as Element).props.footerPadding, isTV ? undefined : {top: 10, bottom: 30, keyboardBottom: 12});
        assert.equal((ordinary.tree as Element).props.showFooterSeparator, undefined, 'ordinary Selection retains the BottomSheet divider default');
        assert.deepEqual(sheet.props.footerPadding, phone ? {top: 0, bottom: 34, keyboardBottom: 12} : isTV ? undefined : {top: 10, bottom: 30, keyboardBottom: 12});
        assert.equal(sheet.props.showFooterSeparator, phone ? false : undefined);
        if (phone) {
          assert.equal(deletion.props.headerContext.props.style.paddingBottom, 10);
          assert.equal(deletion.props.headerContext.props.children, ownerHeader, 'the actual caller-owned identity remains intact');
          assert.equal(deletion.props.titleTextStyle.includeFontPadding, false);
        } else assert.equal(deletion.props.headerContext, ownerHeader);
        const facts = ofType(dialog.tree, SelectionText).find(node => text(node).includes('0 B'))!;
        const copy = await mount(SelectionText, facts.props);
        try {
          assert.equal((copy.tree as Element).props.style[0].fontFamily, fontFamilies[phone ? 400 : 500]);
          assert.equal(facts.props.style.lineHeight, isTV ? undefined : phone ? 25.2 : 21);
          if (phone) assert.equal(facts.props.style.color, state.theme.colors.textSecondary);
          assert.equal(ofType(facts, SelectionText)[1].props.weight, 700, 'actual file facts retain their drawn emphasis');
        } finally {await copy.host.unmount();}
        if (phone) {
          const help = ofType(dialog.tree, SelectionText).find(node => text(node).startsWith('The files are deleted'))!;
          assert.equal(help.props.weight, 400); assert.equal(help.props.style.lineHeight, 18 * state.theme.textScale);
        }
      } finally {await surface.host.unmount(); await ordinary.host.unmount(); await dialog.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('fitted footer accounting preserves no-footer, explicit detent, long-content and side-panel behavior', async () => {
  const previousTheme = state.theme, previousViewport = state.viewport;
  state.viewport = {width: 390, height: 844, scale: 1, fontScale: 1};
  const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
  try {
    for (const options of [{fitContent: true}, {fitContent: false, detents: [500]}, {fitContent: true, long: true}, {fitContent: true, side: 'tv'}, {fitContent: true, side: 'tablet'}]) {
      state.theme = createTheme({isTV: options.side === 'tv', width: options.side === 'tv' ? 1920 : options.side ? 820 : 390, reduceMotion: true});
      const props = {testID: 'accounting.regression', open: true, onOpenChange() {}, title: 'Actual content', children: null, showClose: false,
        fitContent: options.fitContent, detents: options.detents, footer: options.detents || options.long || options.side ? React.createElement('Button') : undefined};
      const sheet = await mount(BottomSheet, props);
      try {
        const scroll = () => ofType(sheet.tree, 'ScrollView')[0]!;
        if (!options.side) ofType(sheet.tree, 'GestureDetector')[0]!.props.children.props.onLayout({nativeEvent: {layout: {height: 86}}});
        const footer = elements(sheet.tree).find(n => n.props.onLayout && flatten(n.props.style).position === 'absolute' && flatten(n.props.style).borderTopColor);
        footer?.props.onLayout({nativeEvent: {layout: {height: 87}}}); await sheet.host.rerender();
        scroll().props.onContentSizeChange(350, (options.long ? 1200 : 125) + flatten(scroll().props.contentContainerStyle).paddingBottom); await sheet.host.rerender();
        if (options.side) {
          const side = elements(sheet.tree).find(n => flatten(n.props.style).width === (options.side === 'tv' ? 640 : 440))!;
          assert.equal(flatten(side.props.style).top, 0); assert.equal(flatten(side.props.style).bottom, 0);
          assert.equal(ofType(sheet.tree, FocusBoundary)[0]!.props.modal, true);
        } else {
          const style = flatten(elements(sheet.tree).find(n => n.props.testID === props.testID)!.props.style);
          assert.equal(style.height, options.long ? 820 : options.detents ? 500 : 219);
          if (options.long) assert.ok(flatten(scroll().props.contentContainerStyle).paddingBottom >= 87 + 16, 'clipped long content keeps its scrollable footer clearance');
        }
      } finally {await sheet.host.unmount();}
    }
  } finally {state.theme = previousTheme; state.viewport = previousViewport;}
});

const flattenStyle = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flattenStyle)) : style ?? {};

test('SidePanel can omit its standard close action while retaining supplied content, geometry and escape', async () => {
  const previousTheme = state.theme;
  state.theme = createTheme({isTV: true, width: 1920});
  const calls: unknown[] = [];
  const context = React.createElement('View', {testID: 'actual-context'});
  const children = React.createElement('View', {testID: 'actual-rows'});
  const footer = React.createElement('View', {testID: 'actual-footer'});
  const props = {open: true, title: 'Actual customization', headerContext: context, children, footer, width: 860, topInset: 0,
    initialFocusRef: {current: null}, returnFocusRef: {current: null}, onOpenChange: (...args: unknown[]) => calls.push(args), testID: 'actual-side'};
  const panel = await mount(SidePanel, props);
  try {
    const heading = () => ofType(panel.tree, SurfaceHeader)[0]!;
    // The drawn panel paints inside its ancestor paint provider, so the portal that
    // carries the measured rect and outside-press handler is its own element.
    const portal = () => elements(panel.tree).find(element => element.type === OverlayPortal)!;
    assert.equal((heading().props.action as Element).type, Button);
    await panel.update({...props, showClose: false});
    assert.equal(heading().props.action, undefined);
    assert.equal(portal().props.rect.width, 860);
    assert.equal(portal().props.rect.y, 0);
    assert.equal(portal().props.onOutsidePress, undefined);
    assert.equal(ofType(panel.tree, FocusBoundary)[0]!.props.modal, true);
    for (const node of [context, children, footer]) assert.ok(elements(panel.tree).includes(node));
    const host = () => elements(panel.tree).find(element => element.props.testID === props.testID)!;
    host().props.onAccessibilityEscape();
    assert.deepEqual(calls, [[false, 'escape']]);
    await panel.update({...props, showClose: false, dismissOnBack: false});
    host().props.onAccessibilityEscape();
    assert.equal(calls.length, 1);
    await panel.update(props);
    (heading().props.action as Element).props.onPress();
    assert.deepEqual(calls.at(-1), [false, 'close-button']);
  } finally {await panel.host.unmount(); state.theme = previousTheme;}
});

test('an authored TV side-panel canvas scales inside its physical portal without replacing slide or focus lifecycle', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const previousTheme = state.theme, previousViewport = state.viewport;
  state.theme = createTheme({isTV: true, width: 1920});
  state.viewport = {width: 1920, height: 1080, scale: 1, fontScale: 1};
  const provider = await mount(OverlaysProvider, {children: null});
  const contextElement = (provider.tree as Element).props.children as Element;
  const context = contextElement.type as any, previousContext = context._currentValue2;
  context._currentValue2 = {...contextElement.props.value, frame: {x: 10, y: 30, width: 1280, height: 720}};
  const calls: unknown[] = [], focused: string[] = [];
  const child = React.createElement('View', {testID: 'actual-layout-rows'}), footer = React.createElement('View', {testID: 'actual-layout-footer'});
  const props = {open: true, title: 'Actual customization', children: child, footer, width: 860, topInset: 0, showClose: false,
    logicalCanvas: {width: 860, height: 1080}, style: {paddingTop: 60, paddingLeft: 60}, testID: 'actual-logical-panel',
    initialFocusRef: {current: {requestTVFocus() {focused.push('initial');}}}, returnFocusRef: {current: {requestTVFocus() {focused.push('return');}}},
    onOpenChange: (...args: unknown[]) => calls.push(args)};
  const panel = await mount(SidePanel, props);
  const portalRect = () => (elements(panel.tree).find(element => element.type === OverlayPortal)!.props.rect);
  const outer = () => elements(panel.tree).find(node => node.props.testID === props.testID)!;
  // The authored 860 × 1080 canvas is the only absolutely placed View carrying both dimensions.
  const authored = () => elements(panel.tree).find(node => {
    const style = flattenStyle(node.props.style);
    return style.position === 'absolute' && style.width === 860 && style.height === 1080;
  });
  try {
    const physicalWidth = 860 * (720 / 1080);
    const expected = {x: 10 + 1280 - physicalWidth, y: 30, width: physicalWidth, height: 720};
    // The source multiplies the authored canvas through its real frame scale, so the
    // same geometry arrives by a different float order; compare at drawn precision.
    const drawn = (value: number) => Number(value.toFixed(9));
    const rect = portalRect();
    assert.deepEqual({...rect, x: drawn(rect.x)}, {...expected, x: drawn(expected.x)});
    // The portal carries the physical frame; the panel itself stays on the authored canvas
    // and is scaled once by its real TVDesignCanvas owner.
    assert.equal(ofType(panel.tree, TVDesignCanvas).length, 1);
    assert.equal(flattenStyle(outer().props.style).width, 860);
    assert.deepEqual(flattenStyle(outer().props.style).transform, [{translateX: 860}], 'opening begins outside the authored canvas');
    assert.equal(authored()!.type, 'View');
    assert.equal(flattenStyle(authored()!.props.style).paddingTop, 60); assert.equal(flattenStyle(authored()!.props.style).paddingLeft, 60);
    assert.equal(ofType(authored(), FocusBoundary)[0].props.modal, true);
    assert.ok(elements(authored()).includes(child)); assert.ok(elements(authored()).includes(footer));
    assert.equal(ofType(panel.tree, SurfaceHeader)[0].props.action, undefined);
    t.mock.timers.tick(320); await panel.host.rerender();
    assert.deepEqual(focused, ['initial']); assert.deepEqual(flattenStyle(outer().props.style).transform, [{translateX: 0}]);
    outer().props.onAccessibilityEscape(); assert.deepEqual(calls, [[false, 'escape']]);
    context._currentValue2 = {...context._currentValue2, frame: {x: 0, y: 0, width: 1920, height: 1080}};
    await panel.host.rerender();
    assert.equal(portalRect().width, 860); assert.equal(portalRect().height, 1080);
    await panel.update({...props, logicalCanvas: undefined});
    assert.equal(authored(), undefined, 'ordinary panels retain the existing unscaled composition');
    assert.equal(flattenStyle(outer().props.style).paddingTop, 60);
    await panel.update({...props, open: false}); await panel.host.rerender();
    assert.deepEqual(flattenStyle(outer().props.style).transform, [{translateX: 860}], 'closing translates the panel while its canvas owner keeps the separate scale');
  } finally {
    await panel.host.unmount(); await provider.host.unmount(); context._currentValue2 = previousContext; state.theme = previousTheme; state.viewport = previousViewport;
  }
  assert.deepEqual(focused, ['initial', 'return']);
});

test('a supplied BottomSheet heading replaces the standard row while retaining context and measured sheet anatomy', async () => {
  const previousTheme = state.theme;
  const context = React.createElement('View', {testID: 'actual-owner-context'});
  const custom = React.createElement('View', {testID: 'actual-member-header'});
  const props = {open: true, title: 'Actual member', description: 'Actual kind and year', onOpenChange() {}, headerContext: context, children: React.createElement('View', {testID: 'actual-menu-rows'}), fitContent: true, testID: 'actual-sheet'};
  try {
    for (const screen of [{isTV: false, width: 390}, {isTV: false, width: 900}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(screen);
      const sheet = await mount(BottomSheet, props);
      try {
        assert.equal(ofType(sheet.tree, SurfaceHeader).length, 1);
        await sheet.update({...props, headerContent: custom});
        assert.equal(ofType(sheet.tree, SurfaceHeader).length, 0);
        assert.equal(elements(sheet.tree).filter(element => element === custom).length, 1);
        assert.equal(elements(sheet.tree).filter(element => element === context).length, 1);
        assert.ok(ofType(sheet.tree, 'ScrollView').length);
        if (state.theme.platform === 'phone') {
          const header = ofType(sheet.tree, 'GestureDetector')[0]!.props.children;
          assert.equal(typeof header.props.onLayout, 'function');
          header.props.onLayout({nativeEvent: {layout: {height: 110}}});
          await sheet.host.rerender();
          const grabber = elements(header).find(element => element.props.accessibilityRole === 'adjustable')!;
          assert.ok(grabber.props.accessibilityActions.some((action: {name: string}) => action.name === 'dismiss'));
        } else assert.equal(ofType(sheet.tree, FocusBoundary)[0]!.props.modal, true);
        await sheet.update({...props, headerContent: null});
        assert.equal(ofType(sheet.tree, SurfaceHeader).length, 0);
        await sheet.update(props);
        assert.equal(ofType(sheet.tree, SurfaceHeader).length, 1);
      } finally {await sheet.host.unmount();}
    }
  } finally {state.theme = previousTheme;}
});

test('a BottomSheet composition can opt into its backdrop alpha without changing dismissal', async () => {
  const previousTheme = state.theme;
  try {
    for (const screen of [{isTV: false, width: 390}, {isTV: false, width: 1024}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(screen);
      const calls: unknown[] = [];
      const props = {open: true, title: 'Actual menu', children: null, onOpenChange: (...args: unknown[]) => calls.push(args)};
      const sheet = await mount(BottomSheet, props);
      try {
        const scrim = () => elements(sheet.tree).find(element => element.props.pointerEvents === 'none' && flattenStyle(element.props.style).backgroundColor)!;
        const color = state.theme.isTV ? state.theme.colors.ink : '#03080c';
        assert.equal(flattenStyle(scrim().props.style).backgroundColor, `${color}a3`);
        await sheet.update({...props, backdropOpacity: 0.58});
        assert.equal(flattenStyle(scrim().props.style).backgroundColor, `${color}94`);
        const outside = elements(sheet.tree).find(element => element.props.onResponderRelease)!;
        assert.equal(outside.props.accessible, false);
        outside.props.onResponderRelease();
        ofType(sheet.tree, 'Modal')[0]!.props.onRequestClose();
        assert.deepEqual(calls, [[false, 'outside'], [false, 'back']]);
        await sheet.update(props);
        assert.equal(flattenStyle(scrim().props.style).backgroundColor, `${color}a3`);
      } finally {await sheet.host.unmount();}
    }
  } finally {state.theme = previousTheme;}
});

async function withNativeControl(element: Element, inspect: (host: Element) => void) {
  const mounted: Awaited<ReturnType<typeof mount>>[] = [];
  try {
    let current = element;
    while (typeof current.type !== 'string') {
      const instance = await mount(current.type, current.props); mounted.push(instance); current = instance.tree as Element;
    }
    assert.equal(current.type, 'Pressable');
    inspect(current);
  } finally {for (const instance of mounted.reverse()) await instance.host.unmount();}
}

test('Selection footer recipes reach actual buttons without changing other size classes or losing identity', async () => {
  const previous = state.theme;
  try {
    for (const options of [{isTV: false, width: 390, textScale: 1.25}, {isTV: false, width: 820}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const instances: Awaited<ReturnType<typeof mount>>[] = [];
      try {
        instances.push(await mount(BulkEditor, bulk({testID: 'footer.bulk'})));
        instances.push(await mount(DeleteMediaDialog, {testID: 'footer.delete', open: true, onOpenChange() {}, ownerHeader: null, owner: true, selectionCount: 1, preview: null, confirmation: '', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete() {}}));
        instances.push(await mount(MixedEditExplainer, {testID: 'footer.mixed', open: true, onOpenChange() {}, total: 0, groups: [], description: 'Actual explanation', onKeepOnly() {}}));
        const wrapper = await mount(PlaylistPicker, {testID: 'footer.picker', open: true, onOpenChange() {}, count: 1, selectionLabel: 'Actual selection', entries: [], creating: true, onCreatingChange() {}, name: '', onNameChange() {}, onCreateAndAdd() {}, onAdd() {}, query: '', onQueryChange() {}, onRetry() {}, onViewDestination() {}, onResultDismiss() {}});
        instances.push(wrapper, await mount((wrapper.tree as Element).type, (wrapper.tree as Element).props));
        const phone = state.theme.platform === 'phone', tv = state.theme.isTV;
        for (const instance of instances.filter(item => item !== wrapper)) {
          const surface = ofType(instance.tree, SelectionSurface)[0]!;
          for (const button of ofType(surface.props.footer, Button)) await withNativeControl(button, host => {
            const style = flattenStyle(host.props.style({pressed: false}));
            assert.equal(host.props.testID, button.props.testID);
            assert.equal(host.props.accessibilityRole, 'button');
            assert.equal(!!host.props.disabled, !!button.props.disabled || !!button.props.loading);
            if (state.theme.textScale > 1) {
              assert.equal(style.height, undefined, 'enlarged labels can grow the footer button');
              assert.equal(style.minHeight, phone ? 48 : tv ? 64 : 46, 'the authored face remains the minimum');
            } else assert.equal(style.height, phone ? 48 : tv ? 64 : 46);
            assert.equal(style.paddingHorizontal, phone ? 20 : 26);
            assert.equal(style.borderRadius, phone ? 10 : tv ? 14 : 12);
            const content = host.props.children({pressed: false});
            const label = ofType(content, 'Text')[0]!;
            assert.equal(flattenStyle(label.props.style).fontSize, (phone ? 15.5 : tv ? 22 : 15) * state.theme.textScale);
            assert.equal(text(label), text(button.props.children));
          });
        }
      } finally {for (const instance of instances.reverse()) await instance.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('phone receipt rows stay contiguous while retaining every outcome and the failed-only retry', async () => {
  const previous = state.theme;
  try {
    for (const options of [{isTV: false, width: 390}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const calls: unknown[] = [];
      const editor = await mount(BulkEditor, bulk({result: {updated: 1, failed: 1, results: [{kind: 'item', id: 'a', ok: true}, {kind: 'item', id: 'b', ok: false, code: 'metadata_conflict'}]}, onRetry: ids => calls.push(ids)}));
      try {
        const rows = elements(editor.tree).find(node => Array.isArray(node.props.children) && node.props.children.length === 2 && node.props.children.every((child: any) => React.isValidElement<Record<string, any>>(child) && flattenStyle(child.props.style).paddingVertical === 9))!;
        assert.ok(rows);
        assert.equal(flattenStyle(rows.props.style).gap, state.theme.isTV ? 6 : 0);
        assert.match(text(rows), /Actual saved title/); assert.match(text(rows), /Actual failed title/);
        assert.match(text(rows), /Someone else changed this title/);
        ofType((editor.tree as Element).props.footer, Button).at(-1)!.props.onPress();
        assert.deepEqual(calls, [['b']]);
      } finally {await editor.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('phone Delete separates full-width helper copy while preserving the real switch and confirmation gate', async () => {
  const previous = state.theme; state.theme = createTheme({isTV: false, width: 390});
  const calls: unknown[] = [];
  const preview = {revision: 7, confirmation: 'Actual title', confirmationKind: 'title' as const, totalBytes: 0, totalFiles: 1, totalBytesText: '0 B', blocked: [], targets: [{itemId: 'a', title: 'Actual title', kind: 'movie', libraryId: 'one', libraryName: 'Actual library', allowMediaDeletion: true, titles: 1, bytes: 0, files: [], dependents: []}]};
  const props: DeleteMediaDialogProps = {testID: 'phone.delete.helper', open: true, onOpenChange() {}, ownerHeader: null, owner: true, selectionCount: 1, preview, confirmation: '', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange: value => calls.push(value), onDelete: (...args) => calls.push(args)};
  const dialog = await mount(DeleteMediaDialog, props);
  try {
    const toggle = ofType(dialog.tree, Switch)[0]!;
    assert.equal(toggle.props.description, undefined);
    const help = ofType(dialog.tree, SelectionText).find(node => node.props.size === 12 && text(node) === toggle.props.accessibilityHint)!;
    assert.ok(help, 'helper remains outside the switch copy column');
    assert.equal(toggle.props.accessibilityLabel, text(toggle.props.children));
    assert.equal(toggle.props.style.minHeight, 26); assert.equal(toggle.props.style.paddingVertical, 0);
    assert.equal(flattenStyle((dialog.tree as Element).props.footer.props.style).paddingHorizontal, 4);
    const control = await mount(Switch, toggle.props);
    try {await withNativeControl(control.tree as Element, host => {
      assert.equal(host.props.testID, controlID(props.testID, 'remove-files'));
      assert.equal(host.props.accessibilityRole, 'switch');
      assert.equal(host.props.accessibilityState.checked, true);
      assert.equal(host.props.accessibilityHint, text(help));
      host.props.onPress({});
    });} finally {await control.host.unmount();}
    assert.deepEqual(calls, [false]);
    let remove = ofType((dialog.tree as Element).props.footer, Button).at(-1)!;
    assert.equal(remove.props.disabled, true); remove.props.onPress(); assert.deepEqual(calls, [false]);
    await dialog.update({...props, confirmation: preview.confirmation, removeFiles: false});
    assert.match(text(dialog.tree), /The next scan finds the files again/);
    remove = ofType((dialog.tree as Element).props.footer, Button).at(-1)!;
    assert.equal(remove.props.disabled, false); remove.props.onPress(); assert.deepEqual(calls, [false, [preview, preview.confirmation, false]]);
    await dialog.update({...props, pending: true}); assert.equal(ofType(dialog.tree, Switch)[0]!.props.disabled, true);
    await dialog.update({...props, owner: false}); assert.equal(dialog.tree, null);
  } finally {await dialog.host.unmount(); state.theme = previous;}
});

test('phone multi-delete shows the drawn aggregate while retaining the complete preview and real count gate', async () => {
  const previous = state.theme;
  const targets = Array.from({length: 6}, (_, index) => ({itemId: `actual-${index}`, title: `Actual target ${index}`, kind: 'movie', libraryId: 'one', libraryName: 'Actual library', allowMediaDeletion: true, titles: 1, bytes: 1024, files: [], dependents: [{kind: 'playlist', count: 2, description: 'playlist entries'}, {kind: 'progress', count: 1, description: 'saved places'}]}));
  const preview: NonNullable<DeleteMediaDialogProps['preview']> = {revision: 7, confirmation: '6', confirmationKind: 'count', totalBytes: 6144, totalFiles: 6, totalBytesText: '6 KB', blocked: [], targets};
  try {
    for (const options of [{isTV: false, width: 390}, {isTV: false, width: 820}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const phone = state.theme.platform === 'phone', calls: unknown[] = [];
      const props: DeleteMediaDialogProps = {open: true, onOpenChange() {}, ownerHeader: null, owner: true, selectionCount: 6, preview, confirmation: '5', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete: (...args) => calls.push(args), renderArtwork: id => `Artwork ${id}`};
      const dialog = await mount(DeleteMediaDialog, props);
      try {
        const content = text(dialog.tree);
        assert.match(content, /6 files, 6 KB\./);
        if (phone) {
          assert.match(content, /Playlist and collection entries and saved places go too\./);
          assert.doesNotMatch(content, /Actual target|Artwork actual-|12 playlists/);
          assert.equal(ofType(dialog.tree, TextField)[0]!.props.keyboardType, 'default', 'the drawn phone count confirmation uses the native text keyboard');
          const toggle = ofType(dialog.tree, Switch)[0]!;
          assert.ok(toggle.props.accessibilityHint, 'the real removal consequence remains available to accessibility');
          assert.equal(ofType(dialog.tree, SelectionText).some(node => text(node) === toggle.props.accessibilityHint), false, 'the compact bulk frame omits the separate removal helper');
        } else {
          assert.equal(ofType(dialog.tree, TextField)[0]!.props.keyboardType, 'number-pad');
          assert.match(content, /Actual target 0/);
          assert.match(content, /Actual target 4/);
          assert.doesNotMatch(content, /Actual target 5/);
          assert.match(content, /and 1 more/);
          assert.match(content, /12 playlists/);
        }
        let remove = ofType((dialog.tree as Element).props.footer, Button).at(-1)!;
        assert.equal(remove.props.disabled, true); remove.props.onPress(); assert.deepEqual(calls, []);
        await dialog.update({...props, confirmation: preview.confirmation});
        remove = ofType((dialog.tree as Element).props.footer, Button).at(-1)!;
        assert.equal(remove.props.disabled, false); remove.props.onPress();
        assert.deepEqual(calls, [[preview, '6', true]], 'complete preview and all targets reach the existing guarded mutation');
        if (phone) {
          const other = {...preview, targets: [{...targets[0]!, dependents: [{kind: 'library-channel', count: 2, description: 'scheduled library channel slots'}]}, targets[1]!]};
          await dialog.update({...props, preview: other});
          assert.match(text(dialog.tree), /2 scheduled library channel slots/);
          assert.doesNotMatch(text(dialog.tree), /Playlist and collection entries and saved places go too/);
          const toggle = ofType(dialog.tree, Switch)[0]!;
          assert.ok(ofType(dialog.tree, SelectionText).some(node => text(node) === toggle.props.accessibilityHint), 'other dependency previews retain the existing helper');
        }
      } finally {await dialog.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('phone Delete result keeps the drawn warning insets and type while preserving receipts and close focus', async () => {
  const previous = state.theme;
  try {
    for (const options of [{isTV: false, width: 390, textScale: 1.25}, {isTV: false, width: 820}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const calls: unknown[] = [];
      const props: DeleteMediaDialogProps = {open: true, onOpenChange: (...args) => calls.push(args), ownerHeader: null, owner: true, selectionCount: 2, preview: null,
        result: {revision: 7, removed: 1, failed: 1, bytesDeleted: 0, filesRetained: true, receipts: [{itemId: 'removed', title: 'Actual removed title', removed: true, filesDeleted: 1, filesKept: 0, bytes: 0, code: ''}, {itemId: 'kept', title: 'Actual kept title', removed: false, filesDeleted: 0, filesKept: 1, bytes: 0, code: 'file_not_removed'}]},
        confirmation: '', onConfirmationChange() {}, removeFiles: true, onRemoveFilesChange() {}, onDelete: () => calls.push('delete')};
      const dialog = await mount(DeleteMediaDialog, props);
      try {
        const surface = dialog.tree as Element, phone = state.theme.platform === 'phone';
        const warning = elements(surface).find(node => node.key === 'kept')!;
        assert.ok(warning); assert.equal(elements(surface).some(node => node.key === 'removed'), false);
        const box = flattenStyle(warning.props.style);
        assert.equal(box.borderWidth, 1); assert.equal(box.gap, 10);
        assert.equal(box.paddingVertical ?? box.padding, phone ? 10 : 12);
        assert.equal(box.paddingHorizontal ?? box.padding, phone ? 11 : 12);
        const copy = elements(warning).find(node => flattenStyle(node.props.style).flex === 1)!;
        assert.equal(flattenStyle(copy.props.style).gap, phone ? 0 : 3);
        const [title, helper] = ofType(warning, SelectionText);
        assert.equal(text(title), 'Actual kept title'); assert.match(text(helper), /Kept, because the file could not be deleted/);
        const titleHost = await mount(SelectionText, title!.props), helperHost = await mount(SelectionText, helper!.props);
        try {
          const titleStyle = flattenStyle((titleHost.tree as Element).props.style), helperStyle = flattenStyle((helperHost.tree as Element).props.style);
          assert.equal(titleStyle.lineHeight, phone ? 20.3 * state.theme.textScale : undefined);
          assert.equal(helperStyle.lineHeight, phone ? 19.575 * state.theme.textScale : state.theme.isTV ? 29 : 19.575);
          assert.equal(helperStyle.color, phone ? state.theme.colors.textSecondary : state.theme.colors.textTertiary);
        } finally {await helperHost.host.unmount(); await titleHost.host.unmount();}
        const close = ofType(surface.props.footer, Button);
        assert.equal(close.length, 1); assert.equal(close[0]!.props.ref, surface.props.initialFocusRef);
        assert.equal(ofType(surface, Switch).length, 0); assert.equal(ofType(surface, TextField).length, 0);
        close[0]!.props.onPress(); assert.deepEqual(calls, [[false, 'action']]);
      } finally {await dialog.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('phone Search uses the pinned heading and helper line boxes without changing other StateView recipes', async () => {
  const previous = state.theme;
  try {
    for (const options of [{isTV: false, width: 390, textScale: 1.25}, {isTV: false, width: 820}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const view = await mount(StateView, {variant: 'search', title: 'Actual no matches', body: 'Actual query explanation', icon: 'i-clock'});
      try {
        const [title, body] = ofType(view.tree, 'Text'), phone = state.theme.platform === 'phone', tv = state.theme.isTV, scale = state.theme.textScale;
        const heading = flattenStyle(title!.props.style), helper = flattenStyle(body!.props.style);
        assert.equal(heading.fontSize, (phone ? 24 : tv ? 44 : 22) * scale);
        assert.ok(Math.abs(heading.lineHeight - (phone ? 27.6 : tv ? 47.52 : 26.4) * scale) < .0001);
        assert.equal(helper.fontSize, (phone ? 14.5 : tv ? 24 : 14) * scale);
        if (phone) {
          assert.equal(helper.lineHeight, 21.75 * scale); assert.equal(helper.marginBottom, 0);
          assert.equal(heading.includeFontPadding, false); assert.equal(helper.includeFontPadding, false);
          assert.equal(heading.marginBottom, 6); assert.equal(heading.textAlign, 'left');
        } else {assert.equal(heading.includeFontPadding, undefined); assert.equal(helper.includeFontPadding, undefined);}
        assert.match(text(view.tree), /Actual no matches/); assert.match(text(view.tree), /Actual query explanation/);
        assert.equal(ofType(view.tree, Icon).length, tv ? 1 : 0);
      } finally {await view.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('partial picker receipts show the actual count without an undrawn check and retry only failures', async () => {
  const previous = state.theme;
  try {
    for (const options of [{isTV: false, width: 390}, {isTV: false, width: 820}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      const calls: unknown[] = [];
      const props = {open: true, onOpenChange: (...args: unknown[]) => calls.push(args), count: 4, selectionLabel: 'Actual selection', entries: [], creating: false, onCreatingChange() {}, name: '', onNameChange() {}, onCreateAndAdd() {}, onAdd: () => calls.push('add'), query: '', onQueryChange() {},
        result: {destination: {id: 'actual-playlist', title: 'Actual playlist', size: 15}, inserted: 3, failed: 1, total: 4, failures: [{id: 'actual-failure', title: 'Actual failed title'}], complete: true},
        onRetry: (ids: readonly string[]) => calls.push(ids), onViewDestination() {}, onResultDismiss() {}};
      const wrapper = await mount(PlaylistPicker, props);
      const picker = await mount((wrapper.tree as Element).type, (wrapper.tree as Element).props);
      try {
        const surface = ofType(picker.tree, SelectionSurface)[0]!;
        assert.match(text(surface), /Actual playlist/); assert.match(text(surface), /15 titles/);
        assert.ok(ofType(surface, SelectionText).some(node => text(node) === '3 added'));
        assert.equal(ofType(surface, Icon).filter(node => node.props.name === 'i-check').length, 0);
        assert.equal(ofType(surface, Icon).filter(node => node.props.name === 'i-warn').length, 1);
        const retry = ofType(surface.props.footer, Button).at(-1)!;
        assert.equal(surface.props.initialFocusRef, retry.props.ref);
        retry.props.onPress(); assert.deepEqual(calls, [['actual-failure']]);
        ofType(surface.props.footer, Button)[0]!.props.onPress();
        assert.deepEqual(calls, [['actual-failure'], [false, 'action']]);
      } finally {await picker.host.unmount(); await wrapper.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('drawn list-field prompts use singular catalogue copy and retain real validated edits', async () => {
  const previous = state.theme;
  try {
    for (const options of [{isTV: false, width: 390}, {isTV: false, width: 820}, {isTV: true, width: 1920}]) {
      state.theme = createTheme(options);
      for (const fieldName of ['genres', 'tags', 'labels']) for (const empty of [false, true]) {
        const calls: unknown[] = [];
        const field = {field: fieldName, label: fieldName, group: 'general' as const, type: 'list' as const, maxLength: 128, max: 64, bulk: true};
        const editor = await mount(BulkEditor, bulk({schema: [field], values: {[fieldName]: {state: 'mixed', distinctCount: empty ? 0 : 1, truncated: false, values: [], entries: empty ? [] : [{value: 'Existing', count: 4}]}}, changes: {}, onChangesChange: changes => calls.push(changes)}));
        const listNode = elements(editor.tree).find(node => node.props.field?.field === fieldName)!;
        const list = await mount(listNode.type, listNode.props);
        try {
          const input = ofType(list.tree, TextField)[0]!;
          const expected = fieldName === 'genres' ? 'Add a genre' : fieldName === 'tags' ? empty ? 'None yet. Add a tag' : 'Add a tag' : empty ? 'None yet. Add a label' : 'Add a label';
          assert.equal(input.props.placeholder, expected); assert.equal(input.props.accessibilityLabel, expected);
          input.props.onChangeText('Fresh');
          await list.host.rerender();
          ofType(list.tree, TextField)[0]!.props.onSubmitEditing();
          assert.deepEqual(calls, [{[fieldName]: {add: ['Fresh'], remove: []}}]);
        } finally {await list.host.unmount(); await editor.host.unmount();}
      }
    }
  } finally {state.theme = previous;}
});

// The actual People panel, state presenter and native controls are materialised
// through the same element/hook harness used by the other TV focus regressions.
test('TV People retains its focused continuation host and renders the real scoped retry action', async () => {
  const {TVPeoplePanel} = await import('../src-next/screens/settings-live/server/TVPeoplePanel.tsx');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime.ts');
  const {NativeScreenServices} = await import('../src-next/screens/screen-services.ts');
  const {act} = await import('./helpers/fabric-hooks.mjs');
  const authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'fence'};
  const owner = {authority: () => authority, current: () => true, subscribeAuthority: () => () => {}, navigation: {open() {}}};
  const lifetime = new NativeScreenLifetime(owner), feedback = new NativeScreenServices(owner as any, async () => 'id');
  const account = {canManage: true, account: {role: 'admin', primaryProfileId: 'primary', disabled: false}};
  const device = (id: string) => ({id, accountId: 'account', profileId: 'primary', username: 'a', name: id, platform: 'tv', app: '', appVersion: '', lastNetwork: '', liveState: 'idle', firstSeenAt: '2026-01-01T00:00:00Z', lastSeenAt: '2026-01-01T00:00:00Z', revision: 1});
  let rejectPage!: (error: Error) => void, resolvePage!: (page: unknown) => void;
  const requests: string[] = [];
  const ports = {context: {account, api: {request: async (path: string) => {
    requests.push(path);
    if (path.includes('/members')) return {items: [], limit: 50, nextCursor: ''};
    if (path.includes('/invitations')) return {items: [], limit: 50, nextCursor: '', totalCount: 0, matchedCount: 0};
    if (!path.includes('cursor=')) return {items: [device('first')], limit: 50, nextCursor: 'offered'};
    return new Promise((resolve, reject) => {resolvePage = resolve; rejectPage = reject;});
  }}}};
  const runtime = {text: {t: (id: string) => id, locale: 'en-US'}, error: feedback.error};
  const panel = await mount(TVPeoplePanel, {ports, runtime, lifetime});
  const focus = () => ofType(panel.tree, Focusable).find(element => element.props.testID === 'tv-people-devices-more')!;
  try {
    await act(() => {});
    const before = focus(); assert.ok(before); assert.equal(before.props.disabled, false);
    let nativeId: string | undefined;
    await withNativeControl(before, host => {nativeId = host.props.testID; host.props.onFocus({nativeEvent: {}}); host.props.onPress();});
    await act(() => {});
    const pending = focus(); assert.ok(pending); assert.equal(pending.key, before.key); assert.equal(pending.props.disabled, true); assert.equal(pending.props.focusableWhenDisabled, true);
    await withNativeControl(pending, host => {assert.equal(host.props.testID, nativeId); assert.notEqual(host.props.focusable, false); assert.equal(host.props.onPress, undefined);});
    await act(() => rejectPage(new Error('offline')));
    const failed = focus(); assert.ok(failed); assert.equal(failed.key, before.key); assert.equal(failed.props.disabled, false);
    assert.ok(ofType(panel.tree, Focusable).find(element => element.props.focusId === 'tv-people:device:first'));
    const error = ofType(panel.tree, StateView).find(element => element.props.testID === 'tv-people-devices-error')!;
    assert.ok(error.props.primaryAction); const errorView = await mount(StateView, error.props);
    try {
      const action = elements(errorView.tree).find(element => element.props.action?.id === 'retry')!;
      assert.ok(action); await withNativeControl(action, host => {assert.ok(host.props.testID?.includes('tv-people-devices-error')); host.props.onPress();});
      await act(() => {}); assert.equal(focus().props.disabled, true);
      await act(() => resolvePage({items: [device('last')], limit: 50, nextCursor: ''}));
      assert.ok(ofType(panel.tree, Focusable).find(element => element.props.focusId === 'tv-people:device:first'));
      assert.ok(ofType(panel.tree, Focusable).find(element => element.props.focusId === 'tv-people:device:last'));
      assert.deepEqual(requests.filter(path => path.includes('/devices')).map(path => new URLSearchParams(path.split('?')[1]).get('cursor')), [null, 'offered', 'offered']);
      // A saved callback from the prior page cannot start a successor request.
      before.props.onPress(); assert.equal(requests.filter(path => path.includes('/devices')).length, 3);
    } finally {await errorView.host.unmount();}
  } finally {await panel.host.unmount(); feedback.dispose(); lifetime.dispose();}
});

for (const role of ['administrator', 'owner'] as const) test(`TV Dashboard ${role} presenter binds continuation and retry to the captured section`, async () => {
  const {screenModule} = await import('./helpers/screen-harness.mjs');
  const {act} = await import('./helpers/fabric-hooks.mjs');
  const {NativeDashboardController} = await import('../src-next/screens/settings-live/dashboard-controller.ts');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime.ts');
  const {NativeScreenServices} = await import('../src-next/screens/screen-services.ts');
  const {defaultI18n} = await import('../../../packages/i18n/src/index.ts');
  const dashboard = await screenModule(new URL('../src-next/screens/settings-live/DashboardScreen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': await import('react-native'),
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/server-admin/dashboard': await import('../../../packages/client-core/src/server-admin/dashboard.ts'),
    '../../../../../packages/client-core/src/presentation/content': await import('../../../packages/client-core/src/presentation/content.ts'),
    '../../../../../packages/client-core/src/server-admin/console-catalogue': await import('../../../packages/client-core/src/server-admin/console-catalogue.ts'),
    '../../../../../packages/client-core/src/server-admin/now-playing': await import('../../../packages/client-core/src/server-admin/now-playing.ts'),
    '../../../../../packages/client-core/src/presentation/kit-model': await import('../../../packages/client-core/src/presentation/kit-model/index.ts'),
    '../../kit/controls': {Button, Focusable}, '../../kit/focus': await import('../src-next/kit/focus/index.ts'),
    '../../kit/shell': await import('../src-next/kit/shell/index.ts'), '../../kit/states': {StateView},
    '../../kit/shell/visual': await import('../src-next/kit/shell/visual.ts'), '../../kit/theme': {useTheme: () => state.theme},
    '../../kit/cards/model': cardModel, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, './server/TVServerPageMenu': {TVServerPageMenu: 'TVServerPageMenu'},
    './server/TVDiskUsage': {TVDiskUsage: 'TVDiskUsage'}, '../../shell/tv-focus': await import('../src-next/shell/tv-focus.ts'),
    './Screen': {...await import('../src-next/screens/settings-live/Screen.tsx'), useScreenEntry: () => () => {}},
    './dashboard-presentation': await import('../src-next/screens/settings-live/dashboard-presentation.ts'),
    './owner-dashboard/use-owner-dashboard': {NativeOwnerDashboardPhone: 'NativeOwnerDashboardPhone'},
    '../../platform/event-resync': await import('../src-next/platform/event-resync.ts'),
  });
  const authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'fence'};
  const owner = {authority: () => authority, current: () => true, subscribeAuthority: () => () => {}, navigation: {open() {}}};
  const lifetime = new NativeScreenLifetime(owner), feedback = new NativeScreenServices(owner as any, async () => 'id');
  const totals = {streams: 11, paused: 0, converting: 0, bandwidthKbps: 0, people: 11, atHome: 11, away: 0};
  const session = (id: string) => ({id, user: 'person', profile: 'profile', device: 'device', title: id, kind: 'vod', state: 'playing', positionMs: 0, decision: {}, location: 'local'});
  let reject!: (error: Error) => void, complete!: (value: any) => void;
  const calls: (string | undefined)[] = [], jobCalls: (string | undefined)[] = [];
  const controller = new NativeDashboardController(lifetime, () => role, {
    sessions: {list: async cursor => {calls.push(cursor); return cursor ? new Promise((resolve, fail) => {complete = resolve; reject = fail;}) : {items: [session('first'), ...Array.from({length: 9}, (_, i) => session(`first${i + 1}`))] as never[], nextCursor: 'next', totals, revision: 'rev-1', scope: {serverId: 'server', viewerFence: 'fence'}};}},
    console: {measurement: async name => ({name, observedAt: 1, freshUntil: 30001, facts: {}}), alerts: async () => [], jobs: async cursor => {jobCalls.push(cursor); return {items: [{id: cursor ? 'later' : 'first', kind: 'scan', revision: 1}] as never[], nextCursor: cursor ? '' : 'job-next'};}, dispose() {}},
  });
  const runtime = {text: {t: defaultI18n.t, locale: 'en-US'}, error: feedback.error, artwork: () => undefined};
  let sessionEvent: (() => void) | undefined, resyncEvent: (() => void) | undefined;
  const ports = {serverName: () => 'Server', context: {events: {on: (name: string, listener: () => void) => {if (name === 'admin.sessions') sessionEvent = listener; return () => {sessionEvent = undefined;};}, onResync: (listener: () => void) => {resyncEvent = listener; return () => {resyncEvent = undefined;};}}, navigation: {open() {}, back() {}}}};
  const panel = await mount(dashboard.NativeDashboardScreen, {controller, ports, runtime, deniedFallback: null});
  try {
    await act(() => {});
    const row = ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-session-first')!;
    assert.ok(row);
    const identities = ofType(row, 'Text').flatMap(element => React.Children.toArray(element.props.children)).filter(value => ['person', 'profile', 'device'].includes(String(value)));
    assert.deepEqual(identities, ['person', 'profile', 'device'], 'TV identity leads with username, then a differing profile, then device');
    if (role === 'owner') {
      const jobMore = ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-jobs-more')!; assert.ok(jobMore);
      await withNativeControl(jobMore, host => host.props.onPress()); await act(() => {});
      assert.ok(ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-job-later'));
      // ConsoleClient.jobs takes the first-page cursor as '' (its own default), not undefined.
      assert.deepEqual(jobCalls, ['', 'job-next']);
    } else assert.deepEqual(jobCalls, []);
    const initialMore = ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-sessions-more')!;
    await withNativeControl(initialMore, host => host.props.onPress()); await act(() => {});
    await withNativeControl(row, host => host.props.onFocus({nativeEvent: {}}));
    assert.ok(controller.sessionFocusToken('first'), 'the original drawn handler can lease its resident row while pending');
    let more = ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-sessions-more')!;
    assert.ok(more); assert.equal(more.props.disabled, true); assert.equal(more.props.focusableWhenDisabled, true);
    await withNativeControl(more, host => {assert.equal(host.props.onPress, undefined); assert.notEqual(host.props.focusable, false);});
    await act(() => reject(new Error('offline')));
    assert.ok(ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-session-first'));
    const error = ofType(panel.tree, StateView).find(element => element.props.testID === 'dashboard-sessions-page-error')!;
    const errorView = await mount(StateView, error.props);
    try {
      const retry = elements(errorView.tree).find(element => element.props.action?.id === 'retry')!;
      await withNativeControl(retry, host => {assert.ok(host.props.testID?.includes('dashboard-sessions-page-error')); host.props.onPress();});
      await act(() => {}); more = ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-sessions-more')!;
      assert.equal(more.props.disabled, true);
      await act(() => complete({items: [session('last')], nextCursor: undefined, totals, revision: 'rev-1', scope: {serverId: 'server', viewerFence: 'fence'}}));
      assert.ok(ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-session-last'));
      assert.equal(controller.getSnapshot().sessions.length, 11);
      assert.ok(sessionEvent); sessionEvent!(); await act(() => {});
      assert.ok(controller.sessionFocusToken('first')); assert.equal(controller.getSnapshot().sessionWindow?.pending, true);
      await act(() => complete({items: [session('last')], nextCursor: undefined, totals, revision: 'rev-1', scope: {serverId: 'server', viewerFence: 'fence'}}));
      assert.ok(ofType(panel.tree, Focusable).find(element => element.props.testID === 'dashboard-session-last'));
      await withNativeControl(row, host => host.props.onBlur({nativeEvent: {}})); assert.equal(controller.sessionFocusToken('first'), undefined);
      assert.deepEqual(calls, [undefined, 'next', 'next', undefined, 'next']);
      // Missed admin.sessions hints (a resync) are recovered by reading the Dashboard as it is now.
      assert.ok(resyncEvent); const before = calls.length; resyncEvent!(); await act(() => {});
      assert.ok(calls.length > before && calls[before] === undefined, 'a resync rereads the sessions from their first page');
    } finally {await errorView.host.unmount();}
  } finally {await panel.host.unmount(); controller.dispose(); feedback.dispose(); lifetime.dispose();}
});

test('TV whole-query unavailable Delete and Download stay visible with reasons and cannot execute', async () => {
  const previous = state.theme; state.theme = createTheme({isTV: true, width: 1920});
  const calls: string[] = [], deletion = 'Delete at most 200 titles at once. Select fewer, or narrow the view with a filter first.', download = 'This isn’t available on your server right now.';
  const props: SelectionBarProps = {count: 201, selectionLabel: '201 movies', onCancel() {}, menu: 'more', onMenuChange() {}, onAction: id => calls.push(id), actions: [
    {id: 'more'}, {id: 'collection'},
    {id: 'download', disabled: true, wholeQueryUnavailable: true, reason: download},
    {id: 'delete', disabled: true, wholeQueryUnavailable: true, reason: deletion},
    {id: 'edit', disabled: true, wholeQueryUnavailable: true, reason: 'Unrelated disabled owner action'}, {id: 'refresh'},
  ]};
  const bar = await mount(SelectionBar, props);
  try {
    const node = elements(bar.tree).find(element => element.props.menu === 'more' && element.type !== SelectionBar)!;
    const menu = await mount(node.type, node.props);
    try {
      const element = menu.tree as Element; assert.equal(element.type, Menu); assert.equal(element.props.presentation, 'sideSheet');
      const rows = element.props.items.filter((entry: any) => entry.onSelect);
      assert.deepEqual(rows.map((entry: any) => entry.id), ['collection', 'download', 'delete']);
      for (const [id, reason] of [['download', download], ['delete', deletion]]) {
        const row = rows.find((entry: any) => entry.id === id); assert.ok(row); assert.equal(row.disabled, true); assert.equal(row.description, reason); row.onSelect();
        const rendered = await mount(MenuRow, {...row, onPress: row.onSelect});
        try {const control = ofType(rendered.tree, Focusable)[0]!; assert.equal(control.props.disabled, true); assert.equal(control.props.accessibilityState.disabled, true); assert.ok(text(rendered.tree).includes(reason));} finally {await rendered.host.unmount();}
      }
      assert.deepEqual(calls, [], 'even direct invocation of an unavailable row cannot reach an executor');
      rows[0].onSelect(); assert.deepEqual(calls, ['collection']);
      await menu.update({...node.props, actions: [{id: 'delete', wholeQueryUnavailable: true}, {id: 'download', disabled: true, reason: download}, {id: 'edit'}, {id: 'refresh'}]});
      assert.deepEqual((menu.tree as Element).props.items, [], 'marker alone and ordinary ready/disabled forbidden actions remain curated');
      await menu.update({...node.props, count: 200});
      assert.deepEqual((menu.tree as Element).props.items.filter((entry: any) => entry.onSelect).map((entry: any) => entry.id), ['collection'], 'the exception cannot expose bounded-query owner/download actions');
    } finally {await menu.host.unmount();}
  } finally {await bar.host.unmount(); state.theme = previous;}
});


test('actual phone tablet and TV selection bars expose the disabled count-free search refusal with no executor', async () => {
 const previous = state.theme, viewport = state.viewport;
 const calls: string[] = [];
 try {
  for(const [isTV,width] of [[false,390],[false,1024],[true,1920]] as const) {
   state.theme = createTheme({isTV,width}); state.viewport = {...viewport,width};
   const props: SelectionBarProps = {count:1,actions:[{id:'play'}],menu:null,onMenuChange(){},onCancel(){calls.push('cancel');},onAction:id=>calls.push(id),
    selectAllUnavailable:{label:'Select all',reason:'Unavailable for this search.'},
    selectAllLabel:'Select all 999',onSelectAll(){calls.push('stale');}};
   const bar = await mount(SelectionBar, props);
   try {
    const control = ofType(bar.tree,Button).find(node=>node.props.accessibilityLabel==='Select all')!;
    assert.ok(control); assert.equal(control.props.disabled,true); assert.equal(control.props.onPress,undefined);
    assert.equal(control.props.accessibilityHint,props.selectAllUnavailable!.reason); assert.deepEqual(control.props.accessibilityState,{disabled:true});
    assert.equal(text(control),'Select all'); assert.match(text(bar.tree),/Unavailable for this search\./); assert.doesNotMatch(text(bar.tree),/999/);
    await bar.update({...props,selectAllUnavailable:undefined});
    const ready = ofType(bar.tree,Button).find(node=>text(node)==='Select all 999')!;
    assert.ok(ready); ready.props.onPress(); assert.equal(calls.at(-1),'stale');
    await bar.update({...props,selectAllUnavailable:undefined,selectAllLabel:undefined,onSelectAll:undefined});
    assert.doesNotMatch(text(bar.tree),/Select all|Unavailable for this search/);
   } finally {await bar.host.unmount();}
  }
 } finally {state.theme=previous;state.viewport=viewport;}
 assert.deepEqual(calls,['stale','stale','stale']);
});

test('phone and TV selection actions retain disabled sort reasons and Queue explanations without executors', async () => {
  const previous = state.theme;
  const calls: string[] = [], reason = 'Unavailable with this sort order.';
  try {
    for (const isTV of [false, true]) {
      state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
      const props: SelectionBarProps = {count: 201, onCancel() {}, menu: 'queue', onMenuChange() {}, onAction: id => calls.push(id), actions: [
        {id: 'play', disabled: true, reason}, {id: 'playlist', disabled: true, reason}, {id: 'queue', reason}, {id: 'playNext', disabled: true, reason}, {id: 'playLater', disabled: true, reason},
      ]};
      const bar = await mount(SelectionBar, props);
      try {
        if (isTV) {
          const controls = elements(bar.tree).filter(element => element.props.accessibilityHint === reason && element.props.disabled);
          assert.equal(controls.length, 2);for (const control of controls) control.props.onPress();assert.deepEqual(calls, []);
        }
        if (!isTV) {
          const phone = await mount(SelectionPhoneActionBar, props);
          try {
            const controls = elements(phone.tree).filter(element => element.props.accessibilityHint === reason && element.props.disabled);
            assert.equal(controls.length, 2);for (const control of controls) control.props.onPress();assert.deepEqual(calls, []);
          } finally {await phone.host.unmount();}
        }
        const node = elements(bar.tree).find(element => element.props.menu === 'queue' && element.type !== SelectionBar)!;
        const menu = await mount(node.type, node.props);
        try {
          const rows = elements(menu.tree).filter(element => element.props.description === reason && element.props.onPress);
          assert.equal(rows.length, 2);
          for (const row of rows) {assert.equal(row.props.disabled, true);row.props.onPress();}
          assert.deepEqual(calls, []);
        } finally {await menu.host.unmount();}
      } finally {await bar.host.unmount();}
    }
  } finally {state.theme = previous;}
});
test('bounded unavailable Delete uses the existing phone control reason while TV owner curation remains intact', async () => {
  const previous = state.theme, calls: string[] = [], reason = 'Unavailable with this sort order.';
  try {
    for (const isTV of [false, true]) {
      state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
      const props: SelectionBarProps = {count: 200, onCancel() {}, menu: 'more', onMenuChange() {}, onAction: id => calls.push(id), actions: [{id: 'more'}, {id: 'delete', disabled: true, reason}]};
      const bar = await mount(SelectionBar, props);
      try {
        const node = elements(bar.tree).find(element => element.props.menu === 'more' && element.type !== SelectionBar)!;
        const menu = await mount(node.type, node.props);
        try {
          if (isTV) assert.deepEqual((menu.tree as any).props.items, [], 'a new exclusion cannot expose a curated owner action');
          else {
            const row = elements(menu.tree).find(element => element.props.description === reason && element.props.onPress);assert.ok(row);assert.equal(row.props.disabled, true);row.props.onPress();
          }
          assert.deepEqual(calls, []);
        } finally {await menu.host.unmount();}
      } finally {await bar.host.unmount();}
    }
  } finally {state.theme = previous;}
});

test('Many values renders on actual native scalar, enum and list controls without an exact cardinality', async () => {
  const many = {state: 'many' as const, atLeast: 65, truncated: true, values: [], entries: [{value: 'Preserved', count: 17}]};
  for (const isTV of [false, true]) {
    state.theme = createTheme({isTV, width: isTV ? 1920 : 390});
    for (const field of [bulk().schema[0]!, {...bulk().schema[0]!, type: 'enum' as const, allowed: ['A']}, bulk().schema[1]!]) {
      const calls: unknown[] = [], props = bulk({count: 70, schema: [field], values: {[field.field]: many}, onChangesChange: value => calls.push(value)});
      const editor = await mount(BulkEditor, props);
      try {
        const rendered = elements(editor.tree).find(element => element.props.field?.field === field.field)!;
        const input = await mount(rendered.type, rendered.props);
        try {
          const label = field.type === 'enum' ? ofType(input.tree, Select)[0]!.props.placeholder : text(input.tree);
          assert.match(label, /Many values/); assert.doesNotMatch(label, /65|70 values/); assert.deepEqual(calls, []);
          if (field.type === 'list') assert.match(text(input.tree), /Preserved/);
          else if (field.type !== 'enum') {
            const control = ofType(input.tree, TextField)[0]!;
            assert.equal(control.props.value, ''); control.props.onChangeText('2020');
            assert.deepEqual(calls, [{year: {value: '2020'}}]);
          }
        } finally {await input.host.unmount();}
      } finally {await editor.host.unmount();}
    }
  }
  state.theme = createTheme({isTV: true, width: 1920});
});
