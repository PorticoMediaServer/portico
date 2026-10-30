// @fabric-react
/** Fabric hook probes run the real ownership hooks and publishers with
 * controlled context. Returned child elements are inspected separately; this
 * does not establish native scroller/image/host identity or device pixels. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createFabricHookHost, act} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {freezeOverlayPaint} from '../src-next/kit/overlays/overlay-paint-model.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {activeLyricLine, lyricSeekPosition} from '../../../packages/client-core/src/lyrics';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes)
  : React.isValidElement<any>(value) ? [value, ...nodes(value.props.children)] : [];

async function fixture() {
  let tree: any, mode = {visible: true, interactive: true, root: true};
  const contexts: any[] = [];
  const react = {...React, default: React,
    createContext: (initial: any) => {const value = {Provider: 'Provider', initial};contexts.push(value);return value;},
    useContext: (context: any) => context === contexts[0] ? tree?.props.value : tree?.props.children.props.value ?? context.initial,
  };
  const hooks = await screenModule(new URL('../src-next/kit/overlays/CommittedPaint.tsx', import.meta.url), {
    react, './OverlayPaintContext': {useOverlayPaintMode: () => mode},
  });
  const provider = createFabricHookHost(() => {tree = hooks.CommittedPaintProvider({children: null});});await provider.mount();
  const common = {react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    '../../../kit/overlays/CommittedPaint': hooks,
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../../kit/artwork': {Artwork: 'Artwork'},
    '../../../kit/theme': {useTheme: () => ({colors: {text: '#fff', textSecondary: '#ccc', textTertiary: '#aaa', line: '#555', accentStrong: '#0ff'}})},
    '../../../kit/browse/presentation': {browseType: () => ({})},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
  };
  const panes = await screenModule(new URL('../src-next/screens/destinations/music/ListeningPanePaint.tsx', import.meta.url), common);
  return {hooks, panes, common, provider, publish: () => tree.props.value, store: () => tree.props.children.props.value,
    inert() {mode = {...mode, interactive: false};}, hidden() {mode = {...mode, visible: false, interactive: false};},
    async close() {await provider.unmount();}};
}

test('ordinary display updates keep original callbacks; retirement never adopts a successor operation', async () => {
  const f = await fixture(), owner = {}, scope = 'original', calls: string[] = [];
  let semantic = {}, value = {data: {caption: 'first'}, actions: {press: () => calls.push('first')}}, paint: any;
  const publisher = createFabricHookHost(() => f.hooks.useCommittedPaintPublication(owner, 'queue', scope, semantic, value));
  const leaf = createFabricHookHost(() => {paint = f.hooks.useCommittedPaint({owner, channel: 'queue', scope});});
  try {
    await act(() => publisher.mount());await leaf.mount();const original = paint;
    value = {data: {caption: 'second'}, actions: {press: () => calls.push('second')}};
    await act(() => publisher.rerender());await leaf.rerender();
    if (original.current()) original.actions.press();
    assert.deepEqual(calls, ['first'], 'an old UI event retains its original callback, not the latest one');
    assert.equal(paint.data.caption, 'second');
    await act(() => publisher.unmount());
    assert.equal(original.current(), false);
    semantic = {};value = {data: {caption: 'successor'}, actions: {press: () => calls.push('successor')}};
    await act(() => publisher.mount());
    if (original.current()) original.actions.press();
    assert.deepEqual(calls, ['first']);
    f.inert();await leaf.rerender();
    assert.equal(paint.data.caption, 'second', 'only display committed by this leaf is retained');
    assert.equal(paint.actions, undefined, 'no operation packet is returned to inert paint');
    assert.equal(paint.current(), false);
    f.hidden();await leaf.rerender();assert.equal(paint.data, undefined);
  } finally {await publisher.unmount();await leaf.unmount();await f.close();}
});

test('original scope replacement retires callbacks and old cleanup cannot erase a current publication', async () => {
  const f = await fixture(), owner = {};let scope = 'a', semantic = {}, paint: any;
  const publisher = createFabricHookHost(() => f.hooks.useCommittedPaintPublication(owner, 'queue', scope, semantic,
    {data: {scope}, actions: {press() {}}}));
  const leaf = createFabricHookHost(() => {paint = f.hooks.useCommittedPaint({owner, channel: 'queue', scope});});
  try {
    await act(() => publisher.mount());await leaf.mount();const original = paint;
    scope = 'b';semantic = {};await leaf.rerender();assert.equal(paint.data, undefined, 'old display cannot cross a new source scope');
    await act(() => publisher.rerender());await leaf.rerender();
    assert.equal(original.current(), false);assert.equal(paint.data.scope, 'b');
    let offOld: any, offNew: any;
    await act(() => {
      offOld = f.publish()(owner, 'extra', {scope, data: {caption: 'old'}, actions: {}, current: () => true});
      offNew = f.publish()(owner, 'extra', {scope, data: {caption: 'new'}, actions: {}, current: () => true});
    });
    await act(() => offOld());assert.equal(f.store().get(owner).get('extra').data.caption, 'new');
    await act(() => offNew());
    await act(() => publisher.unmount());
    assert.equal(f.store().size, 0);
  } finally {await publisher.unmount();await leaf.unmount();await f.close();}
});

test('a full queue display retains committed rows without putting its large data or commands in captured props', async () => {
  const f = await fixture(), owner = Object.freeze({}), calls: string[] = [];
  const data = {current: [], groups: [{key: 'actual', label: 'Next', rows: Array.from({length: 1000}, (_, i) => ({id: `row-${i}`, title: `Row ${i}`,
    current: false, playable: true, menu: false}))}], summary: ['1000 songs']};
  const publisher = createFabricHookHost(() => f.hooks.useCommittedPaintPublication(owner, 'listening-queue', 'source', {},
    {data, actions: {play: (id: string) => calls.push(id)}}));
  let tree: any;
  const leaf = createFabricHookHost(() => {tree = f.panes.ListeningQueuePaint({owner, scope: 'source'});});
  try {
    await act(() => publisher.mount());await leaf.mount();
    const originalPress = nodes(tree).find(node => node.props.testID === 'now-playing-queue-row-row-999').props.onPress;
    originalPress();assert.deepEqual(calls, ['row-999']);
    const element = React.createElement(f.panes.ListeningQueuePaint, {owner, scope: 'source'});
    const frozen = freezeOverlayPaint(element, [f.panes.ListeningQueuePaint]);
    assert.ok(frozen);assert.deepEqual(Object.keys(frozen.props).sort(), ['owner', 'scope']);
    await act(() => publisher.unmount());f.inert();await leaf.rerender();
    originalPress();assert.deepEqual(calls, ['row-999'], 'a queued old row event is inert after publisher retirement');
    assert.equal(nodes(tree).filter(node => node.type === 'Focusable').length, 1000);
    assert.equal(tree.props.pointerEvents, 'none');
    assert.equal(nodes(tree).find(node => node.type === 'ScrollView').props.scrollEnabled, false);
  } finally {await publisher.unmount();await leaf.unmount();await f.close();}
});

test('the real lyrics publisher disposes its original reader while committed display remains inert', async () => {
  const f = await fixture(), owner = {}, target = {}, seeks: number[] = [];
  let reads = 0, refreshes = 0, disposals = 0;
  const document = {format: 'lrc', lines: [{atMs: 0, text: 'first'}, {atMs: 10000, text: 'second'}]};
  const snapshot: any = {data: {selection: {resource: {id: 'resource', document, offsetMs: 0}}, source: {startSeconds: 0, duration: 100}, resources: []},
    candidates: [], warnings: [], preview: null, busy: false, error: '', generation: 1};
  const service = {subscribe: () => () => {}, getSnapshot: () => snapshot,
    async refresh() {refreshes++;}, async choose() {}, dispose() {disposals++;}};
  const {NowPlayingLyrics} = await screenModule(new URL('../src-next/screens/destinations/music/NowPlayingLyrics.tsx', import.meta.url), {
    ...f.common, '../../../../../../packages/client-core/src/lyrics': {activeLyricLine, lyricSeekPosition},
  });
  const read = () => {reads++;return {enabled: true, service};};let result: any, tree: any;
  const publisher = createFabricHookHost(() => {result = NowPlayingLyrics({read, target, positionSeconds: 1, playing: true,
    onSeek: (at: number) => seeks.push(at), paintOwner: owner, paintScope: 'source'});});
  const leaf = createFabricHookHost(() => {tree = f.panes.ListeningLyricsPaint({owner, scope: 'source'});});
  try {
    await leaf.mount();assert.equal(tree, null);assert.equal(reads, 0, 'mounting the pure display leaf cannot start a reader');
    await act(() => publisher.mount());await leaf.rerender();
    assert.equal(result, null, 'the publisher does not render a second native pane');
    assert.equal(reads, 1);assert.equal(refreshes, 1);assert.equal(disposals, 0);
    const originalSeek = nodes(tree).find(node => node.props.testID === 'now-playing-lyrics-line-1').props.onPress;
    const data = f.store().get(owner).get('listening-lyrics').data;
    assert.deepEqual(data.lines.map((line: any) => line.text), ['first', 'second']);
    originalSeek();assert.deepEqual(seeks, [10]);
    await act(() => publisher.unmount());assert.equal(disposals, 1);
    originalSeek();assert.deepEqual(seeks, [10]);
    f.inert();await leaf.rerender();
    assert.ok(nodes(tree).some(node => node.type === 'Text' && node.props.children === 'first'));
    assert.ok(nodes(tree).some(node => node.type === 'Text' && node.props.children === 'second'));
    nodes(tree).find(node => node.props.testID === 'now-playing-lyrics-line-1').props.onPress();assert.deepEqual(seeks, [10]);
    assert.equal(f.store().size, 0);assert.equal('service' in data, false);assert.equal('read' in data, false);
  } finally {await publisher.unmount();await leaf.unmount();await f.close();}
});
