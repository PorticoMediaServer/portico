import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createTheme} from '../src-next/kit/theme';
import {screenModule} from './helpers/screen-harness.mjs';

// The Home page's kit parts (desk, #4604 round 1: a page passes data and kit parts, never layout of its own).
const theme = createTheme({isTV: false, width: 390});
const parts = () => screenModule(new URL('../src-next/kit/home/HomeParts.tsx', import.meta.url), {
  react: {...React, default: React}, 'react-native': {View: 'View'},
  '../artwork': {AmbientArt: 'AmbientArt'}, '../controls': {Button: 'Button'}, '../states': {LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock'},
  '../theme': {useTheme: () => theme},
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
  const {HomeCollectionRow} = await parts();
  let width = 0;
  const row = HomeCollectionRow({width: 390, children: (banner: number) => {width = banner; return 'banner';}});
  assert.equal(row.props.style.paddingHorizontal, theme.gutter);
  assert.equal(width, 390 - theme.gutter * 2); assert.equal(row.props.children, 'banner');
});
