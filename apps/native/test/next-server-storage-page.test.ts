import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {storagePage, t} from './helpers/owner-server-settings.mjs';

test('Storage & backups is a settings page: what uses space, Empty cache, then Backups with Messages under Advanced', async () => {
  const page = await storagePage({...React, default: React}, {}, {'../../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame'}});
  const opened: string[] = [], back = {label: 'Settings', onPress() {}};
  const tree = page.ServerStoragePage({ports: {context: {}}, runtime: {text: {t}}, back, retry: 'Retry', onOpenPage: (value: string) => opened.push(value)});
  assert.equal(tree.type, 'SettingsPageFrame');
  assert.equal(tree.props.title, 'settings.server.storage'); assert.equal(tree.props.back, back);
  const [retry, space, cache, backups] = React.Children.toArray(tree.props.children) as any[];
  assert.equal(retry, 'Retry');
  assert.deepEqual([space.type.name, cache.type.name, backups.type.name], ['StorageSpace', 'EmptyCache', 'ServerBackups']);
  assert.equal(backups.props.advancedContent.type.name, 'KeepRows');
  backups.props.advancedContent.props.onOpenPage('troubleshooting'); assert.deepEqual(opened, ['troubleshooting']);
});
