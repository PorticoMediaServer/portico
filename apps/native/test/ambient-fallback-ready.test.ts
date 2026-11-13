// @fabric-react
/** Authored UNRUN hook/source vectors. Host nodes, native animation and image
 * notifications are controlled; this is no palette/frame/image or TV proof. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createFabricHookHost, act} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : React.isValidElement<any>(node) ? [node, ...nodes(node.props.children)] : [];
async function rig() {
  let props: any, tree: any, fallback = 0, decoded = 0;
  const theme = createTheme({isTV: true, width: 1920});
  const {AmbientArt} = await screenModule(new URL('../src-next/kit/artwork/Surfaces.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', StyleSheet: {absoluteFillObject: {}, create: (value: any) => value},
      Animated: {View: 'AnimatedView', Value: class {constructor(public value: number) {} stopAnimation() {} setValue(next: number) {this.value = next;}},
        timing: () => ({start() {}})}},
    'react-native-reanimated': {default: {View: 'ReanimatedView'}},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
    '../theme': {useTheme: () => theme}, '../useMotion': {useReducedMotion: () => false}, '../dev-observation': {useNativeObservation() {}},
    '../../platform/ArtworkAlphaMask': {ArtworkAlphaMask: 'ArtworkAlphaMask'}, './Artwork': {Artwork: 'Artwork'},
    './model': {artworkArrivalDuration: () => 180},
  });
  const host = createFabricHookHost(() => {tree = AmbientArt(props);});
  return {get fallback() {return fallback;}, get decoded() {return decoded;},
    async mount(next: any = {}) {props = {onFallbackReady: () => fallback++, onReady: () => decoded++, ...next};await host.mount();await act(() => {});},
    async update(next: any) {props = {...props, ...next};await host.rerender();await act(() => {});},
    artwork: () => nodes(tree).filter(node => node.type === 'Artwork'), close: () => host.unmount()};
}
const art = {uri: '/offered', cacheKey: 'offered'};

test('an artless palette reports its own committed readiness once without a decode notification', async () => {
  const r = await rig(); await r.mount();
  try {
    assert.equal(r.fallback, 1); assert.equal(r.decoded, 0);
    await r.update({width: 1920});
    assert.equal(r.fallback, 1, 'an ordinary layout rerender does not re-admit the same fallback');
    assert.equal(r.artwork().length, 0);
  } finally {await r.close();}
});

test('an offered foreground or ambient image still waits for its original actual notification', async () => {
  const r = await rig(); await r.mount({foreground: art});
  try {
    assert.equal(r.fallback, 0); assert.equal(r.decoded, 0);
    await act(() => r.artwork()[0].props.onReady(art));
    assert.equal(r.decoded, 1); assert.equal(r.fallback, 0);
    await r.update({foreground: undefined, source: art});
    assert.equal(r.fallback, 0);
    await act(() => r.artwork()[0].props.onReady(art));
    assert.equal(r.decoded, 2); assert.equal(r.fallback, 0);
  } finally {await r.close();}
});

test('hidden empty art waits for visibility and a later empty interval gets a fresh fallback notification', async () => {
  const r = await rig(); await r.mount({visible: false});
  try {
    assert.equal(r.fallback, 0);
    await r.update({visible: true}); assert.equal(r.fallback, 1);
    await r.update({visible: false}); await r.update({visible: true});
    assert.equal(r.fallback, 1);
    await r.update({foreground: art}); await r.update({foreground: undefined});
    assert.equal(r.fallback, 2); assert.equal(r.decoded, 0);
  } finally {await r.close();}
});

test('a failed v2 wash falls back once and later pictures on that server use v1 for the app session', async () => {
  const source = (server: string, hash: string) => ({variant: 'ambient-v2', maxEdge: 64, version: hash,
    url: `/v1/images/${hash}?size=ambient-v2`, uri: `https://${server}/v1/images/${hash}?size=ambient-v2`,
    cacheKey: `${server}/viewer/session/${hash}/ambient-v2`, headers: {Authorization: 'fixture-authority'}});
  const first = source('legacy.test', 'a'.repeat(64));
  const r = await rig(); let failed = 0; const statuses: string[] = [];
  await r.mount({source: first, onError: () => failed++, onStatusChange: (status: string) => statuses.push(status)});
  try {
    assert.equal(r.artwork()[0].props.source.variant, 'ambient-v2');
    await act(() => r.artwork()[0].props.onStatusChange('error'));
    assert.deepEqual(statuses, [], 'v2 failure is not terminal while v1 can load');
    await act(() => r.artwork()[0].props.onError(first));
    const fallback = r.artwork()[0].props.source;
    assert.equal(fallback.variant, 'ambient-v1');
    assert.equal(fallback.uri, first.uri.replace('ambient-v2', 'ambient-v1'));
    assert.equal(fallback.url, first.url.replace('ambient-v2', 'ambient-v1'));
    assert.notEqual(fallback.cacheKey, first.cacheKey);
    assert.deepEqual(fallback.headers, first.headers);
    assert.equal(failed, 0, 'v2 failure first tries v1');
    await act(() => r.artwork()[0].props.onReady(fallback));
    assert.equal(r.decoded, 1);
    await act(() => r.artwork()[0].props.onStatusChange('error'));
    await act(() => r.artwork()[0].props.onError(fallback));
    assert.deepEqual(statuses, ['error']);
    assert.equal(failed, 1, 'v1 failure is terminal');
    assert.equal(r.artwork()[0].props.source.uri, fallback.uri);
    await r.update({source: source('legacy.test', 'b'.repeat(64))});
    assert.equal(r.artwork()[0].props.source.variant, 'ambient-v1', 'the next picture skips v2');
    await r.update({source: source('current.test', 'c'.repeat(64))});
    assert.equal(r.artwork()[0].props.source.variant, 'ambient-v2', 'another server still uses v2');
    await r.close();
    await r.mount({source: source('legacy.test', 'd'.repeat(64))});
    assert.equal(r.artwork()[0].props.source.variant, 'ambient-v1', 'a new surface remembers the server');
  } finally {await r.close();}
});
