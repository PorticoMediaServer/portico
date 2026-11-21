import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {storagePage, t} from './helpers/owner-server-settings.mjs';
import {hooks} from './helpers/screen-harness.mjs';

test('Storage & backups is a settings page with the shared list\'s three tabs: Space, Limits and retention, Backups', async () => {
  const host = hooks(), layout = {useLayoutEffect() {}, useEffect() {}};
  const react = {...host.react, ...layout, default: {...host.react.default, ...layout}};
  const page = await storagePage(react, {'../../../../../../packages/client-core/src/presentation/settings-spec':
    {settingsRowIndex: new Map(), settingsSpec: [{scope: 'server', id: 'storage', tabs: [{id: 'space', title: 'Space'}, {id: 'limits', title: 'Limits'}, {id: 'backups', title: 'Backups'}]}]}},
    {'../../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame'}, '../../../kit/controls': {Tabs: 'Tabs', Button: 'Button'}});
  const opened: string[] = [], back = {label: 'Settings', onPress() {}};
  const render = () => host.render(() => page.ServerStoragePage({ports: {context: {}}, runtime: {text: {t}}, back, retry: 'Retry', onOpenPage: (value: string) => opened.push(value)}));
  const tree = render();
  assert.equal(tree.type, 'SettingsPageFrame'); assert.equal(tree.props.title, 'settings.server.storage'); assert.equal(tree.props.back, back);
  assert.deepEqual(tree.props.tabs.props.items.map((item: any) => item.value), ['space', 'limits', 'backups']);
  const [retry, space] = tree.props.children as any[];
  assert.equal(retry, 'Retry');
  assert.deepEqual(space.props.children.map((node: any) => node.type.name), ['StorageSpace', 'EmptyCache']);
});
