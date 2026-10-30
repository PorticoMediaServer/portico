import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {buttonSpec} from '@portico/design';
import {createTheme} from '../src-next/kit/theme.ts';
import * as controlModel from '../src-next/kit/controls/control-model.ts';
import {glassRecipe} from '../src-next/platform/glass.ts';
import {screenModule} from './helpers/screen-harness.mjs';

type Element = React.ReactElement<Record<string, any>>;
const flatten = (value: any): Record<string, any> => Array.isArray(value) ? Object.assign({}, ...value.map(flatten)) : value ?? {};
function elements(value: any): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements);
  return React.isValidElement<Record<string, any>>(value) ? [value, ...elements(value.props.children)] : [];
}
async function button(os: 'android' | 'ios', isTV: boolean) {
  const theme = createTheme({isTV, width: isTV ? 1920 : 390});
  const {Button, ResumeButton} = await screenModule(new URL('../src-next/kit/controls/Button.tsx', import.meta.url), {
    react: {...React, default: React}, '@portico/design': {buttonSpec},
    'react-native': {Platform: {OS: os, isTV}, View: 'View', Text: 'Text', ActivityIndicator: 'ActivityIndicator', StyleSheet: {create: (s: any) => s, flatten, absoluteFill: {position: 'absolute', inset: 0}}},
    './Focusable': {Focusable: 'Focusable'}, './control-model': controlModel,
    '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => glassRecipe(os, isTV)},
    '../dev-observation': {useNativeObservation: (_facts: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus: () => {}})}, '../theme': {useTheme: () => theme}, '../Icon': {Icon: 'Icon'},
  });
  function host(variant: 'glass' | 'icon' | 'primary' | 'text', extra: Record<string, any> = {}, ref: any = null, resume = false) {
    const onPress = () => {};
    const component = resume ? ResumeButton : Button;
    const content: Element = component.render({variant, children: 'Action', onPress, accessibilityLabel: 'Action', nextFocusDown: 17, ...extra}, ref);
    const focus: Element = (content.type as any).render(content.props, ref);
    assert.equal(focus.props.onPress, onPress); assert.equal(focus.props.nextFocusDown, 17); assert.equal(focus.props.accessibilityLabel, 'Action');
    assert.equal(focus.props.ref, ref);
    return focus;
  }
  return {theme, host, render(variant: 'glass' | 'icon' | 'primary' | 'text', state: {focused: boolean; hovered: boolean; pressed: boolean}) {
    return elements(host(variant).props.children(state));
  }};
}

test('actual glass and icon Buttons remove only Android dependency tint and retain every drawn theme-alpha state', async () => {
  for (const os of ['android', 'ios'] as const) for (const isTV of [false, true]) {
    const actual = await button(os, isTV);
    for (const variant of ['glass', 'icon'] as const) for (const state of [{focused: false, hovered: false, pressed: false}, {focused: false, hovered: true, pressed: false}, {focused: true, hovered: false, pressed: false}]) {
      const nodes = actual.render(variant, state), blur = nodes.filter(node => node.type === 'BlurView');
      assert.equal(blur.length, glassRecipe(os, isTV).nativeBlur ? 1 : 0);
      if (blur.length) {
        assert.equal(blur[0]!.props.blurType, 'dark'); assert.equal(blur[0]!.props.blurAmount, buttonSpec.glass.blur);
        assert.equal(blur[0]!.props.reducedTransparencyFallbackColor, actual.theme.colors.raisedHigh);
        assert.equal(Object.hasOwn(blur[0]!.props, 'overlayColor'), os === 'android');
        if (os === 'android') assert.equal(blur[0]!.props.overlayColor, 'transparent');
      }
      const alpha = state.focused && isTV ? buttonSpec.glass.focusedTvAlpha : state.hovered ? buttonSpec.glass.hoverAlpha : buttonSpec.glass.alpha;
      const fills = nodes.filter(node => node.type === 'View').map(node => flatten(node.props.style).backgroundColor).filter(Boolean);
      assert.deepEqual(fills, [controlModel.colorAlpha(actual.theme.colors.text, alpha)], `${os}/${isTV}/${variant}: retain sole drawn fill`);
    }
  }
});

