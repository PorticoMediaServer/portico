// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children), ...nodes(value.props.headerContext)] : [];

test('Record series reads the server\'s preview when it opens and creates the rule it previewed, then closes', async () => {
  const calls: unknown[][] = [];
  const config = {matchKind: 'series', allowedChannels: [], blockedChannels: [], keywords: [], blockedKeywords: [], options: {}};
  const theme = {isTV: false, width: 400, colors: {text: '#fff', accentStrong: '#4af', textTertiary: '#888'}, type: {rowLabel: {fontSize: 15.5, fontWeight: '600'}}};
  const {NativeSeriesRuleSheet} = await screenModule(new URL('../src-next/kit/live/SeriesRuleSheet.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../controls': {Button: 'Button', Chip: 'Chip', Select: 'Select'}, '../Icon': {Icon: 'Icon'}, '../overlays': {Dialog: 'Dialog'}, '../states': {StateView: 'StateView'},
    '../shell/visual': {shellType: () => ({})}, '../theme': {useTheme: () => theme},
    '../../screens/settings-live/recording-rule-presentation': {keepChoiceList: () => [], seriesConfig: () => ({config, anchor: {id: 'occurrence'}})},
  });
  const controller = {previewRule: async (value: unknown) => {calls.push(['preview', value]); return {matches: 4};}, createRule: async (value: unknown) => {calls.push(['create', value]); return {id: 'rule'};}};
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeSeriesRuleSheet({controller, draft: {programme: {title: 'News'}}, defaults: undefined, runtime: {text: {t: (key: string) => key}, error: () => ({})},
    open: true, onOpenChange: (open: boolean) => calls.push(['open', open]), onCreated: () => calls.push(['created'])});});
  try {
    await act(() => host.mount()); await act(async () => {await new Promise(resolve => setImmediate(resolve));});
    assert.deepEqual(calls, [['preview', config]], 'the preview is the server\'s, read once on open');
    const create = nodes(tree).find(node => node.props.testID === 'live-series-create');
    assert.equal(create.props.disabled, false, 'Record series waits for the preview, which has come');
    await act(() => create.props.onPress());
    assert.deepEqual(calls.slice(1), [['create', config], ['created'], ['open', false]]);
  } finally {await host.unmount();}
});
