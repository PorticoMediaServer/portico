import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('the server chooser returns to Settings and only offers actions for a saved row', async () => {
  const host = hooks();
  const copy = {'../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}};
  const state = await screenModule(new URL('../src-next/pages/shared/accounts/useServerChooser.ts', import.meta.url), {react: host.react, ...copy});
  const {ServerChooserPage} = await screenModule(new URL('../src-next/pages/touch/accounts/ServerChooserPage.tsx', import.meta.url), {
    react: host.react, ...copy, '../../shared/accounts/useServerChooser': state,
    '../../../kit/frames': {BrowseFrame: 'Frame'}, '../../../kit/controls': {Button: 'Button'}, '../../../kit/overlays': {Menu: 'Menu'},
    '../../../kit/states': {StateView: 'State'}, '../../../kit/accounts/AccountParts': {AccountFields: 'Fields'},
    '../../../kit/accounts/ServerChooserParts': {ServerChooserRow: 'Row', ServerChooserScroll: 'Scroll', ServerGroup: 'Group'},
  });
  let returned = false;
  const tree = host.render(() => ServerChooserPage({snapshot: {servers: []}, busy: false,
    onBack: () => {returned = true;}, onSelect() {}, onSignOut() {}, onForget() {}, onRefresh() {}, onAdd() {}}));
  assert.equal(tree.props.header.back.label, 'next.shell.settings');
  tree.props.header.back.onPress();
  assert.equal(returned, true);
  const menu = tree.props.grid.props.children.find((node: {type?: string}) => node?.type === 'Menu');
  assert.equal(menu.props.open, false);
  assert.deepEqual(menu.props.items, []);
});
