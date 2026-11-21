import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsSectionTab, settingsSpec} from '../src/presentation/settings-spec.ts';

const section = (id: string) => settingsSpec.find(entry => entry.scope === 'server' && entry.id === id)!;

test('a page with tabs puts every group and every Advanced group in exactly one tab (owner-server 8, live-owner 1)', () => {
  for (const entry of settingsSpec.filter(candidate => candidate.tabs)) {
    const groups = entry.tabs!.flatMap(tab => tab.groups), advanced = entry.tabs!.flatMap(tab => tab.advanced ?? []);
    assert.deepEqual([...groups].sort((a, b) => a - b), (entry.groups ?? []).map((_, index) => index), `${entry.id} groups`);
    assert.deepEqual([...advanced].sort((a, b) => a - b), (entry.advanced?.groups ?? []).map((_, index) => index), `${entry.id} advanced`);
  }
  assert.deepEqual(section('storage').tabs!.map(tab => tab.id), ['space', 'limits', 'backups']);
  assert.deepEqual(section('live').tabs!.map(tab => tab.id), ['sources', 'recording', 'library']);
});

test('a row opens on the tab that holds it, Advanced rows included', () => {
  assert.equal(settingsSectionTab(section('storage'), 'storage.usage')?.id, 'space');
  assert.equal(settingsSectionTab(section('storage'), 'storage.keepNotifications')?.id, 'limits');
  assert.equal(settingsSectionTab(section('storage'), 'storage.backupPassphrase')?.id, 'backups');
  assert.equal(settingsSectionTab(section('live'), 'live.rec.keep')?.id, 'recording');
  assert.equal(settingsSectionTab(section('live'), 'live.buffer')?.id, 'sources');
  assert.equal(settingsSectionTab(section('streaming'), 'streaming.downloadQuality'), undefined, 'a page without tabs has none');
});
