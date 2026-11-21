import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createTheme} from '../src-next/kit/theme';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// The Home page's kit parts (desk, #4604 round 1: a page passes data and kit parts, never layout of its own).
const theme = createTheme({isTV: false, width: 390});
const parts = (current = theme) => screenModule(new URL('../src-next/kit/home/HomeParts.tsx', import.meta.url), {
  react: {...React, default: React}, 'react-native': {View: 'View'},
  'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, right: 0, bottom: 0, left: 0})},
  '../layout': {Hero: 'Hero'}, '../frames/HomeHero': {homeLockupStyle: () => ({})}, '../layout/TopBar': {roundBarSize: 38}, '../focus': {FocusRegion: 'FocusRegion'},
  '../artwork': {AmbientArt: 'AmbientArt'}, '../controls': {Button: 'Button'}, '../states': {LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock'},
  '../theme': {useTheme: () => current},
});

test('the spotlight buttons are drawn from data, side by side at the hero action gap', async () => {
  const {HomeHeroActions} = await parts(), onPress = () => {};
  const row = HomeHeroActions({actions: [{id: 'play', label: 'Play', icon: 'i-play', variant: 'primary', onPress}, {id: 'my-list', label: 'My List', icon: 'i-plus', variant: 'glass', onPress}]});
  assert.equal(row.props.style.gap, theme.spec.hero.actions.gap[theme.column]);
  const buttons = row.props.children;
  assert.deepEqual(buttons.map((button: any) => [button.key, button.props.variant, button.props.size, button.props.icon, button.props.children]),
    [['play', 'primary', 'hero', 'i-play', 'Play'], ['my-list', 'glass', 'hero', 'i-plus', 'My List']]);
});

test('a row outside the window keeps only its height; a mounted row reports its box', async () => {
  const {HomeRowPlace} = await parts(), onLayout = () => {};
  assert.deepEqual(HomeRowPlace({height: 312}).props.style, {height: 312});
  const mounted = HomeRowPlace({onLayout, children: 'row'});
  assert.equal(mounted.props.onLayout, onLayout); assert.equal(mounted.props.children, 'row');
});

test('a card slot holds its drawn size, grows only under larger text, and paints the skeleton while waiting', async () => {
  const {HomeCardSlot} = await parts();
  const slot = HomeCardSlot({width: 150, height: 279, waiting: true, children: 'card'});
  assert.deepEqual(slot.props.style, {width: 150, height: 279});
  const paint = slot.props.children;
  assert.equal(paint.props.ready, false); assert.equal(paint.props.children, 'card');
});

test('a collection row puts the banner inside the gutters, as wide as the room between them', async () => {
  for (const current of [theme, createTheme({isTV: true, width: 1920})]) {
    const {HomeCollectionRow} = await parts(current);
    const right = current.isTV ? current.layout.gutter.tvRight : current.gutter;
    let width = 0;
    const row = HomeCollectionRow({width: current.width, children: (banner: number) => {width = banner; return 'banner';}});
    assert.deepEqual(row.props.style, {paddingLeft: current.gutter, paddingRight: right});
    assert.equal(width, current.width - current.gutter - right);
    assert.equal(row.props.children, 'banner');
  }
});

test('a synopsis written without word spaces still previews its first measured lines', async () => {
  const host = hooks();
  const {PreviewText} = await screenModule(new URL('../src-next/kit/layout/PreviewText.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'},
  });
  const text = '春が来た。新しい冒険が始まる。旅の続き。';
  const render = () => host.render(() => PreviewText({text, lines: 2, style: {}}));
  render().props.children[1].props.onTextLayout({nativeEvent: {lines: [
    {text: '春が来た。'}, {text: '新しい冒険が始まる。'}, {text: '旅の続き。'},
  ]}});
  assert.equal(render().props.children[0].props.children[0], '春が来た。新しい冒険が始まる。');
});
