import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

/** A TV server Settings page shown read-only: one row per fact, the empty line alone, and the pager only when offered. */
test('TV server facts: rows you can move through, the empty line when there are none, and the pager when offered', async () => {
  const host = hooks();
  const {TVServerFacts} = await screenModule(new URL('../src-next/kit/settings/TVServerFacts.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../controls': {Button: 'Button', Focusable: 'Focusable'}, '../shell/visual': {shellType: () => ({})},
    '../theme': {useTheme: () => ({colors: {raised: '#111', text: '#fff', textSecondary: '#ccc', textTertiary: '#999'}})},
  });
  const moves: string[] = [];
  let tree = nodes(host.render(() => TVServerFacts({page: 'remote', facts: [{id: 'remote.url', label: 'Address', value: 'https://x', detail: 'Reachable'}],
    empty: 'Empty', next: () => moves.push('next')})));
  const row = tree.find(node => node.type === 'Focusable');
  assert.equal(row.props.focusId, 'tv-server:remote:remote.url');
  assert.equal(row.props.accessibilityLabel, 'Address. https://x. Reachable');
  assert.equal(tree.some(node => node === 'Empty'), false);
  assert.equal(tree.some(node => node.props?.focusId === 'tv-server:remote:previous'), false, 'no Previous on the first page');
  tree.find(node => node.props?.focusId === 'tv-server:remote:next').props.onPress();
  assert.deepEqual(moves, ['next']);
  tree = nodes(host.render(() => TVServerFacts({page: 'history', facts: [], empty: 'Empty'})));
  assert.equal(tree.filter(node => node.type === 'Focusable' || node.type === 'Button').length, 0);
});
