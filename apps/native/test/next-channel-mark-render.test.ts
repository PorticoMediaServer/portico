import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import * as React from 'react';
import {channelTint} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {createTheme, fontFamilies} from '../src-next/kit/theme.ts';
import type {ChannelMarkProps} from '../src-next/kit/artwork/types.ts';
import type {LiveCardProps} from '../src-next/kit/cards/SurfaceCards.tsx';
import {screenModule} from './helpers/screen-harness.mjs';

type Assert<T extends true> = T;
type NoMarkTintOverride = Assert<'tint' extends keyof ChannelMarkProps ? false : true>;
type NoCardTintOverride = Assert<'tint' extends keyof LiveCardProps ? false : true>;
type Element = React.ReactElement<Record<string, any>>;
const flatten = (value: any): Record<string, any> => Array.isArray(value) ? Object.assign({}, ...value.map(flatten)) : value ?? {};
function elements(value: any): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements);
  return React.isValidElement<Record<string, any>>(value) ? [value, ...elements(value.props.children)] : [];
}
const vectors: {seed: string; hash: number; index: number; tint: string}[] = JSON.parse(readFileSync(new URL('../../../packages/client-core/test/fixtures/channel-tint-vectors.json', import.meta.url), 'utf8'));

test('actual ChannelMark renders every canonical shared vector using the delivered seed verbatim', async () => {
  for (const isTV of [false, true]) {
    const theme = createTheme({isTV, width: isTV ? 1920 : 390}), seen: string[] = [];
    const {ChannelMark} = await screenModule(new URL('../src-next/kit/artwork/ChannelMark.tsx', import.meta.url), {
      react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text', StyleSheet: {absoluteFillObject: {position: 'absolute', inset: 0}}},
      '../../../../../packages/client-core/src/presentation/kit-model/index': {channelTint: (seed: string) => {seen.push(seed); return channelTint(seed);}},
      '../theme': {useTheme: () => theme, fontFamilies}, './Artwork': {Artwork: 'Artwork'},
    });
    assert.ok(vectors.some(vector => vector.hash < 0)); assert.ok(vectors.some(vector => vector.hash === -2147483648));
    assert.equal(new Set(vectors.map(vector => vector.tint)).size, 8);
    for (const vector of vectors) {
      const mark = {sourceId: 'actual-source', id: 'actual-channel', text: 'N24', seed: vector.seed};
      const root: Element = ChannelMark({mark, style: {marginTop: 7, backgroundColor: '#ff0000'}});
      const style = flatten(root.props.style);
      assert.equal(seen.at(-1), vector.seed, 'do not trim, normalize or replace the producer seed');
      assert.equal(style.backgroundColor, vector.tint, 'the actual mark background is the canonical tint, without a half-opacity tint layer or caller override');
      assert.equal(style.width, 34); assert.equal(style.height, 34); assert.equal(style.borderRadius, 8); assert.equal(style.marginTop, 7);
      assert.equal(elements(root).filter(node => node.type === 'View').length, 2);
      const label = elements(root).find(node => node.type === 'Text')!;
      assert.equal(label.props.children, mark.text); assert.equal(label.props.style.fontFamily, fontFamilies[800]);
      assert.equal(root.props.accessible, false); assert.equal(root.props.accessibilityRole, undefined);
    }
    assert.deepEqual(seen, vectors.map(vector => vector.seed));
  }
});

test('channel logos stay above the monogram with unchanged geometry, scheduling and accessible identity', async () => {
  const theme = createTheme({isTV: false, width: 390});
  const {ChannelMark} = await screenModule(new URL('../src-next/kit/artwork/ChannelMark.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text', StyleSheet: {absoluteFillObject: {position: 'absolute', inset: 0}}},
    '../../../../../packages/client-core/src/presentation/kit-model/index': {channelTint},
    '../theme': {useTheme: () => theme, fontFamilies}, './Artwork': {Artwork: 'Artwork'},
  });
  const mark = {sourceId: 'source', id: 'channel', text: 'NEWS', seed: 'source:channel'};
  const source = {uri: 'https://server.test/channel-logo', cacheKey: 'current-authority:channel-logo'}, onStatusChange = () => {};
  const root: Element = ChannelMark({mark, source, size: 48, alt: 'Actual channel', visible: false, onStatusChange});
  const children = React.Children.toArray(root.props.children) as Element[], logo = children.at(-1)!;
  assert.equal(logo.type, 'Artwork'); assert.equal(logo.props.source, source); assert.equal(logo.props.fit, 'contain');
  assert.equal(logo.props.visible, false); assert.equal(logo.props.onStatusChange, onStatusChange);
  assert.equal(root.props.accessible, true); assert.equal(root.props.accessibilityRole, 'image'); assert.equal(root.props.accessibilityLabel, 'Actual channel');
  const style = flatten(root.props.style); assert.equal(style.width, 48); assert.equal(style.height, 48); assert.equal(style.borderRadius, 48 * 8 / 34);
  const label = elements(root).find(node => node.type === 'Text')!;
  assert.equal(label.props.children, mark.text); assert.equal(label.props.style.fontSize, 48 * .32);
  assert.equal(label.props.style.color, theme.colors.text); assert.equal(label.props.accessible, false); assert.equal(label.props.allowFontScaling, false);
});

test('LiveCard forwards the authoritative mark and logo without a palette override or card geometry change', async () => {
  for (const isTV of [false, true]) {
    const theme = createTheme({isTV, width: isTV ? 1920 : 390});
    const {LiveCard} = await screenModule(new URL('../src-next/kit/cards/SurfaceCards.tsx', import.meta.url), {
      react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text'},
      '../../../../../packages/i18n/src/index': {defaultI18n: {}},
      '../artwork/index': Object.fromEntries(['AmbientArt', 'Artwork', 'ChannelMark', 'CoverFan', 'CoverMosaic', 'MediaArt'].map(name => [name, name])),
      '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme}, './MediaCards': {CardOverlays: 'CardOverlays'},
      './primitives': Object.fromEntries(['ArtFrame', 'Caption', 'CardFrame', 'Parts', 'Progress', 'Scrim', 'alpha', 'titleStyle'].map(name => [name, name])),
    });
    const mark = {sourceId: 'actual', id: 'channel', text: 'AC', seed: 'actual:channel'}, logo = {uri: 'https://server.test/logo', cacheKey: 'authority:logo'}, onFocus = () => {};
    const frame: Element = LiveCard({title: 'Actual programme', channel: 'Actual channel', mark, logo, onFocus, visible: false});
    assert.equal(frame.props.onFocus, onFocus); assert.equal(frame.props.width, isTV ? 400 : 244);
    const art: Element = frame.props.children({focused: false, hovered: false, holding: false}), nodes = elements(art);
    assert.equal(art.props.height, isTV ? 225 : 140);
    const channel = nodes.find(node => node.type === 'ChannelMark')!;
    assert.equal(channel.props.mark, mark); assert.equal(channel.props.source, logo); assert.equal(channel.props.size, isTV ? 48 : 34); assert.equal(channel.props.visible, false);
    assert.equal(Object.hasOwn(channel.props, 'tint'), false);
    assert.deepEqual(nodes.find(node => node.type === 'AmbientArt')!.props.palette, {first: theme.colors.ambient, second: theme.colors.raised});
  }
});
