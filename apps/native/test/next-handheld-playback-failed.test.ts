import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';

const react = {...hooks().react, createContext: (value: unknown) => ({_currentValue: value})};
const themeModule = await screenModule(new URL('../src-next/kit/theme.ts', import.meta.url), {
  react: {...react, default: react}, 'react-native': {}, '@portico/design': {typography: {
    regular: 'Manrope-Regular', medium: 'Manrope-Medium', semibold: 'Manrope-SemiBold', bold: 'Manrope-Bold', extraBold: 'Manrope-ExtraBold',
  }},
});
const {shellType} = await screenModule(new URL('../src-next/kit/shell/visual.ts', import.meta.url), {'../theme': themeModule, react: {...react, default: react}});
const {NativeHandheldPlaybackFailed} = await screenModule(new URL('../src-next/screens/watch/HandheldPlaybackFailed.tsx', import.meta.url), {
  react: {...react, default: react}, 'react-native': {View: 'View', Text: 'Text'}, '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: () => '0:42'},
  '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../kit/controls': {Button: 'Button'}, '../../kit/theme': {useTheme: () => ({colors: {text: 'white', textSecondary: 'grey'}})},
  '../../kit/shell/visual': {shellType},
});

test('handheld failure offers only the supplied retry and keeps its captured callback', () => {
  let calls = 0; const failure = {fence: {}, positionSeconds: 42};
  const withRetry = NativeHandheldPlaybackFailed({failure, retry: () => {calls++;}});
  const state = withRetry.props.children[1];
  assert.equal(state.props.children[0].props.children[0], 'next.states.playbackStopped');
  state.props.children[2].props.onPress(); assert.equal(calls, 1);
  const noRetry = NativeHandheldPlaybackFailed({failure});
  assert.equal(noRetry.props.children[1].props.children[2], undefined);
});

test('handheld failure paints Manrope and offers the supplied alternative independently of retry', () => {
  let retries = 0, versions = 0;
  const retry = () => {retries++;}, differentVersion = () => {versions++;};
  const tree = NativeHandheldPlaybackFailed({failure: {positionSeconds: 42}, retry, differentVersion});
  const children = tree.props.children[1].props.children;
  const titleStyle = Object.assign({}, ...children[0].props.style), positionStyle = Object.assign({}, ...children[1].props.style);
  assert.equal(titleStyle.fontFamily, 'Manrope-Bold'); assert.equal(titleStyle.fontSize, 16);
  assert.equal(positionStyle.fontFamily, 'Manrope-Regular'); assert.equal(positionStyle.fontSize, 13);
  assert.equal(children[2].props.onPress, retry); assert.equal(children[3].props.onPress, differentVersion);
  children[3].props.onPress(); assert.equal(versions, 1); assert.equal(retries, 0);
  const alternativeOnly = NativeHandheldPlaybackFailed({failure: {positionSeconds: 42}, differentVersion}).props.children[1].props.children;
  assert.equal(alternativeOnly[2], undefined); assert.equal(alternativeOnly[3].props.onPress, differentVersion);
  assert.equal(alternativeOnly[3].props.children[0], 'next.overlays.differentVersion');
  const noActions = NativeHandheldPlaybackFailed({failure: {positionSeconds: 42}}).props.children[1].props.children;
  assert.equal(noActions[2], undefined); assert.equal(noActions[3], undefined);
});