test('Android phone glass owns every face foreground while one original Focusable keeps refs, input and accessibility', async () => {
  const actual = await button('android', false), ref = React.createRef();
  for (const loading of [undefined, false, true]) {
    const state = {focused: true, hovered: true, pressed: true};
    const style = (value: unknown) => {assert.equal(value, state); return {height: 50, maxWidth: 250, borderRadius: 11, paddingHorizontal: 23, paddingRight: 9, backgroundColor: '#ff000029', columnGap: 7};};
    const focus = actual.host('glass', {style, loading, icon: 'i-play', trailingIcon: 'i-right', progress: .4, remaining: 'Time', accessibilityState: {selected: true}, testID: 'real-button', focusRadius: 13, focusInset: true, onLayout: style}, ref, true);
    assert.equal(focus.type, 'Focusable'); assert.equal(focus.props.testID, 'real-button'); assert.equal(focus.props.onLayout, style);
    assert.equal(focus.props.focusRadius, 13); assert.equal(focus.props.focusInset, true); assert.equal(focus.props.accessibilityRole, 'button');
    assert.deepEqual(focus.props.accessibilityState, {selected: true, busy: loading}); assert.equal(focus.props.disabled, loading);
    const face: Element = focus.props.children(state), nodes = elements(face);
    assert.equal(face.type, 'View', 'a native RN View clips the rounded face');
    assert.equal(face.props.pointerEvents, 'none');
    const blur = face.props.children as Element;
    assert.equal(blur.type, 'BlurView'); assert.equal(blur.props.overlayColor, 'transparent');
    const layout = blur.props.children as Element;
    assert.equal(layout.type, 'View'); assert.equal(layout.props.collapsable, false, 'FrameLayout gets one real full-face RN layout child');
    assert.deepEqual(elements(blur).filter(node => node.type === 'Text'), nodes.filter(node => node.type === 'Text'), 'all face foreground stays inside the skipped capture subtree');
    assert.equal(nodes.filter(node => node.type === 'BlurView').length, 1);
    assert.equal(nodes.filter(node => node.type === 'Text').length, 2, 'label and remaining are drawn once');
    assert.equal(nodes.filter(node => node.type === 'ActivityIndicator').length, loading ? 1 : 0);
    assert.equal(nodes.filter(node => node.type === 'Icon').length, loading ? 1 : 2, 'trailing glyph and current leading slot remain inside');
    assert.ok(nodes.some(node => flatten(node.props.style).width === '40%'), 'edge progress is inside the skipped subtree');
    const outer = flatten(focus.props.style(state)), clip = flatten(face.props.style), inner = flatten(layout.props.style);
    assert.equal(outer.height, 50); assert.equal(outer.maxWidth, 250); assert.equal(outer.borderRadius, 11); assert.equal(outer.paddingHorizontal, 0); assert.equal(outer.paddingRight, 0); assert.equal(outer.backgroundColor, 'transparent');
    assert.equal(clip.borderRadius, 11); assert.equal(clip.overflow, 'hidden');
    assert.equal(inner.paddingHorizontal, 23); assert.equal(inner.paddingRight, 9); assert.equal(inner.backgroundColor, '#ff000029');
    for (const element of [face, blur, layout]) {
      const geometry = flatten(element.props.style);
      assert.equal(geometry.alignSelf, 'stretch'); assert.equal(geometry.flexGrow, 1); assert.equal(geometry.flexShrink, 1);
    }
    assert.equal(inner.alignItems, 'center'); assert.equal(inner.justifyContent, 'center');
    assert.ok(nodes.some(node => flatten(node.props.style).gap === 7));
  }
});

test('Apple phone/TV and Android TV retain their background/content siblings and native blur semantics', async () => {
  const state = {focused: true, hovered: false, pressed: false};
  for (const [os, tv] of [['ios', false], ['ios', true], ['android', true]] as const) {
    const actual = await button(os, tv), focus = actual.host('glass', {progress: .4, remaining: 'Time', style: {paddingHorizontal: 23, backgroundColor: '#ff000029'}});
    const tree: Element = focus.props.children(state), nodes = elements(tree);
    assert.equal(tree.type, React.Fragment); assert.equal(flatten(focus.props.style(state)).paddingHorizontal, 23);
    assert.equal(flatten(focus.props.style(state)).backgroundColor, '#ff000029');
    const blur = nodes.filter(node => node.type === 'BlurView'); assert.equal(blur.length, os === 'ios' ? 1 : 0);
    if (blur.length) {assert.equal(blur[0]!.props.children, undefined); assert.equal(Object.hasOwn(blur[0]!.props, 'overlayColor'), false);}
    assert.equal(nodes.filter(node => node.type === 'Text').length, 2);
  }
});

test('primary and text Buttons still have no blur dependency', async () => {
  for (const os of ['android', 'ios'] as const) {
    const actual = await button(os, false);
    for (const variant of ['primary', 'text'] as const) assert.equal(actual.render(variant, {focused: false, hovered: false, pressed: false}).filter(node => node.type === 'BlurView').length, 0);
  }
});

test('explicit glyph color reaches leading, trailing and loading glyphs without changing labels or defaults', async () => {
  const state = {focused: false, hovered: false, pressed: false};
  for (const os of ['android', 'ios'] as const) for (const tv of [false, true]) {
    const actual = await button(os, tv);
    for (const variant of ['primary', 'text', 'glass', 'icon'] as const) for (const loading of [false, true]) {
      const props = {loading, icon: 'i-server', trailingIcon: 'i-right', textStyle: {color: '#9e9e9e'}};
      const defaultNodes = elements(actual.host(variant, props).props.children(state));
      const nodes = elements(actual.host(variant, {...props, iconColor: '#828282'}).props.children(state));
      const glyphs = nodes.filter(node => node.type === 'Icon' || node.type === 'ActivityIndicator');
      assert.equal(glyphs.length, 2); assert.ok(glyphs.every(node => node.props.color === '#828282'));
      const foreground = variant === 'primary' ? actual.theme.colors.ink : variant === 'text' ? actual.theme.colors.accentStrong : actual.theme.colors.text;
      assert.ok(defaultNodes.filter(node => node.type === 'Icon' || node.type === 'ActivityIndicator').every(node => node.props.color === foreground));
      assert.deepEqual(nodes.filter(node => node.type === 'Text').map(node => flatten(node.props.style)), defaultNodes.filter(node => node.type === 'Text').map(node => flatten(node.props.style)));
      assert.ok(nodes.filter(node => node.type === 'Text').every(node => flatten(node.props.style).color === '#9e9e9e'));
      assert.equal(Object.hasOwn(actual.host(variant, {iconColor: '#828282'}).props, 'iconColor'), false, 'the presentation prop never leaks to Focusable');
    }
  }
});
