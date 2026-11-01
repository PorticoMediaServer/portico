// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const region = (tree: any, id: string) => nodes(tree).find((node: any) => node.type === 'FocusRegion' && node.props.id === id);

/** The real chooser under a TV theme. TV directional focus is native and geometric. Connect24 draws
 * Refresh servers and Use a local server side by side, so the row stays, and Refresh's Down is
 * pointed at Use a local server (the caption's order); Up returns to the server rows. The harness
 * mounts no native views, so a node handle stands in for Use a local server's. */
test('the TV server chooser keeps its actions in a row, Down goes Refresh servers, Use a local server, then Link a TV, and Up returns to the servers', async () => {
  const entries: any[] = [];
  const {TVServerChooser} = await screenModule(new URL('../src-next/screens/accounts/TVServerChooser.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', findNodeHandle: (view: any) => view.handle},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/artwork': {ColourBackdrop: 'ColourBackdrop'},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../kit/focus': {FocusProvider: 'FocusProvider', FocusRegion: 'FocusRegion'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/theme': {nativeType: () => ({}), useTheme: () => ({isTV: true, textScale: 1,
      colors: {ink: '#071018', lineSoft: '#223', accentStrong: '#4cf', text: '#fff', textSecondary: '#ccc', textTertiary: '#aaa', raised: '#123'},
      type: {body: {}, bodyStrong: {}, caption: {}, captionStrong: {}}, layout: {gutter: {tvAuth: 96}}})},
    '../destinations/useEntryFocus': {useDestinationEntryFocus: (...args: any[]) => entries.push(args)},
    '../../tv/auth/entry-revision': {useTVBusyEntryRevision: () => 0},
  });
  let tree: any;
  const host = createFabricHookHost(() => {
    const outer = TVServerChooser({snapshot: {selectedServerId: 'living-room', servers: [
        {id: 'living-room', name: 'Living room', labelKey: 'accounts.servers.local', signIn: 'local'},
        {id: 'office', name: 'Office', labelKey: 'accounts.servers.local', signIn: 'local'},
      ]}, busy: false, owner: false, onSelect() {}, onSignOut() {}, onRefresh() {}, onAdd() {},
      footer: React.createElement('View', null, React.createElement('Button', {variant: 'text'}, defaultI18n.t('settings.linkTV')))});
    const chooser = outer.props.children;
    tree = chooser.type(chooser.props);
  });
  await act(() => host.mount());
  try {
    const actionButtons = () => React.Children.toArray(region(tree, 'accounts.chooser.actions').props.children)
      .filter((child: any) => child?.type === 'Button') as any[];
    const bindAdd = actionButtons()[1].props.ref;
    assert.equal(actionButtons()[0].props.nextFocusDown, undefined, 'an unattached native button has no focus handle');
    await act(() => bindAdd({handle: 42}));
    const actions = region(tree, 'accounts.chooser.actions');
    const rows = region(tree, 'accounts.chooser.rows');
    assert.ok(actions && rows, 'both focus regions are drawn');
    assert.deepEqual(entries.at(-1), [{visible: true, intent: 'enter', revision: 0},
      {regionId: 'accounts.chooser.rows', childId: 'server:office'}], 'focus still enters the first offered server row');
    assert.equal(actions.props.style.flexDirection, 'row', 'the actions sit side by side, as Connect24 draws them');
    assert.deepEqual(actions.props.order, ['refresh', 'add']);
    const buttons = React.Children.toArray(actions.props.children).filter((child: any) => child?.type === 'Button');
    assert.deepEqual(buttons.map((child: any) => child.props.focusId), ['refresh', 'add'], 'Refresh servers comes before Use a local server');
    assert.deepEqual(buttons.map((child: any) => child.props.children),
      [defaultI18n.t('accounts.servers.refresh'), defaultI18n.t('accounts.signIn.local')]);
    assert.ok(buttons[1].props.ref, 'Use a local server is the target Refresh points at');
    assert.equal(buttons[0].props.nextFocusDown, 42, 'Down from Refresh servers goes to Use a local server');
    assert.equal(buttons[1].props.nextFocusDown, undefined, 'Down from Use a local server is left to the screen (Link a TV)');
    assert.deepEqual(actions.props.edges, {up: 'accounts.chooser.rows'}, 'Up from the actions returns to the server rows');
    assert.deepEqual(rows.props.edges, {down: 'accounts.chooser.actions'}, 'the last server row still reaches the actions');
    // Link a TV is the caller's footer, drawn immediately after the stack: Down from the last action reaches it.
    const all = nodes(tree);
    const add = all.find((node: any) => node.type === 'Button' && node.props.focusId === 'add');
    const linkTV = all.find((node: any) => node.type === 'Button' && node.props.children === defaultI18n.t('settings.linkTV'));
    assert.ok(add && linkTV, 'the action stack and the footer Link a TV are drawn');
    assert.ok(all.indexOf(linkTV) > all.indexOf(add), 'Link a TV follows the action stack');
    assert.equal(all.slice(all.indexOf(add) + 1, all.indexOf(linkTV)).some((node: any) => node.type === 'Button' || node.type === 'Focusable'),
      false, 'nothing focusable sits between the last action and Link a TV');
    await act(() => host.rerender());
    assert.equal(actionButtons()[1].props.ref, bindAdd, 'rerendering preserves the native ref callback');
    await act(() => bindAdd(null));
    assert.equal(actionButtons()[0].props.nextFocusDown, undefined, 'detaching clears the old focus destination');
    await act(() => bindAdd({handle: 73}));
    assert.equal(actionButtons()[0].props.nextFocusDown, 73, 'a replacement native button supplies the new focus destination');
  } finally {await host.unmount();}
});
